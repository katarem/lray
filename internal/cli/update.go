package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/katarem/lray/internal/ui"
	"github.com/katarem/lray/internal/update"
)

func newUpdate(version string) *cobra.Command {
	var check, yes bool
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Comprueba si hay una versión nueva de lray y la instala",
		Long: `Busca la última release de lray en GitHub y, si es más nueva que la tuya,
descarga la de tu sistema, verifica su checksum y sustituye el binario.

lray también lo comprueba solo una vez al día y te avisa al terminar un
comando. Desactiva ese aviso con LRAY_NO_UPDATE_CHECK=1.`,
		Example: `  lray update
  lray update --check
  lray update -y`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			var latest string
			noRelease := false
			err := ui.RunTask("Buscando la última versión", "Última versión consultada",
				func(ctx context.Context, _ func(string)) error {
					tag, err := update.Latest(ctx)
					latest, noRelease = tag, errors.Is(err, update.ErrNoRelease)
					if noRelease {
						return nil
					}
					return err
				})
			if err != nil {
				return err
			}
			if noRelease {
				ui.Say(ui.Sleepy, "Todavía no hay ninguna release de lray publicada",
					"Vuelve a probar más adelante.")
				return nil
			}
			update.Remember(latest)

			dev := !update.IsRelease(version)
			if !dev && !update.Newer(version, latest) {
				ui.Say(ui.Happy, "Ya tienes la última versión", "lray "+version)
				return nil
			}

			title := fmt.Sprintf("Hay una versión nueva: %s", latest)
			lines := []string{
				"Tienes " + ui.Code(version) + ".",
				"Novedades: " + ui.Code(update.ReleaseURL(latest)),
			}
			if dev {
				title = fmt.Sprintf("La última release es %s", latest)
				lines[0] = "Tu lray es una build de desarrollo (" + ui.Code(version) + "), no sé si es más nueva."
			}
			if check {
				lines = append(lines, "", "Actualiza con "+ui.Code("lray update"))
				ui.Say(ui.Thinking, title, lines...)
				return nil
			}

			exe, err := update.Executable()
			if err != nil {
				return fmt.Errorf("no encuentro el binario de lray: %w", err)
			}
			if errors.Is(update.CheckManaged(exe), update.ErrManaged) {
				lines = append(lines, "", "Lo instalaste con Homebrew; actualízalo con "+ui.Code("brew upgrade lray"))
				ui.Say(ui.Thinking, title, lines...)
				return nil
			}

			ui.Say(ui.Happy, title, lines...)
			if !yes {
				ok, err := ui.Confirm("¿Actualizo ahora?", "Sustituyo "+ui.ShortPath(exe)+" por "+latest+".", true)
				if err != nil {
					return err
				}
				if !ok {
					ui.Say(ui.Sleepy, "Vale, lo dejamos para otro momento.")
					return nil
				}
			}

			err = ui.RunTask("Actualizando lray a "+latest, "lray actualizado",
				func(ctx context.Context, progress func(string)) error {
					return update.Install(ctx, latest, exe, progress)
				})
			if err != nil {
				return err
			}
			ui.Say(ui.Party, "¡Listo! Ya tienes lray "+latest,
				"Mira las novedades en "+ui.Code(update.ReleaseURL(latest)))
			return nil
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "Solo comprobar, sin instalar nada")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Actualizar sin pedir confirmación")
	return cmd
}

// startUpdateCheck consulta en segundo plano (como mucho una vez al día) si
// hay una versión nueva. La función devuelta, llamada al terminar el
// comando, avisa si la hay; espera poco para no frenar nada.
func startUpdateCheck(version string, args []string) func() {
	noop := func() {}
	if !update.IsRelease(version) || os.Getenv("LRAY_NO_UPDATE_CHECK") != "" || !ui.IsTTY() {
		return noop
	}
	if len(args) > 0 {
		switch args[0] {
		case "update", "completion", "__complete", "__completeNoDesc", "-v", "--version", "-h", "--help":
			return noop
		}
	}
	done := make(chan string, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		done <- update.Cached(ctx)
	}()
	return func() {
		select {
		case latest := <-done:
			if update.Newer(version, latest) {
				ui.Warn(fmt.Sprintf("Hay una versión nueva de lray: %s %s. Actualiza con %s",
					ui.Code(latest), ui.MutedText("(tienes "+version+")"), ui.Code("lray update")))
			}
		case <-time.After(1500 * time.Millisecond):
		}
	}
}
