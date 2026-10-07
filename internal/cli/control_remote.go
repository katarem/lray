package cli

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/katarem/lray/internal/config"
	"github.com/katarem/lray/internal/liferay"
	"github.com/katarem/lray/internal/ui"
)

// remoteStart lanza el arranque de un server remoto tras confirmarlo.
// started=false si ya estaba encendido o arrancando (o si dijiste que no).
func remoteStart(ctx context.Context, s *config.Server, yes bool) (r *liferay.Remote, started bool, err error) {
	conn, r, info, err := inspectRemote(ctx, s, false)
	if err != nil {
		return nil, false, err
	}
	switch info.State {
	case liferay.Running:
		ui.Say(ui.Happy, fmt.Sprintf("«%s» ya estaba encendido", s.Name), ui.MutedText("en ")+s.Host)
		return r, false, nil
	case liferay.Starting:
		ui.Say(ui.Thinking, fmt.Sprintf("«%s» ya está arrancando", s.Name),
			"Sigue el progreso con "+ui.Code("lray server logs "+s.Name))
		return r, false, nil
	}
	command := controlOf(s).StartCommand()
	if command == "" {
		return nil, false, fmt.Errorf("no sé cómo arrancar «%s»; vuelve a añadirlo indicando --systemd <servicio>, --service <servicio> o --start-cmd", s.Name)
	}
	if !yes {
		ok, err := ui.Confirm(fmt.Sprintf("¿Arranco «%s»?", s.Name),
			"Ejecutaré en "+s.Host+":\n"+command, true)
		if err != nil {
			return nil, false, err
		}
		if !ok {
			return nil, false, ui.ErrInterrupted
		}
	}
	if err := runRemoteControl(ctx, s, conn, command); err != nil {
		return nil, false, fmt.Errorf("no ha arrancado: %w", err)
	}
	config.RecordState(s.Name, stateName(liferay.Starting), nil)
	return r, true, nil
}

// remoteWaitStart espera con spinner a que el remoto responda.
func remoteWaitStart(ctx context.Context, s *config.Server, r *liferay.Remote, timeout time.Duration) error {
	err := ui.RunTask(fmt.Sprintf("Arrancando «%s»", s.Name), fmt.Sprintf("«%s» arrancado", s.Name),
		func(ctx context.Context, update func(string)) error {
			update("Esperando a que Liferay responda…")
			return waitRemote(ctx, s, r, liferay.Running, timeout, update)
		})
	if errors.Is(err, ui.ErrInterrupted) {
		ui.Say(ui.Thinking, "Dejo de esperar, pero sigue arrancando",
			"Sigue el progreso con "+ui.Code("lray server logs "+s.Name))
		return nil
	}
	return err
}

// remoteStop para un server remoto tras confirmarlo y espera a que se apague.
func remoteStop(ctx context.Context, s *config.Server, yes bool, timeout time.Duration) error {
	conn, r, info, err := inspectRemote(ctx, s, false)
	if err != nil {
		return err
	}
	if info.State == liferay.Stopped {
		ui.Say(ui.Sleepy, fmt.Sprintf("«%s» ya estaba apagado", s.Name))
		return nil
	}
	command := controlOf(s).StopCommand()
	if command == "" {
		return fmt.Errorf("no sé cómo parar «%s»; vuelve a añadirlo indicando --systemd <servicio>, --service <servicio> o --stop-cmd", s.Name)
	}
	if !yes {
		ok, err := ui.Confirm(fmt.Sprintf("¿Paro «%s»?", s.Name),
			"Está en "+s.Host+" y puede que más gente lo esté usando. Ejecutaré:\n"+command, false)
		if err != nil {
			return err
		}
		if !ok {
			ui.Say(ui.Happy, fmt.Sprintf("«%s» sigue encendido.", s.Name))
			return nil
		}
	}
	if err := runRemoteControl(ctx, s, conn, command); err != nil {
		return fmt.Errorf("no se ha parado: %w", err)
	}
	return ui.RunTask(fmt.Sprintf("Parando «%s»", s.Name), fmt.Sprintf("«%s» parado", s.Name),
		func(ctx context.Context, update func(string)) error {
			update("Esperando a que se apague…")
			return waitRemote(ctx, s, r, liferay.Stopped, timeout, update)
		})
}

func controlOf(s *config.Server) config.Control {
	if s.Remote == nil {
		return config.Control{}
	}
	return s.Remote.Control
}
