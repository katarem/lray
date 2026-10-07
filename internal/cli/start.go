package cli

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/katarem/lray/internal/config"
	"github.com/katarem/lray/internal/liferay"
	"github.com/katarem/lray/internal/ui"
)

func newStart() *cobra.Command {
	var noWait bool
	cmd := &cobra.Command{
		Use:               "start <nombre>",
		Short:             "Arranca un server y espera a que esté listo",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeServers,
	}
	timeout := durationFlag(cmd, "timeout", 15*time.Minute, "Cuánto esperar como máximo al arranque")
	cmd.Flags().BoolVar(&noWait, "no-wait", false, "Arrancar y volver a la terminal sin esperar")
	cmd.RunE = func(_ *cobra.Command, args []string) error {
		reg, s, l, err := loadServer(args[0])
		if err != nil {
			return err
		}
		res, err := startServer(reg, s, l)
		if err != nil || !res.started {
			return err
		}
		if noWait {
			ui.Say(ui.Happy, fmt.Sprintf("«%s» está arrancando en segundo plano", s.Name),
				"Míralo con "+ui.Code("lray logs "+s.Name))
			return nil
		}
		err = ui.RunTask(fmt.Sprintf("Arrancando «%s»", s.Name), fmt.Sprintf("«%s» arrancado", s.Name),
			func(ctx context.Context, update func(string)) error {
				return liferay.WaitReady(ctx, l, res.offset, *timeout, update)
			})
		if errors.Is(err, ui.ErrInterrupted) {
			ui.Say(ui.Thinking, "Dejo de esperar, pero sigue arrancando",
				"Sigue el progreso con "+ui.Code("lray logs "+s.Name))
			return nil
		}
		if err != nil {
			return err
		}
		ui.Say(ui.Party, fmt.Sprintf("«%s» está en marcha", s.Name),
			ui.Code(url(l.Port())),
			"",
			ui.MutedText("Logs   ")+ui.Code("lray logs "+s.Name),
			ui.MutedText("Parar  ")+ui.Code("lray stop "+s.Name))
		return nil
	}
	return cmd
}

type startResult struct {
	started bool  // false si ya estaba encendido o arrancando
	offset  int64 // tamaño de catalina.out antes de arrancar
}

// startServer hace las comprobaciones previas (bundle, puerto) y lanza Tomcat.
func startServer(reg *config.Registry, s *config.Server, l *liferay.Layout) (startResult, error) {
	switch l.State() {
	case liferay.Running:
		ui.Say(ui.Happy, fmt.Sprintf("«%s» ya estaba encendido", s.Name), ui.Code(url(l.Port())))
		return startResult{}, nil
	case liferay.Starting:
		ui.Say(ui.Thinking, fmt.Sprintf("«%s» ya está arrancando", s.Name),
			"Sigue el progreso con "+ui.Code("lray logs "+s.Name))
		return startResult{}, nil
	}

	if !l.HasBundle() {
		if l.Workspace == "" {
			return startResult{}, liferay.ErrNoBundle
		}
		ok, err := ui.Confirm("Este workspace aún no tiene bundle",
			"¿Lo descargo ahora con initBundle? Puede tardar unos minutos.", true)
		if err != nil {
			return startResult{}, err
		}
		if !ok {
			return startResult{}, errors.New("sin bundle no puedo arrancarlo")
		}
		if err := runInitBundle(l.Workspace, s.JavaHome); err != nil {
			return startResult{}, err
		}
		nl, err := liferay.Resolve(s.Path)
		if err != nil {
			return startResult{}, err
		}
		*l = *nl
		if !l.HasBundle() {
			return startResult{}, errors.New("initBundle terminó pero no encuentro la carpeta tomcat del bundle")
		}
	}

	if port := l.Port(); liferay.PortInUse(port) {
		desc := fmt.Sprintf("El puerto %d ya está en uso", port)
		if who := whoUsesPort(reg, s.Name, port); who != "" {
			desc += fmt.Sprintf(" por «%s» (páralo con lray stop %s)", who, who)
		}
		ok, err := ui.Confirm("Puerto ocupado", desc+". Si arranco igualmente, Tomcat fallará. ¿Sigo?", false)
		if err != nil {
			return startResult{}, err
		}
		if !ok {
			return startResult{}, ui.ErrInterrupted
		}
	}

	offset := liferay.FileSize(l.CatalinaOut())
	if err := liferay.Start(l, s.JavaHome); err != nil {
		return startResult{}, err
	}
	return startResult{started: true, offset: offset}, nil
}

func newStop() *cobra.Command {
	cmd := &cobra.Command{
		Use:               "stop <nombre>",
		Short:             "Para un server",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeServers,
	}
	timeout := durationFlag(cmd, "timeout", 60*time.Second, "Cuánto esperar antes de forzar la parada")
	cmd.RunE = func(_ *cobra.Command, args []string) error {
		_, s, l, err := loadServer(args[0])
		if err != nil {
			return err
		}
		return stopServer(s, l, *timeout)
	}
	return cmd
}

func stopServer(s *config.Server, l *liferay.Layout, timeout time.Duration) error {
	if _, alive := l.PID(); !alive {
		ui.Say(ui.Sleepy, fmt.Sprintf("«%s» ya estaba apagado", s.Name))
		return nil
	}
	var forced bool
	err := ui.RunTask(fmt.Sprintf("Parando «%s»", s.Name), fmt.Sprintf("«%s» parado", s.Name),
		func(_ context.Context, update func(string)) error {
			update("Pidiendo a Tomcat que se apague ordenadamente…")
			f, err := liferay.Stop(l, s.JavaHome, timeout)
			forced = f
			return err
		})
	if err != nil {
		return err
	}
	if forced {
		ui.Warn(fmt.Sprintf("No respondió en %s y he tenido que forzar la parada.", timeout))
	}
	return nil
}
