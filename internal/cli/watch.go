package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/katarem/lray/internal/config"
	"github.com/katarem/lray/internal/liferay"
	"github.com/katarem/lray/internal/ui"
	"github.com/katarem/lray/internal/watch"
)

// watchInterval es cada cuánto se vuelven a mirar los ficheros.
const watchInterval = 500 * time.Millisecond

func newWatch() *cobra.Command {
	var skipInit bool
	var fast, ignore []string
	cmd := &cobra.Command{
		Use:   "watch <nombre>",
		Short: "Vigila tus módulos y los despliega en el server cada vez que guardas, como blade watch",
		Long: `Vigila los ficheros de la carpeta actual (todo el workspace o solo el
módulo donde estés, como deploy) y, cada vez que guardas, despliega en el
server el proyecto que ha cambiado, igual que blade watch:

  - JSP, CSS, SCSS, JS y .map: deployFast -a, que copia los ficheros al
    módulo ya instalado sin reinstalarlo
  - cualquier otro cambio: deploy -a (sin recompilar sus dependencias)
  - si se ha borrado algo: clean deploy

Antes de empezar despliega todo lo de la carpeta (-s para saltártelo).
Un proyecto es una carpeta con src/ o client-extension.yaml; lo que cambie
fuera de todos se despliega desde la carpeta actual. No se vigilan build,
bin, dist, node_modules, .gradle, bundles, .git ni los temporales de los
editores. Ctrl+C para salir.`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeServers,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, s, l, err := loadServer(args[0])
			if err != nil {
				return err
			}
			if !l.HasBundle() {
				return liferay.ErrNoBundle
			}
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			ws, ok := liferay.FindWorkspace(cwd)
			if !ok {
				return errors.New("aquí no hay ningún workspace de Liferay; entra en tu workspace o en uno de sus módulos")
			}
			ignore = append(append([]string{}, watch.DefaultIgnore...), ignore...)
			// Un liferay.workspace.home.dir dentro de la carpeta que no se llame
			// bundles también cambia al desplegar: vigilarlo sería un bucle.
			if rel, err := filepath.Rel(cwd, l.Home); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
				ignore = append(ignore, rel)
			}
			opt := watch.Options{
				Ignore:  watch.NewMatcher(ignore...),
				Fast:    watch.NewMatcher(append(append([]string{}, watch.DefaultFast...), fast...)...),
				Markers: watch.DefaultMarkers,
			}
			return runWatch(cmd.Context(), s, l, ws, cwd, opt, skipInit)
		},
	}
	f := cmd.Flags()
	f.BoolVarP(&skipInit, "skip-init", "s", false, "No desplegar al empezar, solo cuando cambie algo")
	f.StringSliceVarP(&fast, "fast", "f", nil, "Más ficheros que se despliegan con deployFast, como patrones (p. ej. **/*.ftl)")
	f.StringSliceVarP(&ignore, "ignore", "i", nil, "Más rutas que no se vigilan, como patrones (p. ej. **/generated)")
	return cmd
}

func runWatch(parent context.Context, s *config.Server, l *liferay.Layout, ws, dir string, opt watch.Options, skipInit bool) error {
	// Con terminal, Ctrl+C durante un deploy lo recoge el spinner
	// (ErrInterrupted); mientras espera, o sin terminal, llega como señal.
	ctx, cancel := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer cancel()
	defer ui.SetTitle(s.Name + " · watch · lray")()

	w, err := watch.New(dir, opt)
	if err != nil {
		return fmt.Errorf("no puedo leer %s: %w", dir, err)
	}
	body := []string{ui.MutedText("Carpeta    ") + ui.ShortPath(dir)}
	projects := w.Projects()
	switch {
	case len(projects) == 1 && projects[0] == "":
		body = append(body, ui.MutedText("Proyecto   ")+scopeOf(ws, dir))
	case len(projects) > 0:
		names := make([]string, 0, len(projects))
		for _, p := range projects {
			names = append(names, scopeOf(ws, filepath.Join(dir, filepath.FromSlash(p))))
		}
		body = append(body, ui.MutedText(fmt.Sprintf("Proyectos  %d: ", len(names)))+listSome(names, 6))
	}
	if l.State() == liferay.Stopped {
		body = append(body, "", ui.WarnText("El server está apagado: ")+"lo que despliegue se instalará al arrancarlo con "+ui.Code("lray server start "+s.Name))
	}
	body = append(body, "", ui.MutedText("Ctrl+C para salir"))
	ui.Say(ui.Happy, fmt.Sprintf("Vigilando cambios para «%s»", s.Name), body...)

	d := &watchDeployer{s: s, l: l, ws: ws}
	if !skipInit {
		if err := d.run(ctx, dir, []string{"deploy"}, "todo lo de esta carpeta"); err != nil {
			return watchBye(s, err)
		}
	}
	waiting := func() { fmt.Println(ui.MutedText("  Esperando cambios… (Ctrl+C para salir)")) }
	waiting()

	tick := time.NewTicker(watchInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return watchBye(s, nil)
		case <-tick.C:
		}
		steps, err := w.Poll()
		if err != nil {
			return fmt.Errorf("no puedo leer %s: %w", dir, err)
		}
		for _, st := range steps {
			pdir := filepath.Join(dir, filepath.FromSlash(st.Project))
			name := scopeOf(ws, pdir)
			fmt.Println()
			fmt.Println(ui.MutedText(time.Now().Format("15:04:05")) + " " + ui.Bold(name) + " " + describeChanges(st.Changes))
			if _, err := os.Stat(pdir); err != nil {
				ui.Warn("La carpeta de " + name + " ya no existe; no despliego nada")
				continue
			}
			if err := d.run(ctx, pdir, st.Tasks, name); err != nil {
				return watchBye(s, err)
			}
		}
		if len(steps) > 0 {
			waiting()
		}
	}
}

// watchDeployer lanza los deploys de watch y recuerda si el plugin del
// workspace no tiene deployFast (versiones antiguas), para no reintentarlo.
type watchDeployer struct {
	s      *config.Server
	l      *liferay.Layout
	ws     string
	noFast bool
}

// run despliega en dir. Un fallo de compilación no para la vigilancia (se
// enseña y se sigue); solo devuelve error si el usuario interrumpe.
func (d *watchDeployer) run(ctx context.Context, dir string, tasks []string, name string) error {
	if d.noFast && tasks[0] == "deployFast" {
		tasks = []string{"deploy", "-a"}
	}
	label := strings.Join(tasks, " ")
	res, err := gradleDeploy(fmt.Sprintf("%s: %s", name, label), fmt.Sprintf("%s: %s", name, label), d.ws, dir, d.s, d.l, tasks)
	if errors.Is(err, ui.ErrInterrupted) || ctx.Err() != nil {
		return ui.ErrInterrupted
	}
	if err != nil && tasks[0] == "deployFast" && fastMissing(res.tail) {
		d.noFast = true
		ui.Warn("El plugin del workspace no tiene deployFast; uso deploy -a")
		return d.run(ctx, dir, []string{"deploy", "-a"}, name)
	}
	if err != nil {
		printGradleFailure(res.tail)
		ui.Warn("No se ha podido desplegar; arréglalo y guarda otra vez")
		return nil
	}
	for _, p := range res.deployed {
		fmt.Println("  " + ui.OkText("↑ ") + p)
	}
	for _, p := range res.unchanged {
		fmt.Println("  " + ui.MutedText("= "+p+" (sin cambios)"))
	}
	return nil
}

// fastMissing dice si Gradle ha fallado porque no existe la tarea deployFast.
func fastMissing(tail []string) bool {
	for _, l := range tail {
		if strings.Contains(l, "'deployFast' not found") {
			return true
		}
	}
	return false
}

func watchBye(s *config.Server, err error) error {
	if err != nil && !errors.Is(err, ui.ErrInterrupted) {
		return err
	}
	fmt.Println()
	ui.Say(ui.Sleepy, fmt.Sprintf("Dejo de vigilar «%s»", s.Name),
		"Lo ya desplegado sigue instalado.",
		ui.MutedText("Logs  ")+ui.Code("lray server logs "+s.Name))
	return nil
}

// scopeOf nombra una carpeta del workspace como lo hace deploy.
func scopeOf(ws, dir string) string {
	if rel, err := filepath.Rel(ws, dir); err == nil && rel != "." {
		return filepath.ToSlash(rel)
	}
	return "todo el workspace"
}

// describeChanges resume los ficheros cambiados de un proyecto.
func describeChanges(cs []watch.Change) string {
	if len(cs) == 1 {
		return ui.MutedText(cs[0].Kind.String() + ": " + filepath.Base(cs[0].Path))
	}
	names := make([]string, 0, len(cs))
	for _, c := range cs {
		names = append(names, filepath.Base(c.Path))
	}
	return ui.MutedText(fmt.Sprintf("%d ficheros: ", len(cs)) + listSome(names, 3))
}

// listSome junta los primeros n nombres y dice cuántos quedan.
func listSome(names []string, n int) string {
	if len(names) <= n {
		return strings.Join(names, ", ")
	}
	return strings.Join(names[:n], ", ") + fmt.Sprintf(" y %d más", len(names)-n)
}
