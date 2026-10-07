package scaffold

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"text/template"
	"unicode"
)

// Las plantillas de módulos son las de blade (com.liferay.project.templates)
// pasadas a text/template. En las rutas, __pkg__ es el paquete como carpetas,
// __Class__ el prefijo de clases y __name__ el nombre del módulo.
//
//go:embed all:templates
var templatesFS embed.FS

// CSSBuilderVersion es la versión de com.liferay.css.builder que ponen las
// plantillas de blade en los módulos con SCSS.
const CSSBuilderVersion = "3.1.4"

// ModuleType describe un tipo de módulo que se puede generar.
type ModuleType struct {
	ID          string // nombre de la plantilla, igual que en blade
	Title       string
	Description string
	NeedsClass  string // si pide una clase: "service" o "service-wrapper"
	IsFragment  bool   // pide el bundle anfitrión
	StripSuffix string // sufijo que sobra en el prefijo de clases (Portlet)
	NoClass     bool   // no genera clases Java
}

// ModuleTypes son los tipos disponibles, en el orden en que se ofrecen.
var ModuleTypes = []ModuleType{
	{ID: "mvc-portlet", Title: "Portlet MVC", Description: "Portlet con JSP sobre MVCPortlet", StripSuffix: "Portlet"},
	{ID: "panel-app", Title: "Aplicación del panel de control", Description: "Portlet con su entrada y categoría en el menú de producto"},
	{ID: "api", Title: "API", Description: "Interfaz Java exportada para que la usen otros módulos"},
	{ID: "service", Title: "Servicio OSGi", Description: "Componente que implementa una interfaz de servicio", NeedsClass: "service"},
	{ID: "service-builder", Title: "Service Builder", Description: "Módulos -api y -service con service.xml"},
	{ID: "service-wrapper", Title: "Service wrapper", Description: "Sobrescribe métodos de un servicio de Liferay", NeedsClass: "service-wrapper"},
	{ID: "rest", Title: "API REST (JAX-RS)", Description: "Aplicación JAX-RS publicada en el whiteboard"},
	{ID: "fragment", Title: "Fragmento OSGi", Description: "Sobrescribe JSP de otro módulo (Fragment-Host)", IsFragment: true, NoClass: true},
	{ID: "control-menu-entry", Title: "Entrada del menú de control", Description: "Enlace en la barra superior de Liferay"},
	{ID: "portlet-configuration-icon", Title: "Icono de configuración de portlet", Description: "Opción nueva en el menú de opciones de un portlet"},
	{ID: "template-context-contributor", Title: "Template context contributor", Description: "Añade variables al contexto de los temas"},
}

// FindModuleType busca un tipo por su ID.
func FindModuleType(id string) (ModuleType, bool) {
	for _, t := range ModuleTypes {
		if t.ID == id {
			return t, true
		}
	}
	return ModuleType{}, false
}

// ModuleTypeIDs devuelve los IDs disponibles (para ayuda y autocompletado).
func ModuleTypeIDs() []string {
	ids := make([]string, len(ModuleTypes))
	for i, t := range ModuleTypes {
		ids[i] = t.ID
	}
	return ids
}

// ModuleOptions configura el módulo a generar.
type ModuleOptions struct {
	Type         string
	Name         string // nombre del módulo y de su carpeta
	Package      string
	ClassName    string // prefijo de las clases
	Author       string
	Product      string // clave de liferay.workspace.product (dxp-2025.q1.0-lts, portal-7.4-ga132...)
	ServiceClass string // service: interfaz a implementar; service-wrapper: clase *Wrapper a extender
	HostBundle   string // fragment: Bundle-SymbolicName del anfitrión
	HostVersion  string // fragment: versión del anfitrión
	Workspace    string // raíz del workspace (para las rutas de proyecto de Gradle)
}

// ------------------------------------------------------------ nombres

// DefaultClassName deriva el prefijo de clases del nombre, como blade:
// "mi-portlet.web" -> "MiPortletWeb" (y quita "Portlet" al final en los portlets).
func DefaultClassName(t ModuleType, name string) string {
	var b strings.Builder
	upper := true
	for _, r := range name {
		if r == '-' || r == '.' || r == ' ' || r == '_' {
			upper = true
			continue
		}
		if !isJavaPart(r) {
			continue
		}
		if upper {
			r = unicode.ToUpper(r)
			upper = false
		}
		b.WriteRune(r)
	}
	cn := b.String()
	if t.StripSuffix != "" && len(cn) > len(t.StripSuffix) && strings.HasSuffix(cn, t.StripSuffix) {
		cn = strings.TrimSuffix(cn, t.StripSuffix)
	}
	return cn
}

// DefaultPackage deriva el paquete del nombre, como blade: "mi-portlet" -> "mi.portlet".
func DefaultPackage(name string) string {
	parts := strings.FieldsFunc(strings.ToLower(name), func(r rune) bool {
		return r == '-' || r == '.' || r == ' '
	})
	return strings.Join(parts, ".")
}

var moduleNameRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9._-]*$`)

// ValidModuleName comprueba que el nombre valga como carpeta y como proyecto de Gradle.
func ValidModuleName(name string) error {
	if !moduleNameRe.MatchString(name) {
		return fmt.Errorf("nombre de módulo no válido %q: empieza por una letra y usa letras, números, '.', '_' o '-'", name)
	}
	return nil
}

// ValidPackage comprueba que sea un paquete Java válido.
func ValidPackage(pkg string) error {
	if pkg == "" {
		return errors.New("el paquete no puede estar vacío")
	}
	for _, part := range strings.Split(pkg, ".") {
		if err := validIdentifier(part); err != nil {
			return fmt.Errorf("paquete no válido %q: %w", pkg, err)
		}
	}
	return nil
}

// ValidClassName comprueba que el prefijo sirva como nombre de clase Java.
func ValidClassName(cn string) error {
	if cn == "" {
		return errors.New("el prefijo de clases no puede estar vacío")
	}
	if err := validIdentifier(cn); err != nil {
		return fmt.Errorf("prefijo de clases no válido %q: %w", cn, err)
	}
	return nil
}

// ValidQualifiedClass comprueba un nombre de clase completo (com.ejemplo.Clase).
func ValidQualifiedClass(fqcn string) error {
	i := strings.LastIndex(fqcn, ".")
	if i <= 0 {
		return fmt.Errorf("%q no es un nombre de clase completo (paquete.Clase)", fqcn)
	}
	if err := ValidPackage(fqcn[:i]); err != nil {
		return err
	}
	return ValidClassName(fqcn[i+1:])
}

func validIdentifier(s string) error {
	if s == "" {
		return errors.New("hay un segmento vacío")
	}
	for i, r := range s {
		if (i == 0 && !isJavaStart(r)) || !isJavaPart(r) {
			return fmt.Errorf("«%s» no es un identificador Java", s)
		}
	}
	if javaKeywords[s] {
		return fmt.Errorf("«%s» es una palabra reservada de Java", s)
	}
	return nil
}

func isJavaStart(r rune) bool { return unicode.IsLetter(r) || r == '_' || r == '$' }
func isJavaPart(r rune) bool  { return isJavaStart(r) || unicode.IsDigit(r) }

var javaKeywords = func() map[string]bool {
	m := map[string]bool{}
	for _, k := range strings.Fields(`abstract assert boolean break byte case catch char class const continue
		default do double else enum extends false final finally float for goto if implements import
		instanceof int interface long native new null package private protected public return short
		static strictfp super switch synchronized this throw throws transient true try void volatile while _`) {
		m[k] = true
	}
	return m
}()

// ------------------------------------------------------------ versión de Liferay

var (
	quarterlyRe = regexp.MustCompile(`(\d{4})\.q(\d)`)
	classicRe   = regexp.MustCompile(`(?:^|[^\d.])7\.(\d)`)
)

// Edition devuelve "dxp" o "portal" según la clave de producto.
func Edition(product string) string {
	p := strings.ToLower(product)
	if strings.Contains(p, "dxp") || strings.Contains(p, "-u") || strings.Contains(p, ".u") {
		return "dxp"
	}
	return "portal"
}

// IsJakarta indica si esa versión de Liferay usa Jakarta EE (2025.Q3 en adelante),
// con el mismo criterio que blade.
func IsJakarta(product string) bool {
	m := quarterlyRe.FindStringSubmatch(strings.ToLower(product))
	if m == nil {
		return false
	}
	year, _ := strconv.Atoi(m[1])
	q, _ := strconv.Atoi(m[2])
	return year > 2025 || (year == 2025 && q >= 3)
}

// classicMinor devuelve el x de las 7.x (4 para las trimestrales y si no se sabe).
func classicMinor(product string) int {
	p := strings.ToLower(product)
	if quarterlyRe.MatchString(p) {
		return 4
	}
	if m := classicRe.FindStringSubmatch(p); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	return 4
}

// ------------------------------------------------------------ generación

type moduleData struct {
	ModuleOptions
	Product           string // dxp | portal
	Jakarta           bool
	Legacy            bool // anterior a 7.4
	SchemaMinor       int  // 7.x del DTD de service.xml
	DSAnnotations     bool
	APIPath           string
	CSSBuilderVersion string
}

var moduleFuncs = template.FuncMap{
	"upper":      strings.ToUpper,
	"lower":      strings.ToLower,
	"underscore": func(s string) string { return strings.ReplaceAll(s, ".", "_") },
	"simple": func(fqcn string) string {
		return fqcn[strings.LastIndex(fqcn, ".")+1:]
	},
}

// CreateModule genera el módulo en dir/<Name> y devuelve las rutas creadas,
// relativas a dir. dir no tiene por qué existir.
func CreateModule(dir string, o ModuleOptions) ([]string, error) {
	t, ok := FindModuleType(o.Type)
	if !ok {
		return nil, fmt.Errorf("no conozco el tipo de módulo %q; los disponibles son: %s", o.Type, strings.Join(ModuleTypeIDs(), ", "))
	}
	if err := ValidModuleName(o.Name); err != nil {
		return nil, err
	}
	if err := ValidPackage(o.Package); err != nil {
		return nil, err
	}
	if !t.NoClass {
		if err := ValidClassName(o.ClassName); err != nil {
			return nil, err
		}
	}
	if t.NeedsClass != "" {
		if err := ValidQualifiedClass(o.ServiceClass); err != nil {
			return nil, err
		}
	}
	if t.IsFragment && (strings.TrimSpace(o.HostBundle) == "" || strings.TrimSpace(o.HostVersion) == "") {
		return nil, errors.New("un fragmento necesita el Bundle-SymbolicName y la versión del módulo anfitrión")
	}
	if o.Author == "" {
		o.Author = "lray"
	}

	target := filepath.Join(dir, o.Name)
	if entries, err := os.ReadDir(target); err == nil && len(entries) > 0 {
		return nil, fmt.Errorf("la carpeta %s ya existe y no está vacía", target)
	}

	minor := classicMinor(o.Product)
	data := moduleData{
		ModuleOptions:     o,
		Product:           Edition(o.Product),
		Jakarta:           IsJakarta(o.Product),
		Legacy:            minor < 4,
		SchemaMinor:       minor,
		DSAnnotations:     minor >= 2,
		APIPath:           gradlePath(o.Workspace, target) + ":" + o.Name + "-api",
		CSSBuilderVersion: CSSBuilderVersion,
	}

	root := "templates/" + t.ID
	var created []string
	err := fs.WalkDir(templatesFS, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		raw, err := templatesFS.ReadFile(p)
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(p, root+"/")
		rel = strings.ReplaceAll(rel, "__pkg__", strings.ReplaceAll(o.Package, ".", "/"))
		rel = strings.ReplaceAll(rel, "__Class__", o.ClassName)
		rel = strings.ReplaceAll(rel, "__name__", o.Name)

		content := raw
		if path.Base(rel) != ".gitkeep" {
			tpl, err := template.New(p).Funcs(moduleFuncs).Option("missingkey=error").Parse(string(raw))
			if err != nil {
				return fmt.Errorf("plantilla %s: %w", p, err)
			}
			var buf bytes.Buffer
			if err := tpl.Execute(&buf, data); err != nil {
				return fmt.Errorf("plantilla %s: %w", p, err)
			}
			content = buf.Bytes()
			if data.Jakarta {
				content = toJakarta(rel, content)
			}
		}
		if err := write(filepath.Join(target, filepath.FromSlash(rel)), content, 0o644); err != nil {
			return err
		}
		created = append(created, path.Join(o.Name, rel))
		return nil
	})
	if err != nil {
		return created, err
	}
	sort.Strings(created)
	return created, nil
}

// toJakarta aplica lo mismo que blade para las versiones Jakarta: todo javax
// pasa a jakarta y la taglib core de JSTL cambia de URI.
func toJakarta(rel string, content []byte) []byte {
	if strings.HasSuffix(rel, ".jsp") {
		content = bytes.ReplaceAll(content, []byte("http://java.sun.com/jsp/jstl/core"), []byte("jakarta.tags.core"))
	}
	return bytes.ReplaceAll(content, []byte("javax"), []byte("jakarta"))
}

// gradlePath convierte la carpeta del módulo en su ruta de proyecto de Gradle
// dentro del workspace (p. ej. :modules:foo).
func gradlePath(ws, target string) string {
	if ws == "" {
		return ":" + filepath.Base(target)
	}
	rel, err := filepath.Rel(ws, target)
	if err != nil || strings.HasPrefix(rel, "..") {
		return ":" + filepath.Base(target)
	}
	return ":" + strings.ReplaceAll(filepath.ToSlash(rel), "/", ":")
}
