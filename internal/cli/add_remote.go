package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

	"github.com/katarem/lray/internal/config"
	"github.com/katarem/lray/internal/liferay"
	"github.com/katarem/lray/internal/ui"
)

// remoteFlags son las opciones de lray server add que solo aplican a remotos.
type remoteFlags struct {
	sshPort                     int
	log                         string
	systemd, service            string
	startCmd, stopCmd, statusCm string
	sudo                        bool
}

func (f *remoteFlags) register(cmd *cobra.Command) {
	fl := cmd.Flags()
	fl.IntVar(&f.sshPort, "ssh-port", 0, "Remotos: puerto SSH si no es el 22 ni está en ~/.ssh/config")
	fl.StringVar(&f.log, "log", "", "Remotos: patrón de logs, relativo al liferay home (por defecto "+liferay.DefaultRemoteLog+")")
	fl.StringVar(&f.systemd, "systemd", "", "Remotos: se arranca y para con systemctl; nombre del servicio")
	fl.StringVar(&f.service, "service", "", "Remotos: se arranca y para con service (init.d); nombre del servicio")
	fl.StringVar(&f.startCmd, "start-cmd", "", "Remotos: comando propio para arrancarlo")
	fl.StringVar(&f.stopCmd, "stop-cmd", "", "Remotos: comando propio para pararlo")
	fl.StringVar(&f.statusCm, "status-cmd", "", "Remotos: comando que termina bien (código 0) si está encendido")
	fl.BoolVar(&f.sudo, "sudo", true, "Remotos: arrancar y parar con sudo (--sudo=false para no usarlo)")
}

// control monta la gestión a partir de las opciones; ok=false si no se dio ninguna.
func (f *remoteFlags) control(cmd *cobra.Command) (config.Control, bool, error) {
	custom := f.startCmd != "" || f.stopCmd != "" || f.statusCm != ""
	n := 0
	for _, set := range []bool{f.systemd != "", f.service != "", custom} {
		if set {
			n++
		}
	}
	switch {
	case n > 1:
		return config.Control{}, false, errors.New("elige una sola forma de gestionarlo: --systemd, --service o los --*-cmd")
	case f.systemd != "":
		return config.Control{Kind: config.ControlSystemd, Service: f.systemd, Sudo: f.sudo}, true, nil
	case f.service != "":
		return config.Control{Kind: config.ControlService, Service: f.service, Sudo: f.sudo}, true, nil
	case custom:
		sudo := f.sudo && cmd.Flags().Changed("sudo") // en comandos propios, sudo solo si lo pides
		return config.Control{Kind: config.ControlCustom, Start: f.startCmd, Stop: f.stopCmd, Status: f.statusCm, Sudo: sudo}, true, nil
	}
	return config.Control{}, false, nil
}

type remoteAdd struct {
	name, dest, path, version string
	port                      int
	portSet, yes              bool
	flags                     *remoteFlags
}

func addRemote(cmd *cobra.Command, a remoteAdd) error {
	ctx := cmd.Context()
	reg, err := config.Load()
	if err != nil {
		return err
	}
	if old, ok := reg.Get(a.name); ok && !a.yes {
		replace, err := ui.Confirm(
			fmt.Sprintf("Ya hay un server llamado «%s»", a.name),
			"Ahora apunta a "+old.Location()+". ¿Lo reemplazo?", false)
		if err != nil {
			return err
		}
		if !replace {
			ui.Say(ui.Sleepy, "Vale, lo dejo como estaba.")
			return nil
		}
	}
	ctrl, given, err := a.flags.control(cmd)
	if err != nil {
		return err
	}

	s := &config.Server{
		Name: a.name, Path: a.path, Version: a.version, Port: a.port, Host: a.dest,
		Remote: &config.Remote{SSHPort: a.flags.sshPort, Log: a.flags.log, Control: ctrl},
	}
	conn, r := remoteOf(s)
	if err := connect(ctx, s, conn); err != nil {
		return err
	}
	var info *liferay.RemoteInfo
	err = ui.RunTask("Mirando qué hay en "+s.Location(), "Liferay encontrado",
		func(ctx context.Context, _ func(string)) error {
			var err error
			info, err = r.Inspect(ctx, a.version == "")
			return err
		})
	if err != nil {
		return remoteErr(s, err)
	}
	s.Path = info.Home

	if !given && !a.yes && ui.IsTTY() {
		if ctrl, err = askControl(ctx, r); err != nil {
			return err
		}
		s.Remote.Control = ctrl
	}
	if s.Version == "" {
		s.Version = info.Version
	}
	if !a.portSet {
		s.Port = info.Port
		if s.Port == 0 {
			s.Port = liferay.DefaultPort
		}
	}

	// Ya con el puerto y la forma de consultarlo, el estado real.
	_, r = remoteOf(s)
	if fresh, err := r.Inspect(ctx, false); err == nil {
		info.State, info.HTTP, info.LogFile = fresh.State, fresh.HTTP, fresh.LogFile
		if fresh.Version != "" && a.version == "" {
			s.Version = fresh.Version // la de la cabecera del portal en marcha
		}
		if p := fresh.AnsweredPort(); p > 0 && !a.portSet {
			s.Port = p
		}
	}

	reg.Put(*s)
	if err := reg.Save(); err != nil {
		return err
	}
	config.RecordState(s.Name, stateName(info.State), nil)

	lines := []string{
		ui.MutedText("Máquina   ") + s.Host,
		ui.MutedText("Ruta      ") + s.Path,
		ui.MutedText("Servidor  ") + appServerLabel(info),
		ui.MutedText("Versión   ") + orDash(s.Version),
		ui.MutedText("Puerto    ") + fmt.Sprint(s.Port),
		ui.MutedText("Gestión   ") + s.Remote.Control.Describe(),
		ui.MutedText("Log       ") + orDash(info.LogFile),
		ui.MutedText("Estado    ") + stateLabel(info.State),
		"",
		ui.MutedText("Detalles  ") + ui.Code("lray server check "+s.Name),
		ui.MutedText("Logs      ") + ui.Code("lray server logs "+s.Name),
	}
	if info.LogFile == "" {
		lines = append(lines, "", ui.WarnText("No encuentro logs con ")+r.LogPattern()+ui.MutedText("; cámbialo con --log al añadirlo."))
	}
	ui.Say(ui.Party, fmt.Sprintf("«%s» añadido", s.Name), lines...)
	return nil
}

// askControl pregunta cómo se arranca y se para el server remoto.
func askControl(ctx context.Context, r *liferay.Remote) (config.Control, error) {
	services := liferay.FindServices(ctx, r.Exec)
	c := config.Control{Kind: config.ControlSystemd, Sudo: true}
	if len(services) > 0 {
		c.Service = services[0]
	}
	desc := "lray lo usará en start, stop y dev, y para saber si está encendido."
	if len(services) > 0 {
		desc += "\nServicios que he encontrado: " + strings.Join(services, ", ")
	}
	err := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().
			Title("¿Cómo se arranca y se para este Liferay?").
			Description(desc).
			Options(
				huh.NewOption("systemd (systemctl start|stop <servicio>)", config.ControlSystemd),
				huh.NewOption("service (service <servicio> start|stop)", config.ControlService),
				huh.NewOption("Con mis propios comandos", config.ControlCustom),
				huh.NewOption("No lo gestiono desde lray (solo estado y logs)", config.ControlNone),
			).
			Value(&c.Kind),
	)).WithTheme(ui.Theme()).Run()
	if err != nil {
		return c, err
	}

	var fields []huh.Field
	switch c.Kind {
	case config.ControlSystemd, config.ControlService:
		fields = append(fields, huh.NewInput().Title("Nombre del servicio").Placeholder("jboss").
			Value(&c.Service).Validate(required("dime el nombre del servicio")))
	case config.ControlCustom:
		c.Sudo = false
		fields = append(fields,
			huh.NewInput().Title("Comando para arrancarlo").Placeholder("/opt/liferay/jboss/bin/arrancar.sh").Value(&c.Start),
			huh.NewInput().Title("Comando para pararlo").Value(&c.Stop),
			huh.NewInput().Title("Comando que termina bien si está encendido").
				Description("Opcional. Si lo dejas vacío, busco el proceso java del servidor.").Value(&c.Status),
		)
	default:
		return config.Control{}, nil
	}
	fields = append(fields, huh.NewConfirm().
		Title("¿Hace falta sudo para arrancarlo y pararlo?").
		Description("Si te pide contraseña, te la preguntará la propia máquina al arrancar o parar.").
		Affirmative("Sí").Negative("No").Value(&c.Sudo))
	err = huh.NewForm(huh.NewGroup(fields...)).WithTheme(ui.Theme()).Run()
	return c, err
}

func appServerLabel(info *liferay.RemoteInfo) string {
	switch info.AppServer {
	case "jboss":
		return "JBoss/WildFly · " + info.AppDir
	case "tomcat":
		return "Tomcat · " + info.AppDir
	}
	return "—"
}
