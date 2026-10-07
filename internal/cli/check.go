package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/katarem/lray/internal/config"
	"github.com/katarem/lray/internal/liferay"
	"github.com/katarem/lray/internal/ssh"
	"github.com/katarem/lray/internal/ui"
)

func newCheck() *cobra.Command {
	return &cobra.Command{
		Use:   "check <nombre>",
		Short: "Comprueba un server ahora mismo y enseña todo lo que sé de él",
		Long: `Comprueba un server ahora mismo y enseña todo lo que sé de él: dónde
está, versión de Liferay, servidor de aplicaciones, puerto, cómo se gestiona,
qué log se sigue y su estado real. Con los remotos se conecta por SSH y
actualiza el último estado conocido que enseña lray server list.`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeServers,
		RunE: func(cmd *cobra.Command, args []string) error {
			reg, s, err := findServer(args[0])
			if err != nil {
				return err
			}
			if s.IsRemote() {
				return checkRemote(cmd.Context(), reg, s)
			}
			return checkLocal(s)
		},
	}
}

func checkLocal(s *config.Server) error {
	l, err := liferay.Resolve(s.Path)
	if err != nil {
		return fmt.Errorf("la ruta de «%s» (%s) ya no es válida: %w", s.Name, ui.ShortPath(s.Path), err)
	}
	version := s.Version
	if version == "" {
		version = liferay.DetectVersion(l)
	}
	lines := []string{ui.MutedText("Tipo       ") + "local"}
	if l.Workspace != "" {
		lines = append(lines, ui.MutedText("Workspace  ")+ui.ShortPath(l.Workspace))
	}
	lines = append(lines,
		ui.MutedText("Home       ")+ui.ShortPath(l.Home),
		ui.MutedText("Tomcat     ")+orDash(ui.ShortPath(l.Tomcat)),
		ui.MutedText("Versión    ")+orDash(version),
	)
	if s.JavaHome != "" {
		lines = append(lines, ui.MutedText("Java       ")+ui.ShortPath(s.JavaHome))
	}
	if !l.HasBundle() {
		lines = append(lines, ui.MutedText("Estado     ")+ui.MutedText("○ sin bundle"))
		ui.Say(ui.Sleepy, fmt.Sprintf("«%s»", s.Name), lines...)
		return nil
	}
	st := l.State()
	state := stateLabel(st)
	if pid, alive := l.PID(); alive {
		state += ui.MutedText(fmt.Sprintf(" · PID %d", pid))
	}
	lines = append(lines,
		ui.MutedText("Puerto     ")+fmt.Sprint(l.Port())+ui.MutedText("  "+url(l.Port())),
		ui.MutedText("Log        ")+ui.ShortPath(l.LogFile()),
		ui.MutedText("Estado     ")+state,
	)
	ui.Say(stateMood(st), fmt.Sprintf("«%s»", s.Name), lines...)
	return nil
}

func checkRemote(ctx context.Context, reg *config.Registry, s *config.Server) error {
	conn, r := remoteOf(s)
	if err := connect(ctx, s, conn); err != nil {
		config.RecordState(s.Name, "", err)
		return err
	}
	var info *liferay.RemoteInfo
	err := ui.RunTask(fmt.Sprintf("Comprobando «%s»", s.Name), "Comprobado",
		func(ctx context.Context, _ func(string)) error {
			var err error
			info, err = r.Inspect(ctx, true)
			return err
		})
	if err != nil {
		err = remoteErr(s, err)
		config.RecordState(s.Name, "", err)
		return err
	}
	config.RecordState(s.Name, stateName(info.State), nil)
	if info.Version != "" && info.Version != s.Version {
		s.Version = info.Version
		_ = reg.Save()
	}

	ctrl := config.Control{}
	if s.Remote != nil {
		ctrl = s.Remote.Control
	}
	port := ui.MutedText("Puerto     ") + fmt.Sprint(s.Port)
	switch {
	case info.HTTP > 0:
		port += ui.MutedText(fmt.Sprintf("  (responde HTTP %d)", info.HTTP))
	case info.HTTP == 0:
		port += ui.MutedText("  (no responde)")
	}
	if info.Port != 0 && info.Port != s.Port {
		port += ui.WarnText(fmt.Sprintf("  ojo: su configuración dice %d", info.Port))
	}
	state := stateLabel(info.State)
	if info.Since != "" && info.State != liferay.Stopped {
		state += ui.MutedText(" · desde " + info.Since)
	}
	session := "abierta (se cierra tras " + ssh.Persist() + " sin usarse)"
	if !conn.Connected() {
		session = "sin sesión compartida"
	}
	lines := []string{
		ui.MutedText("Tipo       ") + "remoto (SSH)",
		ui.MutedText("Máquina    ") + s.Host,
		ui.MutedText("Home       ") + info.Home,
		ui.MutedText("Servidor   ") + appServerLabel(info),
		ui.MutedText("Versión    ") + orDash(s.Version),
		port,
		ui.MutedText("Gestión    ") + ctrl.Describe(),
		ui.MutedText("Log        ") + orDash(info.LogFile),
		ui.MutedText("Sesión     ") + session,
		ui.MutedText("Estado     ") + state,
	}
	if info.LogFile == "" {
		lines = append(lines, "", ui.WarnText("No encuentro logs con ")+r.LogPattern())
	}
	ui.Say(stateMood(info.State), fmt.Sprintf("«%s»", s.Name), lines...)
	return nil
}

func stateMood(st liferay.State) ui.Mood {
	switch st {
	case liferay.Running:
		return ui.Happy
	case liferay.Starting:
		return ui.Thinking
	}
	return ui.Sleepy
}

func newConnect() *cobra.Command {
	return &cobra.Command{
		Use:   "connect <nombre>",
		Short: "Abre la sesión SSH con un server remoto (te pide la contraseña una vez)",
		Long: `Abre la sesión SSH con un server remoto y la deja abierta en segundo plano:
los comandos siguientes (logs, check, start...) la reutilizan sin volver a
pedirte la contraseña. Se cierra sola tras un rato sin usarse
(LRAY_SSH_PERSIST, por defecto ` + ssh.DefaultPersist + `) o con lray server disconnect.`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeServers,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, s, err := findServer(args[0])
			if err != nil {
				return err
			}
			if !s.IsRemote() {
				return fmt.Errorf("«%s» es local; no necesita conexión", s.Name)
			}
			_, _, info, err := inspectRemote(cmd.Context(), s, false)
			if err != nil {
				return err
			}
			ui.Say(ui.Happy, fmt.Sprintf("Conectado a «%s»", s.Name),
				ui.MutedText("Estado  ")+stateLabel(info.State),
				ui.MutedText("Sesión  ")+"abierta hasta "+ssh.Persist()+" sin usarse")
			return nil
		},
	}
}

func newDisconnect() *cobra.Command {
	return &cobra.Command{
		Use:               "disconnect <nombre>",
		Short:             "Cierra la sesión SSH abierta con un server remoto",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeServers,
		RunE: func(_ *cobra.Command, args []string) error {
			_, s, err := findServer(args[0])
			if err != nil {
				return err
			}
			if !s.IsRemote() {
				return fmt.Errorf("«%s» es local; no tiene sesión que cerrar", s.Name)
			}
			conn, _ := remoteOf(s)
			if !conn.Connected() {
				ui.Say(ui.Sleepy, fmt.Sprintf("No había ninguna sesión abierta con «%s»", s.Name))
				return nil
			}
			if err := conn.Disconnect(); err != nil {
				return fmt.Errorf("no he podido cerrar la sesión: %w", err)
			}
			ui.Say(ui.Happy, fmt.Sprintf("Sesión con «%s» cerrada", s.Name),
				"La próxima vez que lo uses te volverá a pedir la contraseña.")
			return nil
		},
	}
}
