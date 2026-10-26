package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/katarem/lray/internal/config"
	"github.com/katarem/lray/internal/liferay"
	"github.com/katarem/lray/internal/ui"
)

func newDeploy() *cobra.Command {
	var clean bool
	cmd := &cobra.Command{
		Use:   "deploy <nombre>",
		Short: "Compila y despliega en el server: todo el workspace o solo el módulo donde estés",
		Long: `Compila y despliega en el server indicado, igual que blade deploy:
desde la raíz del workspace despliega todos los módulos; desde dentro de
un módulo (o de una carpeta de módulos) solo lo que cuelga de ahí.`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeServers,
		RunE: func(_ *cobra.Command, args []string) error {
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
			scope := scopeOf(ws, cwd)

			var tasks []string
			if clean {
				tasks = append(tasks, "clean")
			}
			tasks = append(tasks, "deploy")
			res, err := gradleDeploy(fmt.Sprintf("Desplegando %s en «%s»", scope, s.Name), fmt.Sprintf("Desplegado en «%s»", s.Name),
				ws, cwd, s, l, tasks)
			if errors.Is(err, ui.ErrInterrupted) {
				return err
			}
			if err != nil {
				printGradleFailure(res.tail)
				return errors.New("la compilación ha fallado; arriba tienes los errores")
			}
			deployed, unchanged := res.deployed, res.unchanged

			var body []string
			for _, p := range deployed {
				body = append(body, ui.OkText("↑ ")+p)
			}
			for _, p := range unchanged {
				body = append(body, ui.MutedText("= "+p+" (sin cambios)"))
			}
			if len(body) == 0 {
				body = append(body, ui.MutedText("No había nada que desplegar aquí."))
			}
			body = append(body, "")
			if l.State() == liferay.Stopped {
				body = append(body, ui.WarnText("El server está apagado: ")+"se instalará al arrancarlo con "+ui.Code("lray server start "+s.Name))
			} else {
				body = append(body, "Mira cómo se instalan con "+ui.Code("lray server logs "+s.Name))
			}
			mood := ui.Party
			if len(deployed) == 0 {
				mood = ui.Thinking
			}
			ui.Say(mood, fmt.Sprintf("Listo en «%s»", s.Name), body...)
			return nil
		},
	}
	cmd.Flags().BoolVar(&clean, "clean", false, "Hacer clean antes de compilar")
	return cmd
}

type deployResult struct {
	deployed, unchanged []string // proyectos, con / («modules/foo»)
	tail                []string // últimas líneas de Gradle
}

// gradleDeploy ejecuta las tareas de Gradle en dir con un spinner y apunta
// qué proyectos se han desplegado y cuáles no tenían cambios. Gradle usa como
// proyecto la carpeta desde la que se lanza, así que basta con ejecutar el
// wrapper en dir. La propiedad redirige el deploy al liferay home del server.
func gradleDeploy(title, doneTitle, ws, dir string, s *config.Server, l *liferay.Layout, tasks []string) (deployResult, error) {
	var res deployResult
	args := append(append([]string{}, tasks...), "-Pliferay.workspace.home.dir="+l.Home)
	err := ui.RunTask(title, doneTitle, func(ctx context.Context, update func(string)) error {
		lines, err := liferay.RunGradle(ctx, ws, dir, s.JavaHome, args, func(line string) {
			task, ok := strings.CutPrefix(line, "> Task ")
			if !ok {
				return
			}
			update(task)
			fields := strings.Fields(task)
			if len(fields) == 0 {
				return
			}
			i := strings.LastIndex(fields[0], ":")
			if i < 0 || (fields[0][i+1:] != "deploy" && fields[0][i+1:] != "deployFast") {
				return
			}
			project := strings.ReplaceAll(strings.Trim(fields[0][:i], ":"), ":", "/")
			switch {
			case project == "":
			case len(fields) == 1:
				res.deployed = append(res.deployed, project)
			case fields[1] == "UP-TO-DATE":
				res.unchanged = append(res.unchanged, project)
			}
		})
		res.tail = lines
		return err
	})
	return res, err
}
