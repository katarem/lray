package cli

import "github.com/spf13/cobra"

// newServer agrupa los comandos que gestionan servers Liferay
// (lray server <comando>), dejando la raíz libre para otras áreas.
func newServer() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "server",
		Short: "Crea, arranca, despliega y sigue los logs de tus servers Liferay, locales o por SSH",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(
		newInit(), newAdd(), newRm(), newList(), newCheck(), newSet(), newEdit(),
		newStart(), newStop(), newRestart(), newDev(), newLogs(), newDeploy(), newWatch(),
		newOpen(), newShell(), newConnect(), newDisconnect(),
	)
	return cmd
}
