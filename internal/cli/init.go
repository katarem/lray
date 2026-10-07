package cli

import (
	"fmt"
	"path/filepath"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

	"github.com/katarem/lray/internal/config"
	"github.com/katarem/lray/internal/scaffold"
	"github.com/katarem/lray/internal/ui"
)

func newInit() *cobra.Command {
	var dir, javaHome, product, pluginVersion string
	cmd := &cobra.Command{
		Use:   "init <nombre>",
		Short: "Crea un workspace de Liferay nuevo y lo añade a la lista",
		Example: `  lray server init tienda
  lray server init tienda --dir ~/proyectos --product dxp-2025.q2.12-lts`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			name := args[0]
			if err := config.ValidName(name); err != nil {
				return err
			}
			reg, err := config.Load()
			if err != nil {
				return err
			}
			if err := checkFreeServerName(reg, name); err != nil {
				return err
			}
			parent, err := filepath.Abs(dir)
			if err != nil {
				return err
			}
			target := filepath.Join(parent, name)
			if err := checkEmptyDir(target); err != nil {
				return err
			}
			if javaHome != "" {
				if javaHome, err = checkJava(javaHome); err != nil {
					return err
				}
			}
			if !ui.IsTTY() {
				return ui.ErrNeedsTTY
			}

			ui.Say(ui.Happy, fmt.Sprintf("Vamos a crear «%s»", name),
				"Se creará en "+ui.ShortPath(target)+".",
				"Primero, la versión de Liferay.")

			if product == "" {
				if product, err = pickProduct(); err != nil {
					return err
				}
			}

			bundle := true
			if err := huh.NewForm(huh.NewGroup(bundleQuestion(&bundle))).WithTheme(ui.Theme()).Run(); err != nil {
				return err
			}

			return createWorkspace(reg, workspacePlan{
				Name:          name,
				Target:        target,
				Product:       product,
				JavaHome:      javaHome,
				PluginVersion: pluginVersion,
				Register:      true,
				Bundle:        bundle,
			}, false)
		},
	}
	cmd.Flags().StringVar(&dir, "dir", ".", "Carpeta donde crear el workspace")
	cmd.Flags().StringVar(&product, "product", "", "Clave de versión (p. ej. dxp-2025.q2.12-lts); si no, se pregunta")
	cmd.Flags().StringVar(&javaHome, "java", "", "JAVA_HOME para compilar y arrancar este server")
	cmd.Flags().StringVar(&pluginVersion, "plugin-version", "", "Versión del plugin de workspace (por defecto "+scaffold.DefaultPluginVersion+")")
	return cmd
}
