package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/katarem/lray/internal/config"
	"github.com/katarem/lray/internal/liferay"
	"github.com/katarem/lray/internal/ssh"
	"github.com/katarem/lray/internal/ui"
)

func newSet() *cobra.Command {
	var name, path, version, javaHome, logPat string
	var port int
	cmd := &cobra.Command{
		Use:         "set <nombre>",
		Annotations: changesServers,
		Short:       "Cambia a mano el nombre, la ruta, el puerto, la versión, el Java o los logs de un server",
		Long: `Cambia a mano datos de un server ya añadido. Solo toca lo que le indiques
y no pregunta nada (para un formulario con todo, lray server edit).

--name le cambia el nombre en la lista (sus archivos no se tocan).

--path lo apunta a otra carpeta: la raíz del workspace, la carpeta bundles/
o la tomcat-*, igual que en add. En un remoto es la ruta en esa misma
máquina, o usuario@máquina:/ruta para llevarlo a otra. Un server local tiene
que estar apagado para cambiarle la ruta.

--port cambia el puerto HTTP. En los locales desplaza todos los puertos del
server.xml (apagado, AJP, redirect...) la misma cantidad, así que tiene que
estar apagado; si aún no tiene bundle, se aplica al descargarlo. En los
remotos solo cambia el puerto en el que lray pregunta: si el portal contesta
en otro, lray se queda con ese.

--version fija la versión que enseñan list y check, para cuando lray no la
puede averiguar. Si más adelante la detecta (en el portal desplegado o en sus
logs), la sustituye por la detectada. Con --version "" se borra y lray vuelve
a buscarla.

--java (solo locales) es el JAVA_HOME con el que se arranca, se para y se
despliega; con --java "" vuelve a usar el del sistema.

--log (solo remotos) es el patrón de sus logs, relativo al liferay home si no
es absoluto; con --log "" vuelve al de por defecto (` + liferay.DefaultRemoteLog + `).`,
		Example: `  lray server set turismo --name turismo-74
  lray server set turismo --path ~/proyectos/turismo-workspace
  lray server set turismo --port 9080
  lray server set turismo --java /usr/lib/jvm/java-21
  lray server set pre --path usuario@pre2-liferay:/opt/liferay
  lray server set pre --log "/var/log/liferay/liferay.*.log"
  lray server set validacion-turismo --version "DXP 2025.Q1.5 LTS"
  lray server set validacion-turismo --version ""`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeServers,
		RunE: func(cmd *cobra.Command, args []string) error {
			fl := cmd.Flags()
			var c serverChanges
			if fl.Changed("name") {
				c.name = &name
			}
			if fl.Changed("path") {
				c.path = &path
			}
			if fl.Changed("port") {
				c.port = &port
			}
			if fl.Changed("version") {
				c.version = &version
			}
			if fl.Changed("java") {
				c.javaHome = &javaHome
			}
			if fl.Changed("log") {
				c.log = &logPat
			}
			if c == (serverChanges{}) {
				return errors.New("dime qué cambiar: --name, --path, --port, --version, --java o --log")
			}
			reg, s, err := findServer(args[0])
			if err != nil {
				return err
			}
			old, lines, write, err := applyChanges(reg, s, c)
			if err != nil {
				return err
			}
			if len(lines) == 0 {
				ui.Say(ui.Sleepy, fmt.Sprintf("«%s» ya estaba así", s.Name))
				return nil
			}
			hint, err := saveChanges(reg, s, old, write)
			if err != nil {
				return err
			}
			ui.Say(ui.Happy, fmt.Sprintf("«%s» actualizado", s.Name), append(lines, hint...)...)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Nombre nuevo")
	cmd.Flags().StringVar(&path, "path", "", "Ruta nueva (en remotos, también usuario@máquina:/ruta)")
	cmd.Flags().IntVar(&port, "port", 0, "Puerto HTTP nuevo")
	cmd.Flags().StringVar(&version, "version", "", "Versión que enseñar (\"\" para que lray la vuelva a buscar)")
	cmd.Flags().StringVar(&javaHome, "java", "", "Locales: JAVA_HOME con el que arrancarlo (\"\" = el del sistema)")
	cmd.Flags().StringVar(&logPat, "log", "", "Remotos: patrón de logs (\"\" = "+liferay.DefaultRemoteLog+")")
	return cmd
}

// serverChanges son los cambios que se piden a un server (set o edit); nil =
// no se toca. host, sshPort, log y control solo valen para remotos y javaHome
// solo para locales.
type serverChanges struct {
	name, path, version, javaHome *string
	port                          *int
	host, log                     *string
	sshPort                       *int
	control                       *config.Control
}

// applyChanges aplica los cambios a s, solo en memoria, y devuelve cómo era
// antes, las líneas del resumen (ninguna = nada cambia) y lo que hay que
// escribir en el bundle (el server.xml) al guardar.
func applyChanges(reg *config.Registry, s *config.Server, c serverChanges) (old config.Server, lines []string, write func() error, err error) {
	old = *s
	if s.IsRemote() {
		rm := config.Remote{}
		if s.Remote != nil {
			rm = *s.Remote // copia: old conserva el original (sirve para cerrar la sesión vieja)
		}
		s.Remote = &rm
	}
	write = func() error { return nil }
	fail := func(err error) (config.Server, []string, func() error, error) { return old, nil, nil, err }

	if c.port != nil && (*c.port < 1 || *c.port > 65535) {
		return fail(fmt.Errorf("puerto no válido: %d", *c.port))
	}
	if s.IsRemote() && c.javaHome != nil {
		return fail(errors.New("el Java solo se elige en servers locales; en el remoto se usa el Java con el que arranque su servicio"))
	}
	if !s.IsRemote() && c.log != nil {
		return fail(errors.New("el patrón de logs solo se elige en servers remotos; en los locales lray sigue los de su bundle"))
	}
	if !s.IsRemote() && (c.host != nil || c.sshPort != nil || c.control != nil) {
		return fail(errors.New("la máquina, el puerto SSH y la gestión solo se cambian en servers remotos"))
	}
	if c.name != nil && *c.name != s.Name {
		if err := config.ValidName(*c.name); err != nil {
			return fail(err)
		}
		if _, ok := reg.Get(*c.name); ok {
			return fail(fmt.Errorf("ya hay un server llamado «%s»", *c.name))
		}
		s.Name = *c.name
	}
	if c.path != nil {
		if s.IsRemote() {
			err = setRemotePath(s, *c.path)
		} else {
			err = setLocalPath(reg, s, *c.path, c.version == nil)
		}
		if err != nil {
			return fail(err)
		}
	}
	if c.host != nil {
		if err := ssh.ValidDest(*c.host); err != nil {
			return fail(err)
		}
		s.Host = *c.host
	}
	if c.sshPort != nil {
		if *c.sshPort < 0 || *c.sshPort > 65535 {
			return fail(fmt.Errorf("puerto SSH no válido: %d", *c.sshPort))
		}
		s.Remote.SSHPort = *c.sshPort
	}
	if c.port != nil {
		if s.IsRemote() {
			s.Port = *c.port
		} else if write, err = setLocalPort(reg, s, *c.port, &old.Port); err != nil {
			return fail(err)
		}
	}
	if c.version != nil {
		s.Version = *c.version
	}
	if c.javaHome != nil {
		s.JavaHome = *c.javaHome
		if s.JavaHome != "" {
			if s.JavaHome, err = checkJava(s.JavaHome); err != nil {
				return fail(err)
			}
		}
	}
	if c.log != nil {
		s.Remote.Log = *c.log
	}
	if c.control != nil {
		s.Remote.Control = *c.control
	}

	change := func(label, from, to string) {
		if from != to {
			lines = append(lines, ui.MutedText(label)+orDash(from)+ui.MutedText(" → ")+orDash(to))
		}
	}
	change("Nombre     ", old.Name, s.Name)
	change("Ruta       ", shortLocation(&old), shortLocation(s))
	change("Puerto SSH ", portText(sshPortOf(&old)), portText(sshPortOf(s)))
	change("Puerto     ", portText(old.Port), portText(s.Port))
	change("Versión    ", old.Version, s.Version)
	change("Java       ", shortPathOr(old.JavaHome), shortPathOr(s.JavaHome))
	if s.IsRemote() {
		change("Log        ", logPattern(&old), logPattern(s))
		change("Gestión    ", controlText(old.Remote), controlText(s.Remote))
	}
	return old, lines, write, nil
}

// saveChanges escribe lo pendiente en el bundle, guarda el registro y lleva
// el último estado conocido al nombre o la ubicación nuevos. Devuelve líneas
// de pista para el resumen.
func saveChanges(reg *config.Registry, s *config.Server, old config.Server, write func() error) ([]string, error) {
	if err := write(); err != nil {
		return nil, err
	}
	if err := reg.Save(); err != nil {
		return nil, err
	}
	otherMachine := s.Host != old.Host || sshPortOf(s) != sshPortOf(&old)
	switch {
	case s.IsRemote() && (otherMachine || s.Path != old.Path):
		config.ForgetState(old.Name) // era el estado de otra ubicación
		if otherMachine {
			conn, _ := remoteOf(&old)
			_ = conn.Disconnect()
		}
		return []string{"", ui.MutedText("Compruébalo con ") + ui.Code("lray server check "+s.Name)}, nil
	case s.Name != old.Name:
		config.RenameState(old.Name, s.Name)
	}
	return nil, nil
}

// setLocalPath apunta un server local a otra carpeta. Si no se ha indicado
// la versión, se queda con la del bundle nuevo cuando se puede detectar.
func setLocalPath(reg *config.Registry, s *config.Server, path string, detect bool) error {
	if _, _, _, remote, err := parseRemoteSpec(path); err != nil {
		return err
	} else if remote {
		return fmt.Errorf("«%s» es un server local; para llevarlo a otra máquina quítalo con %s y vuelve a añadirlo con %s",
			s.Name, ui.Code("lray server rm"), ui.Code("lray server add"))
	}
	l, err := liferay.Resolve(path)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if l.Root() == s.Path {
		return nil
	}
	if localRunning(s) {
		return errors.New("está encendido; páralo antes de cambiarle la ruta")
	}
	for _, o := range reg.Servers {
		if o.Name != s.Name && !o.IsRemote() && o.Path == l.Root() {
			ui.Warn(fmt.Sprintf("«%s» ya apunta a %s.", o.Name, ui.ShortPath(l.Root())))
			break
		}
	}
	s.Path = l.Root()
	if l.HasBundle() {
		s.Port = l.Port()
	}
	if detect {
		if v := liferay.DetectVersion(l); v != "" {
			s.Version = v
		}
	}
	return nil
}

// localRunning dice si lray tiene encendido el server local (por su PID).
func localRunning(s *config.Server) bool {
	l, err := liferay.Resolve(s.Path)
	if err != nil {
		return false
	}
	_, alive := l.PID()
	return alive
}

// setRemotePath cambia la ruta de un remoto en la misma máquina o, con
// usuario@máquina:/ruta, lo lleva a otra.
func setRemotePath(s *config.Server, path string) error {
	dest, sshPort, rpath, remote, err := parseRemoteSpec(path)
	if err != nil {
		return err
	}
	if !remote {
		s.Path = path
		return nil
	}
	s.Host, s.Path = dest, rpath
	if sshPort != 0 {
		s.Remote.SSHPort = sshPort
	}
	return nil
}

// setLocalPort cambia el puerto de un server local: si ya tiene bundle, en el
// server.xml (lo devuelve en write, para hacerlo al guardar) y, si no, solo en
// el registro para aplicarlo tras initBundle. Con bundle, deja en from el
// puerto que tenía de verdad (el del server.xml).
func setLocalPort(reg *config.Registry, s *config.Server, port int, from *int) (write func() error, err error) {
	write = func() error { return nil }
	l, err := liferay.Resolve(s.Path)
	if err != nil {
		return nil, fmt.Errorf("la ruta de «%s» (%s) ya no es válida: %w; cámbiala con --path", s.Name, ui.ShortPath(s.Path), err)
	}
	if l.HasBundle() {
		current := l.Port()
		*from = current
		if port != current {
			if l.State() != liferay.Stopped {
				return nil, fmt.Errorf("está encendido en el puerto %d; páralo antes de cambiarle el puerto", current)
			}
			warnTakenPort(reg, s, l, port)
			write = func() error {
				if err := l.SetPort(port); err != nil {
					return fmt.Errorf("no he podido cambiar el puerto en el server.xml: %w", err)
				}
				return nil
			}
		}
	} else if port != s.Port {
		warnTakenPort(reg, s, l, port)
	}
	s.Port = port
	return write, nil
}

func warnTakenPort(reg *config.Registry, s *config.Server, l *liferay.Layout, port int) {
	if who, ok := takenPorts(reg, s.Name, l.Root())[port]; ok {
		ui.Warn(fmt.Sprintf("El puerto %d ya lo tiene asignado «%s»; no podréis estar encendidos a la vez.", port, who))
	} else if liferay.PortInUse(port) {
		ui.Warn(fmt.Sprintf("Ahora mismo hay algo escuchando en el puerto %d; Tomcat fallará si sigue ocupado al arrancar.", port))
	}
}

func shortLocation(s *config.Server) string {
	if s.IsRemote() {
		return s.Location()
	}
	return ui.ShortPath(s.Path)
}

func shortPathOr(p string) string {
	if p == "" {
		return ""
	}
	return ui.ShortPath(p)
}

func sshPortOf(s *config.Server) int {
	if s.Remote == nil {
		return 0
	}
	return s.Remote.SSHPort
}

func logPattern(s *config.Server) string {
	if s.Remote == nil || s.Remote.Log == "" {
		return liferay.DefaultRemoteLog
	}
	return s.Remote.Log
}

// controlText describe la gestión de un remoto, con los comandos si son propios.
func controlText(rm *config.Remote) string {
	if rm == nil {
		return config.Control{}.Describe()
	}
	c := rm.Control
	if c.Kind != config.ControlCustom {
		return c.Describe()
	}
	return strings.Join([]string{c.Describe(),
		"arranque: " + orDash(c.Start), "parada: " + orDash(c.Stop), "estado: " + orDash(c.Status)}, " · ")
}

func portText(port int) string {
	if port == 0 {
		return ""
	}
	return fmt.Sprint(port)
}
