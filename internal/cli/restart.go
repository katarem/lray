package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/katarem/lray/internal/config"
	"github.com/katarem/lray/internal/liferay"
	"github.com/katarem/lray/internal/ui"
)

func newRestart() *cobra.Command {
	var noWait, yes bool
	cmd := &cobra.Command{
		Use:   "restart <nombre>",
		Short: "Reinicia un server: lo para, espera a que se apague y lo vuelve a arrancar",
		Long: `Para el server, espera a que se apague y lo arranca de nuevo, esperando a
que esté listo. Si estaba apagado, simplemente lo arranca.

En los remotos usa sus comandos de parada y arranque, y pide confirmación una
sola vez (-y para no pedirla).`,
		Example: `  lray server restart mi-liferay
  lray server restart pre -y --no-wait`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeServers,
	}
	stopTimeout := durationFlag(cmd, "stop-timeout", 60*time.Second, "Cuánto esperar a que se pare antes de forzarlo (en remotos, antes de rendirse)")
	timeout := durationFlag(cmd, "timeout", 15*time.Minute, "Cuánto esperar como máximo al arranque")
	cmd.Flags().BoolVar(&noWait, "no-wait", false, "Arrancar y volver a la terminal sin esperar")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Remotos: no pedir confirmación")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		reg, s, err := findServer(args[0])
		if err != nil {
			return err
		}
		if s.IsRemote() {
			stopped, err := remoteRestartStop(cmd.Context(), reg, s, yes, *stopTimeout)
			if err != nil || !stopped {
				return err
			}
		} else {
			_, s, l, err := loadServer(args[0])
			if err != nil {
				return err
			}
			if _, alive := l.PID(); alive {
				if err := stopServer(s, l, *stopTimeout); err != nil {
					return err
				}
			} else {
				ui.Say(ui.Sleepy, fmt.Sprintf("«%s» estaba apagado; lo arranco", s.Name))
			}
		}
		return runStart(cmd.Context(), reg, s, true, noWait, *timeout)
	}
	return cmd
}

// remoteRestartStop es la primera mitad de reiniciar un remoto: comprueba que
// lray sepa pararlo y arrancarlo, lo confirma y lo para. stopped=false si
// dijiste que no.
func remoteRestartStop(ctx context.Context, reg *config.Registry, s *config.Server, yes bool, timeout time.Duration) (stopped bool, err error) {
	conn, r, info, err := inspectRemote(ctx, reg, s, false)
	if err != nil {
		return false, err
	}
	ctl := controlOf(s)
	start, stop := ctl.StartCommand(), ctl.StopCommand()
	if start == "" || (stop == "" && info.State != liferay.Stopped) {
		return false, fmt.Errorf("no sé cómo reiniciar «%s»; vuelve a añadirlo indicando --systemd <servicio>, --service <servicio> o --start-cmd y --stop-cmd", s.Name)
	}
	if info.State == liferay.Stopped {
		ui.Say(ui.Sleepy, fmt.Sprintf("«%s» estaba apagado; lo arranco", s.Name))
		if !yes {
			ok, err := ui.Confirm(fmt.Sprintf("¿Arranco «%s»?", s.Name), "Ejecutaré en "+s.Host+":\n"+start, true)
			if err != nil {
				return false, err
			}
			if !ok {
				return false, ui.ErrInterrupted
			}
		}
		return true, nil
	}
	if !yes {
		ok, err := ui.Confirm(fmt.Sprintf("¿Reinicio «%s»?", s.Name),
			"Está en "+s.Host+" y puede que más gente lo esté usando. Ejecutaré:\n"+stop+"\n"+start, false)
		if err != nil {
			return false, err
		}
		if !ok {
			ui.Say(ui.Happy, fmt.Sprintf("«%s» sigue encendido.", s.Name))
			return false, nil
		}
	}
	if err := runRemoteControl(ctx, s, conn, stop); err != nil {
		return false, fmt.Errorf("no se ha parado: %w", err)
	}
	err = ui.RunTask(fmt.Sprintf("Parando «%s»", s.Name), fmt.Sprintf("«%s» parado", s.Name),
		func(ctx context.Context, update func(string)) error {
			update("Esperando a que se apague…")
			return waitRemote(ctx, s, r, liferay.Stopped, timeout, update)
		})
	return err == nil, err
}
