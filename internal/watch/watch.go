// Package watch vigila los ficheros de un workspace y decide qué tarea de
// Gradle toca en cada proyecto cuando cambian, igual que blade watch.
//
// Vigila por sondeo (recorre la carpeta cada poco y compara fecha y tamaño)
// en vez de con avisos del sistema: funciona igual en Linux, macOS y Windows,
// no tiene límites de inotify ni se pierde carpetas nuevas, y con las carpetas
// pesadas ignoradas (build, node_modules...) recorrer un workspace es barato.
package watch

import (
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Como blade watch: rutas que no se vigilan, ficheros que basta con copiar
// al módulo ya instalado (deployFast) y lo que marca la raíz de un proyecto.
var (
	DefaultIgnore = []string{
		".gradle", ".idea", ".settings", "**/.sass-cache", "**/bin", "**/build", "**/classes", "**/dist",
		"**/liferay-theme.json", "**/node_modules", "**/liferay-npm-bundler-report.html", "**/target", "bundles",
		"gradle",
		// Además de las de blade: git y los temporales de los editores, que
		// aparecen al abrir o guardar y harían desplegar sin haber cambiado nada.
		"**/.git", "**/.DS_Store", "**/.*.sw?", "**/*~", "**/.#*", "**/*___jb_tmp___", "**/*___jb_old___",
	}
	DefaultFast    = []string{"**/*.css", "**/*.js", "**/*.jsp", "**/*.map", "**/*.scss"}
	DefaultMarkers = []string{"src", "client-extension.yaml"}
)

// Matcher comprueba rutas relativas (con /) contra patrones como los de
// blade: «**» vale cero o más carpetas y el resto de segmentos siguen
// path.Match. Un patrón sin «**/» delante solo vale desde la raíz.
type Matcher struct{ patterns [][]string }

// NewMatcher prepara los patrones; \ cuenta como / (rutas de Windows).
func NewMatcher(patterns ...string) Matcher {
	var m Matcher
	for _, p := range patterns {
		p = strings.Trim(strings.ReplaceAll(strings.TrimSpace(p), `\`, "/"), "/")
		if p != "" {
			m.patterns = append(m.patterns, strings.Split(p, "/"))
		}
	}
	return m
}

// Match dice si rel encaja con algún patrón.
func (m Matcher) Match(rel string) bool {
	segs := strings.Split(rel, "/")
	for _, p := range m.patterns {
		if match(p, segs) {
			return true
		}
	}
	return false
}

func match(pat, segs []string) bool {
	if len(pat) == 0 {
		return len(segs) == 0
	}
	if pat[0] == "**" {
		for i := 0; i <= len(segs); i++ {
			if match(pat[1:], segs[i:]) {
				return true
			}
		}
		return false
	}
	if len(segs) == 0 {
		return false
	}
	ok, _ := path.Match(pat[0], segs[0])
	return ok && match(pat[1:], segs[1:])
}

// Entry es lo que se recuerda de cada ruta para saber si ha cambiado.
type Entry struct {
	ModTime time.Time
	Size    int64
	Dir     bool
}

// Snapshot es el estado de una carpeta: ruta relativa con / → entrada.
type Snapshot map[string]Entry

// Scan recorre root saltándose lo ignorado. Las rutas que desaparecen o no se
// pueden leer a mitad del recorrido se omiten: solo falla si root no se lee.
func Scan(root string, ignore Matcher) (Snapshot, error) {
	if _, err := os.Stat(root); err != nil {
		return nil, err
	}
	snap := Snapshot{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if p == root {
			return err
		}
		if err != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if ignore.Match(rel) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		snap[rel] = Entry{ModTime: info.ModTime(), Size: info.Size(), Dir: d.IsDir()}
		return nil
	})
	if err != nil && !errors.Is(err, fs.SkipDir) {
		return nil, err
	}
	return snap, nil
}

// Kind es el tipo de cambio de un fichero.
type Kind int

const (
	Created Kind = iota
	Modified
	Deleted
)

func (k Kind) String() string {
	switch k {
	case Created:
		return "nuevo"
	case Deleted:
		return "borrado"
	}
	return "cambiado"
}

// Change es un fichero que ha cambiado entre dos Snapshot.
type Change struct {
	Path string // relativa a la raíz, con /
	Kind Kind
}

// Diff da los ficheros nuevos, cambiados y borrados de old a cur, ordenados
// por ruta. Las carpetas no cuentan: lo que importa es lo que hay dentro.
func Diff(old, cur Snapshot) []Change {
	var out []Change
	for p, e := range cur {
		if e.Dir {
			continue
		}
		o, ok := old[p]
		switch {
		case !ok || o.Dir:
			out = append(out, Change{p, Created})
		case !o.ModTime.Equal(e.ModTime) || o.Size != e.Size:
			out = append(out, Change{p, Modified})
		}
	}
	for p, o := range old {
		if o.Dir {
			continue
		}
		if e, ok := cur[p]; !ok || e.Dir {
			out = append(out, Change{p, Deleted})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// Projects da las carpetas de proyecto de snap ("" = la raíz): las que tienen
// dentro algo de markers (blade usa src), quedándose con la más externa si
// hay unas dentro de otras.
func Projects(snap Snapshot, markers []string) []string {
	set := map[string]bool{}
	for p := range snap {
		for _, m := range markers {
			if path.Base(p) == m {
				dir := path.Dir(p)
				if dir == "." {
					dir = ""
				}
				set[dir] = true
			}
		}
	}
	return outermost(set)
}

// outermost ordena los proyectos y quita los que caen dentro de otro. Si la
// raíz es un proyecto, es el único.
func outermost(set map[string]bool) []string {
	if set[""] {
		return []string{""}
	}
	all := make([]string, 0, len(set))
	for p := range set {
		all = append(all, p)
	}
	sort.Strings(all) // los padres van antes que sus hijos
	var out []string
	for _, p := range all {
		if owner(out, p) == "" {
			out = append(out, p)
		}
	}
	return out
}

// owner devuelve el proyecto de projects que contiene rel, o "" si ninguno.
func owner(projects []string, rel string) string {
	for _, p := range projects {
		if p != "" && (rel == p || strings.HasPrefix(rel, p+"/")) {
			return p
		}
	}
	return ""
}

// Step es lo que hay que hacer en un proyecto tras unos cambios.
type Step struct {
	Project string   // carpeta relativa a la raíz, con / ("" = la raíz)
	Tasks   []string // argumentos de Gradle
	Changes []Change
}

// Fast dice si el paso solo copia ficheros al módulo ya instalado.
func (s Step) Fast() bool { return len(s.Tasks) > 0 && s.Tasks[0] == "deployFast" }

// Plan agrupa los cambios por proyecto (fuera de todos = la raíz) y elige la
// tarea igual que blade watch: si se ha borrado algo, clean deploy; si todo
// lo cambiado encaja con fast, deployFast -a; si no, deploy -a. -a evita
// recompilar los proyectos de los que depende.
func Plan(changes []Change, projects []string, fast Matcher) []Step {
	byProject := map[string][]Change{}
	var order []string
	for _, c := range changes {
		p := owner(projects, c.Path)
		if _, ok := byProject[p]; !ok {
			order = append(order, p)
		}
		byProject[p] = append(byProject[p], c)
	}
	sort.Strings(order)
	steps := make([]Step, 0, len(order))
	for _, p := range order {
		cs := byProject[p]
		deleted, allFast := false, true
		for _, c := range cs {
			if c.Kind == Deleted {
				deleted = true
			}
			if !fast.Match(c.Path) {
				allFast = false
			}
		}
		tasks := []string{"deploy", "-a"}
		switch {
		case deleted:
			tasks = []string{"clean", "deploy"}
		case allFast:
			tasks = []string{"deployFast", "-a"}
		}
		steps = append(steps, Step{Project: p, Tasks: tasks, Changes: cs})
	}
	return steps
}

// Options configura un Watcher.
type Options struct {
	Ignore  Matcher
	Fast    Matcher
	Markers []string
}

// Watcher junta los cambios de una carpeta entre llamadas a Poll.
type Watcher struct {
	root  string
	opt   Options
	base  Snapshot // lo último entregado
	last  Snapshot // la última pasada
	dirty bool
}

// New hace la primera pasada: lo que ya hay no cuenta como cambio.
func New(root string, opt Options) (*Watcher, error) {
	snap, err := Scan(root, opt.Ignore)
	if err != nil {
		return nil, err
	}
	return &Watcher{root: root, opt: opt, base: snap, last: snap}, nil
}

// Projects da los proyectos que se ven ahora mismo.
func (w *Watcher) Projects() []string { return Projects(w.last, w.opt.Markers) }

// Poll vuelve a recorrer la carpeta. Mientras se sigan viendo cambios nuevos
// espera (un guardado suele tocar varios ficheros seguidos); en la primera
// pasada tranquila entrega todo lo cambiado desde la entrega anterior, así
// que un fichero que aparece y desaparece entre medias no cuenta. Los
// proyectos salen de antes y de después, para que un módulo borrado no
// acabe en la raíz.
func (w *Watcher) Poll() ([]Step, error) {
	cur, err := Scan(w.root, w.opt.Ignore)
	if err != nil {
		return nil, err
	}
	if len(Diff(w.last, cur)) > 0 {
		w.last, w.dirty = cur, true
		return nil, nil
	}
	if !w.dirty {
		return nil, nil
	}
	changes := Diff(w.base, cur)
	projects := union(Projects(w.base, w.opt.Markers), Projects(cur, w.opt.Markers))
	w.base, w.last, w.dirty = cur, cur, false
	return Plan(changes, projects, w.opt.Fast), nil
}

// union junta dos listas de proyectos (la más externa manda, como en
// Projects).
func union(a, b []string) []string {
	set := map[string]bool{}
	for _, p := range append(a, b...) {
		set[p] = true
	}
	return outermost(set)
}
