package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

	"github.com/katarem/lray/internal/config"
	"github.com/katarem/lray/internal/liferay"
	"github.com/katarem/lray/internal/scaffold"
	"github.com/katarem/lray/internal/ui"
)

// newModule agrupa los comandos de módulos (lray module <comando>).
func newModule() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "module",
		Aliases: []string{"mod"},
		Short:   "Crea módulos de Liferay dentro de tu workspace",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(newModuleCreate())
	return cmd
}

func newModuleCreate() *cobra.Command {
	var o scaffold.ModuleOptions
	var dir, server string
	var yes bool
	cmd := &cobra.Command{
		Use:   "create [nombre]",
		Short: "Crea un módulo (portlet, servicio, REST...) con un asistente, como blade create",
		Long: `Crea un módulo con las mismas plantillas que blade create. Te pregunta el
tipo de módulo, el nombre, el paquete y el prefijo de las clases (y lo que
necesite cada tipo); lo que pases como opción ya no se pregunta.

El módulo se crea en la carpeta de módulos del workspace en el que estés
(liferay.workspace.modules.dir, "modules" por defecto). Con --server usas
el workspace de un server de tu lista, y con --dir eliges otra carpeta.

Tipos: ` + strings.Join(scaffold.ModuleTypeIDs(), ", ") + `.`,
		Example: `  lray module create
  lray module create carrito --type mvc-portlet
  lray module create carrito-api -t api -p com.tienda.carrito -c Carrito
  lray module create usuarios-wrapper -t service-wrapper --service com.liferay.portal.kernel.service.UserLocalServiceWrapper
  lray module create login-jsp -t fragment --host-bundle com.liferay.login.web --host-version 6.0.0`,
		Args: cobra.MaximumNArgs(1),
	}
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) == 1 {
			o.Name = args[0]
		}
		interactive := ui.IsTTY()
		if !interactive && !yes {
			return ui.ErrNeedsTTY
		}

		ws, serverName, err := moduleWorkspace(server, interactive)
		if err != nil {
			return err
		}
		o.Workspace = ws
		o.Product = liferay.DetectVersion(&liferay.Layout{Workspace: ws})

		base := dir
		if base == "" {
			base = modulesDir(ws)
		} else if base, err = filepath.Abs(liferay.ExpandHome(base)); err != nil {
			return err
		}
		if o.Author == "" {
			o.Author = currentUser()
		}

		// Tipo de módulo.
		if o.Type == "" {
			if !interactive {
				return errors.New("indica el tipo de módulo con --type (" + strings.Join(scaffold.ModuleTypeIDs(), ", ") + ")")
			}
			ui.Say(ui.Happy, "Vamos a crear un módulo",
				"Irá en "+ui.ShortPath(base)+".",
				"Pulsa Enter para aceptar lo que te propongo.")
			if o.Type, err = pickModuleType(); err != nil {
				return err
			}
		}
		t, ok := scaffold.FindModuleType(o.Type)
		if !ok {
			return fmt.Errorf("no conozco el tipo de módulo %q; los disponibles son: %s", o.Type, strings.Join(scaffold.ModuleTypeIDs(), ", "))
		}

		validName := func(n string) error {
			if err := scaffold.ValidModuleName(n); err != nil {
				return err
			}
			return checkEmptyDir(filepath.Join(base, n))
		}

		// Nombre.
		if o.Name == "" {
			if !interactive {
				return errors.New("indica el nombre del módulo")
			}
			if err := huh.NewForm(huh.NewGroup(
				huh.NewInput().
					Title("¿Cómo se llama el módulo?").
					Description("Será su carpeta y su Bundle-Name. Por ejemplo, carrito-web.").
					Validate(validName).
					Value(&o.Name),
			)).WithTheme(ui.Theme()).Run(); err != nil {
				return err
			}
		} else if err := validName(o.Name); err != nil {
			return err
		}

		// Paquete, prefijo de clases y lo que pida cada tipo.
		askPackage := o.Package == ""
		askClass := o.ClassName == "" && !t.NoClass
		askService := o.ServiceClass == "" && t.NeedsClass != ""
		askHost := t.IsFragment && o.HostBundle == ""
		askHostVersion := t.IsFragment && o.HostVersion == ""
		if askPackage {
			o.Package = scaffold.DefaultPackage(o.Name)
		}
		if askClass {
			o.ClassName = scaffold.DefaultClassName(t, o.Name)
		}
		if interactive {
			var fields []huh.Field
			if askPackage {
				title := "Paquete Java"
				if t.IsFragment {
					title = "Bundle-SymbolicName del fragmento"
				}
				fields = append(fields, huh.NewInput().
					Title(title).
					Description("Te propongo uno a partir del nombre.").
					Validate(scaffold.ValidPackage).
					Value(&o.Package))
			}
			if askClass {
				fields = append(fields, huh.NewInput().
					Title("Prefijo de las clases").
					Description(classPrefixHint(t)).
					Validate(scaffold.ValidClassName).
					Value(&o.ClassName))
			}
			if askService {
				fields = append(fields, serviceClassInput(t, &o.ServiceClass))
			}
			if askHost {
				fields = append(fields, huh.NewInput().
					Title("Bundle-SymbolicName del módulo anfitrión").
					Description("El módulo cuyos JSP quieres sobrescribir.").
					Placeholder("com.liferay.login.web").
					Validate(scaffold.ValidPackage).
					Value(&o.HostBundle))
			}
			if askHostVersion {
				fields = append(fields, huh.NewInput().
					Title("Versión del módulo anfitrión").
					Description("La que tiene en tu Liferay (Bundle-Version). Búscala en la consola de Gogo con lb.").
					Placeholder("6.0.0").
					Validate(required("indica la versión del anfitrión")).
					Value(&o.HostVersion))
			}
			if len(fields) > 0 {
				if err := huh.NewForm(huh.NewGroup(fields...)).WithTheme(ui.Theme()).Run(); err != nil {
					return err
				}
			}
		}
		if t.NeedsClass != "" && o.ServiceClass == "" {
			return errors.New("este tipo necesita la clase del servicio: usa --service")
		}
		if t.IsFragment && (o.HostBundle == "" || o.HostVersion == "") {
			return errors.New("un fragmento necesita --host-bundle y --host-version")
		}

		// Resumen.
		edition := "Portal CE"
		if scaffold.Edition(o.Product) == "dxp" {
			edition = "DXP"
		}
		version := o.Product
		if version == "" {
			version = ui.WarnText("desconocida") + ui.MutedText(" (no hay liferay.workspace.product; genero para la 7.4)")
		}
		summary := []string{
			"Tipo      " + t.Title,
			"Nombre    " + o.Name,
			"Carpeta   " + ui.ShortPath(filepath.Join(base, o.Name)),
			"Paquete   " + o.Package,
		}
		if !t.NoClass {
			summary = append(summary, "Clases    "+o.ClassName+"…")
		}
		if t.NeedsClass != "" {
			summary = append(summary, "Servicio  "+o.ServiceClass)
		}
		if t.IsFragment {
			summary = append(summary, "Anfitrión "+o.HostBundle+" "+o.HostVersion)
		}
		summary = append(summary, "Liferay   "+version+ui.MutedText("  ("+edition+")"))
		if scaffold.IsJakarta(o.Product) {
			summary = append(summary, ui.MutedText("          Jakarta EE: javax pasa a jakarta"))
		}
		if !yes {
			go_ := true
			if err := huh.NewForm(huh.NewGroup(
				huh.NewNote().Title("Esto es lo que voy a crear").Description(strings.Join(summary, "\n")),
				huh.NewConfirm().Title("¿Lo creo?").Affirmative("Crear módulo").Negative("Cancelar").Value(&go_),
			)).WithTheme(ui.Theme()).Run(); err != nil {
				return err
			}
			if !go_ {
				ui.Say(ui.Sleepy, "Cancelado. No he creado nada.")
				return nil
			}
		}

		var files []string
		target := filepath.Join(base, o.Name)
		err = ui.RunTask("Creando el módulo", "Módulo creado en "+ui.ShortPath(target),
			func(context.Context, func(string)) error {
				files, err = scaffold.CreateModule(base, o)
				return err
			})
		if err != nil {
			return err
		}

		body := moduleFilesSummary(files)
		body = append(body, "")
		body = append(body, moduleNextSteps(t, o, ws, target, serverName)...)
		ui.Say(ui.Party, fmt.Sprintf("«%s» está listo", o.Name), body...)
		return nil
	}

	cmd.Flags().StringVarP(&o.Type, "type", "t", "", "Tipo de módulo: "+strings.Join(scaffold.ModuleTypeIDs(), ", "))
	cmd.Flags().StringVarP(&o.Package, "package", "p", "", "Paquete Java (por defecto, a partir del nombre)")
	cmd.Flags().StringVarP(&o.ClassName, "class-name", "c", "", "Prefijo de las clases (por defecto, a partir del nombre)")
	cmd.Flags().StringVar(&o.ServiceClass, "service", "", "service: interfaz a implementar; service-wrapper: clase *Wrapper a extender")
	cmd.Flags().StringVar(&o.HostBundle, "host-bundle", "", "fragment: Bundle-SymbolicName del módulo anfitrión")
	cmd.Flags().StringVar(&o.HostVersion, "host-version", "", "fragment: versión del módulo anfitrión")
	cmd.Flags().StringVar(&o.Author, "author", "", "Autor para los @author (por defecto, tu usuario)")
	cmd.Flags().StringVarP(&dir, "dir", "d", "", "Carpeta donde crearlo (por defecto, la de módulos del workspace)")
	cmd.Flags().StringVar(&server, "server", "", "Usar el workspace de este server de tu lista")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "No pedir confirmación")
	_ = cmd.RegisterFlagCompletionFunc("type", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		out := make([]string, len(scaffold.ModuleTypes))
		for i, t := range scaffold.ModuleTypes {
			out[i] = t.ID + "\t" + t.Title
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	})
	_ = cmd.RegisterFlagCompletionFunc("server", func(c *cobra.Command, _ []string, s string) ([]string, cobra.ShellCompDirective) {
		return completeServers(c, nil, s)
	})
	return cmd
}

// moduleWorkspace decide en qué workspace crear el módulo: el del server
// indicado, el de la carpeta actual o, si no hay ninguno, uno de la lista.
// Devuelve también el nombre del server de ese workspace, si lo hay.
func moduleWorkspace(server string, interactive bool) (string, string, error) {
	if server != "" {
		_, s, l, err := loadServer(server)
		if err != nil {
			return "", "", err
		}
		if l.Workspace == "" {
			return "", "", fmt.Errorf("«%s» es un bundle suelto, no un workspace; no puedo crear módulos en él", s.Name)
		}
		return l.Workspace, s.Name, nil
	}

	reg, err := config.Load()
	if err != nil {
		return "", "", err
	}
	type wsServer struct{ name, ws string }
	var known []wsServer
	for _, s := range reg.Servers {
		if l, err := liferay.Resolve(s.Path); err == nil && l.Workspace != "" {
			known = append(known, wsServer{s.Name, l.Workspace})
		}
	}

	if cwd, err := os.Getwd(); err == nil {
		if ws, ok := liferay.FindWorkspace(cwd); ok {
			for _, k := range known {
				if k.ws == ws {
					return ws, k.name, nil
				}
			}
			return ws, "", nil
		}
	}

	noWS := errors.New("aquí no hay ningún workspace de Liferay; entra en uno, usa --server <nombre> o crea uno con lray workspace create")
	if !interactive || len(known) == 0 {
		return "", "", noWS
	}
	opts := make([]huh.Option[int], len(known))
	for i, k := range known {
		opts[i] = huh.NewOption(k.name+"  "+ui.MutedText(ui.ShortPath(k.ws)), i)
	}
	choice := 0
	if err := huh.NewForm(huh.NewGroup(
		huh.NewSelect[int]().
			Title("No estás dentro de un workspace. ¿En cuál lo creo?").
			Options(opts...).
			Value(&choice),
	)).WithTheme(ui.Theme()).Run(); err != nil {
		return "", "", err
	}
	return known[choice].ws, known[choice].name, nil
}

// modulesDir es la carpeta de módulos del workspace (liferay.workspace.modules.dir).
func modulesDir(ws string) string {
	d := liferay.ReadProperties(filepath.Join(ws, "gradle.properties"))["liferay.workspace.modules.dir"]
	if i := strings.Index(d, ","); i >= 0 {
		d = d[:i]
	}
	d = strings.TrimSpace(d)
	if d == "" || d == "*" {
		d = "modules"
	}
	if filepath.IsAbs(d) {
		return filepath.Clean(d)
	}
	return filepath.Join(ws, d)
}

func pickModuleType() (string, error) {
	opts := make([]huh.Option[string], len(scaffold.ModuleTypes))
	for i, t := range scaffold.ModuleTypes {
		opts[i] = huh.NewOption(fmt.Sprintf("%-34s %s", t.Title, t.Description), t.ID)
	}
	choice := scaffold.ModuleTypes[0].ID
	err := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().
			Title("¿Qué tipo de módulo?").
			Description("Las mismas plantillas que blade create.").
			Options(opts...).
			Height(len(opts) + 2).
			Value(&choice),
	)).WithTheme(ui.Theme()).Run()
	return choice, err
}

func classPrefixHint(t scaffold.ModuleType) string {
	switch t.ID {
	case "mvc-portlet", "panel-app":
		return "Se usa en las clases: <Prefijo>Portlet, <Prefijo>PortletKeys…"
	case "rest":
		return "Se usa en la clase <Prefijo>Application."
	case "api", "service", "service-wrapper":
		return "Es el nombre de la clase."
	}
	return "Se usa en el nombre de las clases generadas."
}

func serviceClassInput(t scaffold.ModuleType, v *string) huh.Field {
	in := huh.NewInput().Validate(scaffold.ValidQualifiedClass).Value(v)
	if t.NeedsClass == "service-wrapper" {
		return in.Title("Clase wrapper a extender").
			Description("Nombre completo de la clase *Wrapper del servicio que quieres sobrescribir.").
			Placeholder("com.liferay.portal.kernel.service.UserLocalServiceWrapper")
	}
	return in.Title("Interfaz del servicio").
		Description("Nombre completo de la interfaz que implementa el componente.").
		Placeholder("com.liferay.portal.kernel.events.LifecycleAction")
}

func required(msg string) func(string) error {
	return func(s string) error {
		if strings.TrimSpace(s) == "" {
			return errors.New(msg)
		}
		return nil
	}
}

// moduleFilesSummary lista los ficheros principales que se han generado.
func moduleFilesSummary(files []string) []string {
	var out []string
	for _, f := range files {
		if filepath.Base(f) == ".gitkeep" {
			continue
		}
		out = append(out, ui.OkText("+ ")+f)
	}
	const max = 12
	if len(out) > max {
		rest := len(out) - max
		out = append(out[:max], ui.MutedText(fmt.Sprintf("  … y %d más", rest)))
	}
	return out
}

func moduleNextSteps(t scaffold.ModuleType, o scaffold.ModuleOptions, ws, target, serverName string) []string {
	rel, err := filepath.Rel(ws, target)
	if err != nil || strings.HasPrefix(rel, "..") {
		rel = filepath.Base(target)
	}
	cdPath := ui.ShortPath(target)
	if cwd, err := os.Getwd(); err == nil {
		if r, err := filepath.Rel(cwd, target); err == nil && !strings.HasPrefix(r, "..") {
			cdPath = r
		}
	}
	if serverName == "" {
		serverName = "<server>"
	}
	var out []string
	switch {
	case t.ID == "service-builder":
		project := ":" + strings.ReplaceAll(filepath.ToSlash(rel), "/", ":") + ":" + o.Name + "-service"
		out = append(out,
			"Edita "+ui.Code(filepath.Join(rel, o.Name+"-service", "service.xml"))+" y genera el código:",
			ui.Code("./gradlew "+project+":buildService")+ui.MutedText("    desde la raíz del workspace"))
	case t.IsFragment:
		out = append(out, "Copia en "+ui.Code("src/main/resources/META-INF/resources")+" los JSP del anfitrión que quieras cambiar.")
	}
	out = append(out,
		ui.Code("cd "+cdPath),
		ui.Code("lray server deploy "+serverName)+ui.MutedText("    compila y lo despliega"))
	return out
}

// currentUser es el autor por defecto de los @author, como hace blade.
func currentUser() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		name := u.Username
		if i := strings.LastIndexAny(name, `\/`); i >= 0 { // DOMINIO\usuario en Windows
			name = name[i+1:]
		}
		return name
	}
	for _, k := range []string{"USER", "USERNAME"} {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return "lray"
}
