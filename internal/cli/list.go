package cli

import (
	"fmt"
	"sync"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/spf13/cobra"

	"github.com/tu-usuario/lray/internal/config"
	"github.com/tu-usuario/lray/internal/liferay"
	"github.com/tu-usuario/lray/internal/ui"
)

func newList() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "Lista tus servers con su versión y estado",
		Args:    cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			reg, err := config.Load()
			if err != nil {
				return err
			}
			if len(reg.Servers) == 0 {
				ui.Say(ui.Sleepy, "Todavía no tienes servers",
					"Añade uno que ya exista:  "+ui.Code("lray add <nombre> <ruta>"),
					"o crea uno nuevo:         "+ui.Code("lray init <nombre>"))
				return nil
			}

			type row struct {
				version, state, port string
				on                   bool
			}
			rows := make([]row, len(reg.Servers))
			var wg sync.WaitGroup
			for i := range reg.Servers {
				wg.Add(1)
				go func(i int, s config.Server) { // las sondas HTTP van en paralelo
					defer wg.Done()
					r := row{version: s.Version, port: "—"}
					l, err := liferay.Resolve(s.Path)
					switch {
					case err != nil:
						r.state = ui.BadText("✘ ruta no encontrada")
					case !l.HasBundle():
						r.state = ui.MutedText("○ sin bundle")
					default:
						st := l.State()
						r.state, r.on, r.port = stateLabel(st), st != liferay.Stopped, fmt.Sprint(l.Port())
					}
					if r.version == "" && l != nil {
						r.version = liferay.DetectVersion(l)
					}
					rows[i] = r
				}(i, reg.Servers[i])
			}
			wg.Wait()

			changed, on := false, 0
			data := make([][]string, len(rows))
			for i, r := range rows {
				s := &reg.Servers[i]
				if s.Version == "" && r.version != "" {
					s.Version, changed = r.version, true
				}
				if r.on {
					on++
				}
				data[i] = []string{ui.Bold(s.Name), orDash(r.version), r.state, r.port, ui.MutedText(ui.ShortPath(s.Path))}
			}
			if changed {
				_ = reg.Save()
			}

			head := lipgloss.NewStyle().Foreground(ui.Sea).Bold(true).Padding(0, 1)
			cell := lipgloss.NewStyle().Padding(0, 1)
			t := table.New().
				Border(lipgloss.RoundedBorder()).
				BorderStyle(lipgloss.NewStyle().Foreground(ui.Muted)).
				Headers("Nombre", "Versión", "Estado", "Puerto", "Ruta").
				Rows(data...).
				StyleFunc(func(r, _ int) lipgloss.Style {
					if r == table.HeaderRow {
						return head
					}
					return cell
				})
			fmt.Println()
			fmt.Println(t)
			fmt.Println(ui.MutedText(fmt.Sprintf("  %d servers, %d encendidos", len(rows), on)))
			fmt.Println()
			return nil
		},
	}
}
