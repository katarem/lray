package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/katarem/lray/internal/config"
	"github.com/katarem/lray/internal/liferay"
	"github.com/katarem/lray/internal/ui"
)

func newRm() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:               "rm <nombre>",
		Aliases:           []string{"remove"},
		Short:             "Quita un server de la lista (sus archivos no se tocan)",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeServers,
		RunE: func(_ *cobra.Command, args []string) error {
			name := args[0]
			reg, err := config.Load()
			if err != nil {
				return err
			}
			s, ok := reg.Get(name)
			if !ok {
				return fmt.Errorf("no tengo ningún server llamado «%s»", name)
			}
			running := false
			if !s.IsRemote() {
				if l, err := liferay.Resolve(s.Path); err == nil {
					_, running = l.PID()
				}
			}
			if !yes {
				desc := "Solo lo quito de la lista: " + ui.ShortPath(s.Path) + " se queda donde está."
				if s.IsRemote() {
					desc = "Solo lo quito de la lista: en " + s.Host + " no toco nada."
				}
				if running {
					desc += "\nOjo: está encendido y seguirá encendido."
				}
				sure, err := ui.Confirm(fmt.Sprintf("¿Seguro que quieres quitar «%s»?", name), desc, false)
				if err != nil {
					return err
				}
				if !sure {
					ui.Say(ui.Happy, fmt.Sprintf("«%s» se queda en la lista.", name))
					return nil
				}
			}
			reg.Remove(name)
			if err := reg.Save(); err != nil {
				return err
			}
			config.ForgetState(name)
			again := ui.ShortPath(s.Path)
			if s.IsRemote() {
				conn, _ := remoteOf(s)
				_ = conn.Disconnect()
				again = s.Location()
			}
			ui.Say(ui.Thinking, fmt.Sprintf("«%s» ya no está en la lista", name),
				"Si lo echas de menos: "+ui.Code(fmt.Sprintf("lray server add %s %s", name, again)))
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "No pedir confirmación")
	return cmd
}
