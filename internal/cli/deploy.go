package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tu-usuario/lray/internal/liferay"
	"github.com/tu-usuario/lray/internal/ui"
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
			scope := "todo el workspace"
			if rel, _ := filepath.Rel(ws, cwd); rel != "." {
				scope = filepath.ToSlash(rel)
			}

			gradleArgs := []string{}
			if clean {
				gradleArgs = append(gradleArgs, "clean")
			}
			// Gradle usa como proyecto la carpeta desde la que se lanza, así que
			// basta con ejecutar el wrapper en cwd. La propiedad redirige el
			// deploy al liferay home del server elegido.
			gradleArgs = append(gradleArgs, "deploy", "-Pliferay.workspace.home.dir="+l.Home)

			var deployed, unchanged, tail []string
			err = ui.RunTask(fmt.Sprintf("Desplegando %s en «%s»", scope, s.Name), fmt.Sprintf("Desplegado en «%s»", s.Name),
				func(ctx context.Context, update func(string)) error {
					lines, err := liferay.RunGradle(ctx, ws, cwd, s.JavaHome, gradleArgs, func(line string) {
						task, ok := strings.CutPrefix(line, "> Task ")
						if !ok {
							return
						}
						update(task)
						fields := strings.Fields(task)
						if len(fields) == 0 || !strings.HasSuffix(fields[0], ":deploy") {
							return
						}
						project := strings.TrimSuffix(strings.TrimPrefix(fields[0], ":"), ":deploy")
						project = strings.ReplaceAll(project, ":", "/")
						switch {
						case len(fields) == 1:
							deployed = append(deployed, project)
						case fields[1] == "UP-TO-DATE":
							unchanged = append(unchanged, project)
						}
					})
					tail = lines
					return err
				})
			if errors.Is(err, ui.ErrInterrupted) {
				return err
			}
			if err != nil {
				printGradleFailure(tail)
				return errors.New("la compilación ha fallado; arriba tienes los errores")
			}

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
				body = append(body, ui.WarnText("El server está apagado: ")+"se instalará al arrancarlo con "+ui.Code("lray start "+s.Name))
			} else {
				body = append(body, "Mira cómo se instalan con "+ui.Code("lray logs "+s.Name))
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
