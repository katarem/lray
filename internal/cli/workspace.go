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

	"github.com/katarem/lray/internal/config"
	"github.com/katarem/lray/internal/liferay"
	"github.com/katarem/lray/internal/scaffold"
	"github.com/katarem/lray/internal/ui"
)

// newWorkspace agrupa los comandos de Liferay Workspaces (lray workspace <comando>).
func newWorkspace() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "workspace",
		Aliases: []string{"ws"},
		Short:   "Crea Liferay Workspaces",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(newWorkspaceCreate())
	return cmd
}

func newWorkspaceCreate() *cobra.Command {
	var dir, javaHome, product, pluginVersion string
	var noRegister, bundle, yes bool
	cmd := &cobra.Command{
		Use:   "create [nombre]",
		Short: "Crea un Liferay Workspace nuevo con un asistente",
		Long: `Crea un Liferay Workspace (Gradle) preguntándote lo necesario: nombre,
carpeta, edición y versión de Liferay, si lo añades a tu lista de servers y
si descargas ya el bundle. Lo que pases como opción ya no se pregunta.`,
		Example: `  lray workspace create
  lray workspace create tienda --dir ~/proyectos
  lray workspace create tienda --product dxp-2025.q2.12-lts --bundle -y`,
		Args: cobra.MaximumNArgs(1),
	}
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		reg, err := config.Load()
		if err != nil {
			return err
		}
		var name string
		if len(args) == 1 {
			name = args[0]
		}
		interactive := ui.IsTTY()
		if !interactive {
			if name == "" || product == "" {
				return errors.New("sin terminal interactiva necesito el nombre y --product")
			}
			if !yes {
				return ui.ErrNeedsTTY
			}
		}
		validName := func(n string) error {
			if err := config.ValidName(n); err != nil {
				return err
			}
			if !noRegister {
				return checkFreeServerName(reg, n)
			}
			return nil
		}
		if name != "" {
			if err := validName(name); err != nil {
				return err
			}
		}
		if javaHome != "" {
			if javaHome, err = checkJava(javaHome); err != nil {
				return err
			}
		}

		if interactive {
			ui.Say(ui.Happy, "Vamos a crear un Liferay Workspace",
				"Te pregunto lo justo; pulsa Enter para aceptar lo que te propongo.")
		}

		// Nombre y carpeta.
		parentIn := dir
		if interactive && (name == "" || !cmd.Flags().Changed("dir")) {
			if !cmd.Flags().Changed("dir") {
				if cwd, err := os.Getwd(); err == nil {
					parentIn = ui.ShortPath(cwd)
				}
			}
			var fields []huh.Field
			if name == "" {
				fields = append(fields, huh.NewInput().
					Title("¿Cómo se llama?").
					Description("Será el nombre de la carpeta y, si lo registras, del server.").
					Placeholder("tienda").
					Validate(validName).
					Value(&name))
			}
			if !cmd.Flags().Changed("dir") {
				fields = append(fields, huh.NewInput().
					Title("¿Dónde lo creo?").
					Description("Se creará una carpeta con su nombre dentro de esta.").
					Validate(func(s string) error {
						if strings.TrimSpace(s) == "" {
							return errors.New("indica una carpeta")
						}
						return checkEmptyDir(filepath.Join(liferay.ExpandHome(strings.TrimSpace(s)), name))
					}).
					Value(&parentIn))
			}
			if err := huh.NewForm(huh.NewGroup(fields...)).WithTheme(ui.Theme()).Run(); err != nil {
				return err
			}
		}
		parent, err := filepath.Abs(liferay.ExpandHome(strings.TrimSpace(parentIn)))
		if err != nil {
			return err
		}
		target := filepath.Join(parent, name)
		if err := checkEmptyDir(target); err != nil {
			return err
		}

		// Versión de Liferay.
		if product == "" {
			if product, err = pickProduct(); err != nil {
				return err
			}
		}

		// Registro y bundle.
		register := !noRegister
		if interactive {
			var fields []huh.Field
			if !cmd.Flags().Changed("no-register") {
				fields = append(fields, huh.NewConfirm().
					Title("¿Lo añado a tu lista de servers?").
					Description("Así podrás arrancarlo, ver sus logs y desplegar con lray server …").
					Affirmative("Sí, añádelo").
					Negative("No").
					Value(&register))
			}
			if !cmd.Flags().Changed("bundle") {
				bundle = true
				fields = append(fields, bundleQuestion(&bundle))
			}
			if len(fields) > 0 {
				if err := huh.NewForm(huh.NewGroup(fields...)).WithTheme(ui.Theme()).Run(); err != nil {
					return err
				}
			}
		}
		if register {
			if err := checkFreeServerName(reg, name); err != nil {
				return err
			}
		}

		return createWorkspace(reg, workspacePlan{
			Name:          name,
			Target:        target,
			Product:       product,
			JavaHome:      javaHome,
			PluginVersion: pluginVersion,
			Register:      register,
			Bundle:        bundle,
		}, yes)
	}
	cmd.Flags().StringVar(&dir, "dir", ".", "Carpeta donde crear el workspace")
	cmd.Flags().StringVar(&product, "product", "", "Clave de versión (p. ej. dxp-2025.q2.12-lts); si no, se pregunta")
	cmd.Flags().StringVar(&javaHome, "java", "", "JAVA_HOME para compilar y arrancar este workspace")
	cmd.Flags().StringVar(&pluginVersion, "plugin-version", "", "Versión del plugin de workspace (por defecto "+scaffold.DefaultPluginVersion+")")
	cmd.Flags().BoolVar(&noRegister, "no-register", false, "No añadirlo a la lista de servers")
	cmd.Flags().BoolVar(&bundle, "bundle", false, "Descargar el bundle al terminar (initBundle)")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "No pedir confirmación")
	return cmd
}

// workspacePlan es todo lo decidido antes de crear un workspace.
type workspacePlan struct {
	Name, Target, Product, JavaHome, PluginVersion string
	Register, Bundle                               bool
}

// createWorkspace enseña el resumen, crea el workspace, lo registra si toca
// y descarga el bundle si se ha pedido.
func createWorkspace(reg *config.Registry, p workspacePlan, yes bool) error {
	summary := []string{
		"Nombre    " + p.Name,
		"Carpeta   " + ui.ShortPath(p.Target),
		"Versión   " + p.Product,
	}
	if p.JavaHome != "" {
		summary = append(summary, "Java      "+ui.ShortPath(p.JavaHome))
	}
	if p.Register {
		summary = append(summary, "Server    se añade a tu lista como «"+p.Name+"»")
	} else {
		summary = append(summary, "Server    no se añade a tu lista")
	}
	switch {
	case p.Bundle:
		summary = append(summary, "Bundle    se descarga ahora")
	case p.Register:
		summary = append(summary, "Bundle    más tarde (lray server start lo ofrecerá)")
	default:
		summary = append(summary, "Bundle    más tarde (./gradlew initBundle)")
	}
	if !yes {
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
	}

	pluginVersion := p.PluginVersion
	if pluginVersion == "" {
		pluginVersion = os.Getenv("LRAY_WORKSPACE_PLUGIN_VERSION")
	}
	err := ui.RunTask("Creando el workspace", "Workspace creado en "+ui.ShortPath(p.Target),
		func(context.Context, func(string)) error {
			return scaffold.Create(p.Target, scaffold.Options{Product: p.Product, PluginVersion: pluginVersion})
		})
	if err != nil {
		return err
	}
	if p.Register {
		reg.Put(config.Server{Name: p.Name, Path: p.Target, Version: p.Product, JavaHome: p.JavaHome})
		if err := reg.Save(); err != nil {
			return err
		}
	}

	retry := ui.Code("cd "+ui.ShortPath(p.Target)) + " y " + ui.Code("./gradlew initBundle")
	if p.Register {
		retry = ui.Code("lray server start " + p.Name)
	}
	if p.Bundle {
		if err := runInitBundle(p.Target, p.JavaHome); err != nil {
			if errors.Is(err, ui.ErrInterrupted) {
				ui.Say(ui.Thinking, "Workspace creado, descarga cancelada", "Retómala cuando quieras con "+retry)
				return nil
			}
			ui.Say(ui.Worried, "El workspace está creado, pero el bundle no", err.Error(), "Puedes reintentarlo con "+retry)
			return nil
		}
	}

	next := []string{ui.Code("cd " + ui.ShortPath(p.Target))}
	if p.Register {
		next = append(next, ui.Code("lray server dev "+p.Name)+ui.MutedText("    arranca y muestra los logs"))
	}
	next = append(next, ui.Code("lray module create")+ui.MutedText("    crea tu primer módulo"))
	if !p.Register {
		next = append(next, ui.Code("lray server add "+p.Name+" "+ui.ShortPath(p.Target))+ui.MutedText("    para registrarlo después"))
	}
	ui.Say(ui.Party, fmt.Sprintf("«%s» está listo", p.Name), next...)
	return nil
}

// bundleQuestion pregunta si descargar el bundle al terminar.
func bundleQuestion(v *bool) huh.Field {
	return huh.NewConfirm().
		Title("¿Descargo el bundle al terminar?").
		Description("Ejecuta initBundle para tener Tomcat + Liferay listos. Tarda unos minutos.").
		Affirmative("Sí, descárgalo").
		Negative("Más tarde").
		Value(v)
}

// checkFreeServerName falla si ya hay un server registrado con ese nombre.
func checkFreeServerName(reg *config.Registry, name string) error {
	if _, ok := reg.Get(name); ok {
		return fmt.Errorf("ya tienes un server llamado «%s»; elige otro nombre o quítalo con lray server rm %s", name, name)
	}
	return nil
}

// checkEmptyDir falla si la carpeta existe y tiene algo dentro.
func checkEmptyDir(dir string) error {
	if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 {
		return fmt.Errorf("%s ya existe y no está vacía", ui.ShortPath(dir))
	}
	return nil
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
