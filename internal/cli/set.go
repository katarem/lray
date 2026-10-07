package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/katarem/lray/internal/ui"
)

func newSet() *cobra.Command {
	var version string
	cmd := &cobra.Command{
		Use:   "set <nombre>",
		Short: "Cambia a mano datos de un server, como la versión que enseña",
		Long: `Cambia a mano datos de un server ya añadido.

--version fija la versión que enseñan list y check, para cuando lray no la
puede averiguar. Si más adelante la detecta (en el portal desplegado o en sus
logs), la sustituye por la detectada. Con --version "" se borra y lray vuelve
a buscarla.`,
		Example: `  lray server set validacion-turismo --version "DXP 2025.Q1.5 LTS"
  lray server set validacion-turismo --version ""`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeServers,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !cmd.Flags().Changed("version") {
				return errors.New("dime qué cambiar, por ejemplo --version \"DXP 2025.Q1.5 LTS\"")
			}
			reg, s, err := findServer(args[0])
			if err != nil {
				return err
			}
			old := s.Version
			s.Version = version
			if err := reg.Save(); err != nil {
				return err
			}
			ui.Say(ui.Happy, fmt.Sprintf("«%s» actualizado", s.Name),
				ui.MutedText("Versión  ")+orDash(old)+ui.MutedText(" → ")+orDash(s.Version))
			return nil
		},
	}
	cmd.Flags().StringVar(&version, "version", "", "Versión que enseñar (\"\" para que lray la vuelva a buscar)")
	return cmd
}
