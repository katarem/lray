package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/katarem/lray/internal/config"
	"github.com/katarem/lray/internal/liferay"
	"github.com/katarem/lray/internal/ui"
)

func newAdd() *cobra.Command {
	var javaHome, version string
	var port int
	var yes bool
	cmd := &cobra.Command{
		Use:   "add <nombre> <ruta>",
		Short: "Añade a la lista un workspace o bundle que ya tienes",
		Example: `  lray server add tienda ~/proyectos/tienda-workspace
  lray server add antiguo /opt/liferay-7.2 --java /usr/lib/jvm/java-11
  lray server add otro ~/proyectos/otro-workspace --port 9080`,
		Args: cobra.ExactArgs(2),
		ValidArgsFunction: func(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
			if len(args) == 1 {
				return nil, cobra.ShellCompDirectiveFilterDirs
			}
			return nil, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			name, path := args[0], args[1]
			if err := config.ValidName(name); err != nil {
				return err
			}
			portSet := cmd.Flags().Changed("port")
			if portSet && (port < 1 || port > 65535) {
				return fmt.Errorf("puerto no válido: %d", port)
			}
			l, err := liferay.Resolve(path)
			if err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
			if javaHome != "" {
				if javaHome, err = checkJava(javaHome); err != nil {
					return err
				}
			}
			reg, err := config.Load()
			if err != nil {
				return err
			}
			if old, ok := reg.Get(name); ok && !yes {
				replace, err := ui.Confirm(
					fmt.Sprintf("Ya hay un server llamado «%s»", name),
					"Ahora apunta a "+ui.ShortPath(old.Path)+". ¿Lo reemplazo por la nueva ruta?", false)
				if err != nil {
					return err
				}
				if !replace {
					ui.Say(ui.Sleepy, "Vale, lo dejo como estaba.")
					return nil
				}
			}

			if version == "" {
				version = liferay.DetectVersion(l)
			}
			if version == "" && !yes {
				version, err = ui.Input("¿Qué versión de Liferay es?",
					"No la he podido detectar. Es solo informativa para lray server list; puedes dejarla vacía.",
					"dxp-2025.q2.12-lts")
				if err != nil {
					return err
				}
			}

			current, running := l.Port(), l.State() != liferay.Stopped
			taken := takenPorts(reg, name, l.Root())
			var portNote string
			switch {
			case portSet:
				if who, ok := taken[port]; ok {
					ui.Warn(fmt.Sprintf("El puerto %d ya lo tiene asignado «%s»; no podréis estar encendidos a la vez.", port, who))
				} else if port != current && liferay.PortInUse(port) {
					ui.Warn(fmt.Sprintf("Ahora mismo hay algo escuchando en el puerto %d; Tomcat fallará si sigue ocupado al arrancar.", port))
				}
			case running:
				port = current // está encendido: ese puerto lo ocupa él mismo
			default:
				if port, err = freePort(l, taken); err != nil {
					return err
				}
				if port != current {
					portNote = fmt.Sprintf("el %d estaba ocupado", current)
					if who, ok := taken[current]; ok {
						portNote = fmt.Sprintf("el %d lo usa «%s»", current, who)
					}
				}
			}
			if l.HasBundle() && port != current {
				if running {
					return fmt.Errorf("está encendido en el puerto %d; páralo antes de cambiarle el puerto", current)
				}
				if err := l.SetPort(port); err != nil {
					return fmt.Errorf("no he podido cambiar el puerto en el server.xml: %w", err)
				}
			}

			reg.Put(config.Server{Name: name, Path: l.Root(), Version: version, JavaHome: javaHome, Port: port})
			if err := reg.Save(); err != nil {
				return err
			}

			portLine := ui.MutedText("Puerto   ") + fmt.Sprint(port)
			if portNote != "" {
				portLine += ui.MutedText(" (" + portNote + ")")
			}
			lines := []string{
				ui.MutedText("Ruta     ") + ui.ShortPath(l.Root()),
				ui.MutedText("Versión  ") + orDash(version),
				portLine,
			}
			if javaHome != "" {
				lines = append(lines, ui.MutedText("Java     ")+ui.ShortPath(javaHome))
			}
			if !l.HasBundle() {
				lines = append(lines, "", ui.WarnText("Aún no tiene bundle: ")+ui.Code("lray server start "+name)+" te ofrecerá descargarlo.")
			}
			ui.Say(ui.Party, fmt.Sprintf("«%s» añadido", name), lines...)
			return nil
		},
	}
	cmd.Flags().StringVar(&javaHome, "java", "", "JAVA_HOME con el que arrancar este server (útil si mezclas 7.x y trimestrales)")
	cmd.Flags().StringVar(&version, "version", "", "Versión a mostrar si no se detecta sola")
	cmd.Flags().IntVar(&port, "port", 0, "Puerto HTTP; si no lo indicas, uso el del bundle o el siguiente libre")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "No preguntar nada")
	return cmd
}

// takenPorts reúne los puertos que ocupan los demás servers registrados
// (aunque estén apagados), con el nombre de quien los usa.
func takenPorts(reg *config.Registry, except, root string) map[int]string {
	taken := map[int]string{}
	for _, s := range reg.Servers {
		if s.Name == except || s.Path == root {
			continue
		}
		l, err := liferay.Resolve(s.Path)
		if err != nil {
			continue
		}
		ports := l.Ports()
		if !l.HasBundle() && s.Port != 0 {
			ports = liferay.ShiftPorts(ports, s.Port-liferay.DefaultPort)
		}
		for _, p := range ports {
			if _, ok := taken[p]; !ok {
				taken[p] = s.Name
			}
		}
	}
	return taken
}

// freePort busca, desde el puerto actual del bundle hacia arriba, el primero
// cuyo juego completo de puertos (HTTP, apagado...) esté libre en la máquina
// y no lo tenga asignado otro server.
func freePort(l *liferay.Layout, taken map[int]string) (int, error) {
	base, current := l.Ports(), l.Port()
	for d := 0; d < 200 && current+d <= 65535; d++ {
		free := true
		for _, p := range liferay.ShiftPorts(base, d) {
			if _, ok := taken[p]; ok || p > 65535 || liferay.PortInUse(p) {
				free = false
				break
			}
		}
		if free {
			return current + d, nil
		}
	}
	return 0, fmt.Errorf("no encuentro un puerto libre a partir del %d; indícalo con --port", current)
}

func checkJava(p string) (string, error) {
	abs, err := filepath.Abs(liferay.ExpandHome(p))
	if err != nil {
		return "", err
	}
	for _, bin := range []string{"java", "java.exe"} {
		if _, err := os.Stat(filepath.Join(abs, "bin", bin)); err == nil {
			return abs, nil
		}
	}
	return "", fmt.Errorf("%s no parece un JAVA_HOME (no encuentro bin/java)", abs)
}
