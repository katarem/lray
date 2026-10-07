package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

	"github.com/tu-usuario/lray/internal/config"
	"github.com/tu-usuario/lray/internal/scaffold"
	"github.com/tu-usuario/lray/internal/ui"
)

func newInit() *cobra.Command {
	var dir, javaHome, product, pluginVersion string
	cmd := &cobra.Command{
		Use:   "init <nombre>",
		Short: "Crea un workspace de Liferay nuevo y lo añade a la lista",
		Example: `  lray init tienda
  lray init tienda --dir ~/proyectos --product dxp-2025.q2.12-lts`,
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
			if _, ok := reg.Get(name); ok {
				return fmt.Errorf("ya tienes un server llamado «%s»; elige otro nombre o quítalo con lray rm %s", name, name)
			}
			parent, err := filepath.Abs(dir)
			if err != nil {
				return err
			}
			target := filepath.Join(parent, name)
			if entries, err := os.ReadDir(target); err == nil && len(entries) > 0 {
				return fmt.Errorf("%s ya existe y no está vacía", ui.ShortPath(target))
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
			if err := huh.NewForm(huh.NewGroup(
				huh.NewConfirm().
					Title("¿Descargo el bundle al terminar?").
					Description("Ejecuta initBundle para tener Tomcat + Liferay listos. Tarda unos minutos.").
					Affirmative("Sí, descárgalo").
					Negative("Más tarde").
					Value(&bundle),
			)).WithTheme(ui.Theme()).Run(); err != nil {
				return err
			}

			summary := []string{
				"Nombre    " + name,
				"Carpeta   " + ui.ShortPath(target),
				"Versión   " + product,
			}
			if javaHome != "" {
				summary = append(summary, "Java      "+ui.ShortPath(javaHome))
			}
			if bundle {
				summary = append(summary, "Bundle    se descarga ahora")
			} else {
				summary = append(summary, "Bundle    más tarde (lray start lo ofrecerá)")
			}
			go_ := false
			if err := huh.NewForm(huh.NewGroup(
				huh.NewNote().Title("Esto es lo que voy a hacer").Description(strings.Join(summary, "\n")),
				huh.NewConfirm().Title("¿Empezamos?").Affirmative("Crear workspace").Negative("Cancelar").Value(&go_),
			)).WithTheme(ui.Theme()).Run(); err != nil {
				return err
			}
			if !go_ {
				ui.Say(ui.Sleepy, "Cancelado. No he creado nada.")
				return nil
			}

			if pluginVersion == "" {
				pluginVersion = os.Getenv("LRAY_WORKSPACE_PLUGIN_VERSION")
			}
			err = ui.RunTask("Creando el workspace", "Workspace creado en "+ui.ShortPath(target),
				func(context.Context, func(string)) error {
					return scaffold.Create(target, scaffold.Options{Product: product, PluginVersion: pluginVersion})
				})
			if err != nil {
				return err
			}
			reg.Put(config.Server{Name: name, Path: target, Version: product, JavaHome: javaHome})
			if err := reg.Save(); err != nil {
				return err
			}

			if bundle {
				if err := runInitBundle(target, javaHome); err != nil {
					if errors.Is(err, ui.ErrInterrupted) {
						ui.Say(ui.Thinking, "Workspace creado, descarga cancelada",
							"Retómala cuando quieras con "+ui.Code("lray start "+name))
						return nil
					}
					ui.Say(ui.Worried, "El workspace está creado, pero el bundle no",
						err.Error(),
						"Puedes reintentarlo con "+ui.Code("lray start "+name))
					return nil
				}
			}

			ui.Say(ui.Party, fmt.Sprintf("«%s» está listo", name),
				ui.Code("cd "+ui.ShortPath(target)),
				ui.Code("lray dev "+name)+ui.MutedText("    arranca y muestra los logs"))
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", ".", "Carpeta donde crear el workspace")
	cmd.Flags().StringVar(&product, "product", "", "Clave de versión (p. ej. dxp-2025.q2.12-lts); si no, se pregunta")
	cmd.Flags().StringVar(&javaHome, "java", "", "JAVA_HOME para compilar y arrancar este server")
	cmd.Flags().StringVar(&pluginVersion, "plugin-version", "", "Versión del plugin de workspace (por defecto "+scaffold.DefaultPluginVersion+")")
	return cmd
}

// pickProduct pregunta edición y versión usando el releases.json de Liferay.
func pickProduct() (string, error) {
	var releases []scaffold.Release
	err := ui.RunTask("Consultando las versiones de Liferay", "Versiones disponibles cargadas",
		func(ctx context.Context, _ func(string)) error {
			rs, err := scaffold.LoadReleases(ctx)
			releases = rs
			return err
		})
	if errors.Is(err, ui.ErrInterrupted) {
		return "", err
	}
	if err != nil || len(releases) == 0 {
		ui.Warn("No he podido descargar la lista de versiones; escríbela a mano.")
		key, err := ui.Input("Clave de la versión", "La de liferay.workspace.product", "dxp-2025.q2.12-lts")
		if err == nil && strings.TrimSpace(key) == "" {
			err = errors.New("necesito una versión para crear el workspace")
		}
		return strings.TrimSpace(key), err
	}

	edition := "dxp"
	if err := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().
			Title("¿Qué edición?").
			Options(
				huh.NewOption("Liferay DXP", "dxp"),
				huh.NewOption("Liferay Portal CE", "portal"),
			).
			Value(&edition),
	)).WithTheme(ui.Theme()).Run(); err != nil {
		return "", err
	}

	list := scaffold.ForProduct(releases, edition)
	if len(list) == 0 {
		return "", fmt.Errorf("no hay versiones de %s en la lista", edition)
	}
	opts := make([]huh.Option[string], 0, len(list))
	choice := list[0].ReleaseKey
	recommendedSet := false
	for _, r := range list {
		opts = append(opts, huh.NewOption(r.Label(), r.ReleaseKey))
		if !recommendedSet && r.HasTag("recommended") {
			choice, recommendedSet = r.ReleaseKey, true
		}
	}
	if err := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().
			Title("¿Qué versión?").
			Description("Arriba las más recientes de cada rama. Pulsa / para buscar.").
			Options(opts...).
			Height(14).
			Value(&choice),
	)).WithTheme(ui.Theme()).Run(); err != nil {
		return "", err
	}
	return choice, nil
}
