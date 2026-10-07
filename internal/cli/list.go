package cli

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/spf13/cobra"

	"github.com/katarem/lray/internal/config"
	"github.com/katarem/lray/internal/liferay"
	"github.com/katarem/lray/internal/ssh"
	"github.com/katarem/lray/internal/ui"
)

func newList() *cobra.Command {
	var syncRemote, onlyLocal, onlyRemote bool
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "Lista tus servers con su versión y estado",
		Long: `Lista tus servers con su versión y estado.

El estado de los locales se mira en el momento. El de los remotos es el
último conocido (sin conectarse, así funciona sin VPN); --sync se conecta a
ellos y lo actualiza.`,
		Example: `  lray server list
  lray server list --sync
  lray server list --local
  lray server list --remote --sync`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if onlyLocal && onlyRemote {
				return errors.New("elige --local o --remote, no los dos")
			}
			reg, err := config.Load()
			if err != nil {
				return err
			}
			if len(reg.Servers) == 0 {
				ui.Say(ui.Sleepy, "Todavía no tienes servers",
					"Añade uno que ya exista:  "+ui.Code("lray server add <nombre> <ruta>"),
					"o uno por SSH:            "+ui.Code("lray server add <nombre> usuario@máquina:/ruta"),
					"o crea uno nuevo:         "+ui.Code("lray server init <nombre>"))
				return nil
			}

			var locals, remotes []*config.Server
			for i := range reg.Servers {
				s := &reg.Servers[i]
				switch {
				case s.IsRemote() && !onlyLocal:
					remotes = append(remotes, s)
				case !s.IsRemote() && !onlyRemote:
					locals = append(locals, s)
				}
			}
			if len(locals)+len(remotes) == 0 {
				what := "locales"
				if onlyRemote {
					what = "remotos"
				}
				ui.Say(ui.Sleepy, "No tienes servers "+what)
				return nil
			}

			states, err := config.LoadStates()
			if err != nil {
				return err
			}
			changed := false
			if syncRemote && len(remotes) > 0 {
				changed = syncRemotes(cmd.Context(), remotes, states)
				_ = states.Save()
			}

			type row struct {
				version, state, port string
				on                   bool
			}
			rows := make([]row, len(locals))
			var wg sync.WaitGroup
			for i, s := range locals {
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
				}(i, *s)
			}
			wg.Wait()

			on := 0
			var data [][]string
			for i, r := range rows {
				s := locals[i]
				if s.Version == "" && r.version != "" {
					s.Version, changed = r.version, true
				}
				if r.on {
					on++
				}
				data = append(data, []string{ui.Bold(s.Name), orDash(r.version), r.state, r.port, ui.MutedText(ui.ShortPath(s.Path))})
			}
			stale := 0
			for _, s := range remotes {
				snap, ok := states.Servers[s.Name]
				if ok && snap.Reachable() && parseState(snap.State) != liferay.Stopped {
					on++
				}
				if !syncRemote {
					stale++
				}
				port := "—"
				if s.Port != 0 {
					port = fmt.Sprint(s.Port)
				}
				data = append(data, []string{ui.Bold(s.Name), orDash(s.Version), snapshotLabel(snap, ok), port, ui.MutedText(s.Location())})
			}
			if changed {
				_ = reg.Save()
			}

			head := lipgloss.NewStyle().Foreground(ui.Sea).Bold(true).Padding(0, 1)
			cell := lipgloss.NewStyle().Padding(0, 1)
			t := table.New().
				Border(lipgloss.RoundedBorder()).
				BorderStyle(lipgloss.NewStyle().Foreground(ui.Muted)).
				Headers("Nombre", "Versión", "Estado", "Puerto", "Ubicación").
				Rows(data...).
				StyleFunc(func(r, _ int) lipgloss.Style {
					if r == table.HeaderRow {
						return head
					}
					return cell
				})
			fmt.Println()
			fmt.Println(t)
			fmt.Println(ui.MutedText(fmt.Sprintf("  %d servers, %d encendidos", len(data), on)))
			if stale > 0 {
				fmt.Println(ui.MutedText("  Remotos: último estado conocido. Actualízalo con ") + ui.Code("lray server list --sync"))
			}
			fmt.Println()
			return nil
		},
	}
	cmd.Flags().BoolVarP(&syncRemote, "sync", "s", false, "Conectarse a los remotos y actualizar su estado")
	cmd.Flags().BoolVarP(&onlyLocal, "local", "l", false, "Solo los servers de esta máquina")
	cmd.Flags().BoolVarP(&onlyRemote, "remote", "r", false, "Solo los servers remotos")
	return cmd
}

// syncRemotes comprueba el estado real de los remotos y lo guarda en states.
// Primero intenta conectar con todos a la vez sin preguntar nada (claves,
// sesiones ya abiertas); luego, uno a uno, los que piden contraseña; y por
// último los consulta todos en paralelo. Devuelve true si cambió alguna
// versión del registro.
func syncRemotes(ctx context.Context, servers []*config.Server, states *config.States) bool {
	type target struct {
		s    *config.Server
		conn *ssh.Conn
		r    *liferay.Remote
		err  error
	}
	ts := make([]*target, len(servers))
	for i, s := range servers {
		conn, r := remoteOf(s)
		ts[i] = &target{s: s, conn: conn, r: r}
	}
	parallel := func(fn func(*target)) {
		var wg sync.WaitGroup
		for _, t := range ts {
			if t.err != nil {
				continue
			}
			wg.Add(1)
			go func(t *target) { defer wg.Done(); fn(t) }(t)
		}
		wg.Wait()
	}

	_ = ui.RunTask(fmt.Sprintf("Conectando con %d servers remotos", len(ts)), "Conexiones listas",
		func(ctx context.Context, _ func(string)) error {
			parallel(func(t *target) { t.err = t.conn.Connect(ctx, false, nil) })
			return nil
		})
	for _, t := range ts {
		if errors.Is(t.err, ssh.ErrNeedsLogin) && ui.IsTTY() {
			t.err = connect(ctx, t.s, t.conn)
		}
	}

	versions := map[*config.Server]string{}
	var mu sync.Mutex
	pending := 0
	for _, t := range ts {
		if t.err == nil {
			pending++
		}
	}
	if pending > 0 {
		_ = ui.RunTask("Comprobando su estado", "Estado actualizado",
			func(ctx context.Context, _ func(string)) error {
				parallel(func(t *target) {
					info, err := t.r.Inspect(ctx, t.s.Version == "")
					mu.Lock()
					defer mu.Unlock()
					if err != nil {
						t.err = err
						return
					}
					states.Seen(t.s.Name, stateName(info.State))
					if t.s.Version == "" && info.Version != "" {
						versions[t.s] = info.Version
					}
				})
				return nil
			})
	}

	for _, t := range ts {
		if t.err != nil {
			err := remoteErr(t.s, t.err)
			states.Failed(t.s.Name, err)
			ui.Warn(capitalize(err.Error()))
		}
	}
	for s, v := range versions {
		s.Version = v
	}
	return len(versions) > 0
}
