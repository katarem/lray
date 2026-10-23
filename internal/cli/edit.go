package cli

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

	"github.com/katarem/lray/internal/config"
	"github.com/katarem/lray/internal/liferay"
	"github.com/katarem/lray/internal/ssh"
	"github.com/katarem/lray/internal/ui"
)

func newEdit() *cobra.Command {
	return &cobra.Command{
		Use:         "edit <nombre>",
		Annotations: changesServers,
		Short:       "Edita un server con un formulario: nombre, ruta, puerto, versión y más",
		Long: `Abre un formulario con los datos de un server ya añadido para cambiar lo
que quieras. Antes de guardar te enseña qué cambia y te pide confirmación.

En los locales: nombre, ruta, puerto, versión y Java. En los remotos: nombre,
máquina, puerto SSH, ruta, puerto, versión, patrón de logs y cómo se arranca
y se para. Las reglas son las de lray server set (por ejemplo, un local tiene
que estar apagado para cambiarle la ruta o el puerto).

Necesita una terminal; para cambios rápidos o desde scripts, lray server set.`,
		Example:           `  lray server edit turismo`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeServers,
		RunE: func(_ *cobra.Command, args []string) error {
			if !ui.IsTTY() {
				return fmt.Errorf("el formulario de edit necesita una terminal; sin ella usa %s", ui.Code("lray server set"))
			}
			reg, s, err := findServer(args[0])
			if err != nil {
				return err
			}
			c, err := editForm(reg, s)
			if err != nil {
				return err
			}
			old, lines, write, err := applyChanges(reg, s, c)
			if err != nil {
				return err
			}
			if len(lines) == 0 {
				ui.Say(ui.Sleepy, "No has cambiado nada.")
				return nil
			}
			ui.Say(ui.Thinking, fmt.Sprintf("Esto cambia en «%s»", old.Name), lines...)
			ok, err := ui.Confirm("¿Lo guardo?", "", true)
			if err != nil {
				return err
			}
			if !ok {
				ui.Say(ui.Sleepy, "Vale, lo dejo como estaba.")
				return nil
			}
			hint, err := saveChanges(reg, s, old, write)
			if err != nil {
				return err
			}
			if len(hint) > 0 {
				hint = hint[1:] // sin la línea en blanco que la separa del resumen
			}
			ui.Say(ui.Happy, fmt.Sprintf("«%s» guardado", s.Name), hint...)
			return nil
		},
	}
}

// editForm enseña el formulario con los datos actuales y devuelve solo lo
// que el usuario ha cambiado.
func editForm(reg *config.Registry, s *config.Server) (serverChanges, error) {
	var c serverChanges
	remote := s.IsRemote()
	running := !remote && localRunning(s)

	name, path, version, java := s.Name, s.Path, s.Version, s.JavaHome
	port := portText(s.Port)
	if !remote {
		if l, err := liferay.Resolve(s.Path); err == nil && l.HasBundle() {
			port = fmt.Sprint(l.Port()) // el del server.xml es el que vale
		}
	}
	host, sshPort, logPat := s.Host, portText(sshPortOf(s)), ""
	if s.Remote != nil {
		logPat = s.Remote.Log
	}
	origPort, origSSHPort := port, sshPort
	stopFirst := ""
	if running {
		stopFirst = "\nEstá encendido: páralo antes de cambiarlo."
	}

	nameField := huh.NewInput().Title("Nombre").Value(&name).Validate(func(v string) error {
		v = strings.TrimSpace(v)
		if v == s.Name {
			return nil
		}
		if err := config.ValidName(v); err != nil {
			return err
		}
		if _, ok := reg.Get(v); ok {
			return fmt.Errorf("ya hay un server llamado «%s»", v)
		}
		return nil
	})
	portField := huh.NewInput().Title("Puerto HTTP").Value(&port).Validate(func(v string) error {
		v = strings.TrimSpace(v)
		if p, err := strconv.Atoi(v); err != nil || p < 1 || p > 65535 {
			return errors.New("pon un puerto entre 1 y 65535")
		}
		if running && v != origPort {
			return errors.New("está encendido; páralo antes de cambiarle el puerto")
		}
		return nil
	})
	versionField := huh.NewInput().Title("Versión").
		Description("La que enseñan list y check. Vacía = que lray la busque.").Value(&version)

	var fields []huh.Field
	if remote {
		fields = []huh.Field{
			nameField,
			huh.NewInput().Title("Máquina").Description("usuario@máquina o un alias de ~/.ssh/config.").
				Value(&host).Validate(func(v string) error { return ssh.ValidDest(strings.TrimSpace(v)) }),
			huh.NewInput().Title("Puerto SSH").Description("Vacío = el 22 o el de ~/.ssh/config.").
				Value(&sshPort).Validate(func(v string) error {
				if v = strings.TrimSpace(v); v == "" {
					return nil
				}
				if p, err := strconv.Atoi(v); err != nil || p < 1 || p > 65535 {
					return errors.New("pon un puerto entre 1 y 65535, o déjalo vacío")
				}
				return nil
			}),
			huh.NewInput().Title("Ruta").Description("El liferay home en esa máquina (o la carpeta de su JBoss/Tomcat).").
				Value(&path).Validate(required("dime la ruta")),
			portField.Description("En el que lray le pregunta; si el portal contesta en otro, lray se queda con ese."),
			versionField,
			huh.NewInput().Title("Logs").
				Description("Patrón de logs, relativo al liferay home si no es absoluto. Vacío = " + liferay.DefaultRemoteLog + ".").
				Placeholder(liferay.DefaultRemoteLog).Value(&logPat),
		}
	} else {
		fields = []huh.Field{
			nameField,
			huh.NewInput().Title("Ruta").Description("Raíz del workspace, carpeta bundles/ o tomcat-*." + stopFirst).
				Value(&path).Validate(func(v string) error {
				v = strings.TrimSpace(v)
				if v == s.Path {
					return nil
				}
				if running {
					return errors.New("está encendido; páralo antes de cambiarle la ruta")
				}
				if _, _, _, isRemote, _ := parseRemoteSpec(v); isRemote {
					return errors.New("para llevarlo a otra máquina, quítalo con lray server rm y añádelo con lray server add")
				}
				_, err := liferay.Resolve(v)
				return err
			}),
			portField.Description("Desplaza todos los puertos del server.xml; sin bundle, se aplica al descargarlo." + stopFirst),
			versionField,
			huh.NewInput().Title("Java").Description("JAVA_HOME con el que arrancarlo. Vacío = el del sistema.").
				Value(&java).Validate(func(v string) error {
				if v = strings.TrimSpace(v); v == "" || v == s.JavaHome {
					return nil
				}
				_, err := checkJava(v)
				return err
			}),
		}
	}
	changeCtrl := false
	if remote {
		fields = append(fields, huh.NewConfirm().Title("¿Cambias cómo se arranca y se para?").
			Description("Ahora: "+controlText(s.Remote)).Affirmative("Sí").Negative("No").Value(&changeCtrl))
	}
	if err := huh.NewForm(huh.NewGroup(fields...)).WithTheme(ui.Theme()).Run(); err != nil {
		return c, err
	}

	str := func(v, orig string, dst **string) {
		if v = strings.TrimSpace(v); v != orig {
			*dst = &v
		}
	}
	num := func(v, orig string, dst **int) {
		if v = strings.TrimSpace(v); v != orig {
			n, _ := strconv.Atoi(v) // ya validado; vacío = 0
			*dst = &n
		}
	}
	str(name, s.Name, &c.name)
	str(path, s.Path, &c.path)
	num(port, origPort, &c.port)
	str(version, s.Version, &c.version)
	if remote {
		str(host, s.Host, &c.host)
		num(sshPort, origSSHPort, &c.sshPort)
		orig := ""
		if s.Remote != nil {
			orig = s.Remote.Log
		}
		str(logPat, orig, &c.log)
	} else {
		str(java, s.JavaHome, &c.javaHome)
	}
	if changeCtrl {
		cur := config.Control{}
		if s.Remote != nil {
			cur = s.Remote.Control
		}
		ctrl, err := controlForm(cur, nil)
		if err != nil {
			return c, err
		}
		if ctrl != cur {
			c.control = &ctrl
		}
	}
	return c, nil
}
