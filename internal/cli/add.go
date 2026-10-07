package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/tu-usuario/lray/internal/config"
	"github.com/tu-usuario/lray/internal/liferay"
	"github.com/tu-usuario/lray/internal/ui"
)

func newAdd() *cobra.Command {
	var javaHome, version string
	var yes bool
	cmd := &cobra.Command{
		Use:   "add <nombre> <ruta>",
		Short: "Añade a la lista un workspace o bundle que ya tienes",
		Example: `  lray add tienda ~/proyectos/tienda-workspace
  lray add antiguo /opt/liferay-7.2 --java /usr/lib/jvm/java-11`,
		Args: cobra.ExactArgs(2),
		ValidArgsFunction: func(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
			if len(args) == 1 {
				return nil, cobra.ShellCompDirectiveFilterDirs
			}
			return nil, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(_ *cobra.Command, args []string) error {
			name, path := args[0], args[1]
			if err := config.ValidName(name); err != nil {
				return err
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
					"No la he podido detectar. Es solo informativa para lray list; puedes dejarla vacía.",
					"dxp-2025.q2.12-lts")
				if err != nil {
					return err
				}
			}

			reg.Put(config.Server{Name: name, Path: l.Root(), Version: version, JavaHome: javaHome})
			if err := reg.Save(); err != nil {
				return err
			}

			lines := []string{
				ui.MutedText("Ruta     ") + ui.ShortPath(l.Root()),
				ui.MutedText("Versión  ") + orDash(version),
				ui.MutedText("Puerto   ") + fmt.Sprint(l.Port()),
			}
			if javaHome != "" {
				lines = append(lines, ui.MutedText("Java     ")+ui.ShortPath(javaHome))
			}
			if !l.HasBundle() {
				lines = append(lines, "", ui.WarnText("Aún no tiene bundle: ")+ui.Code("lray start "+name)+" te ofrecerá descargarlo.")
			}
			ui.Say(ui.Party, fmt.Sprintf("«%s» añadido", name), lines...)
			return nil
		},
	}
	cmd.Flags().StringVar(&javaHome, "java", "", "JAVA_HOME con el que arrancar este server (útil si mezclas 7.x y trimestrales)")
	cmd.Flags().StringVar(&version, "version", "", "Versión a mostrar si no se detecta sola")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "No preguntar nada")
	return cmd
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
