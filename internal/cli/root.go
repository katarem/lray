// Package cli define los comandos de lray.
package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

	"github.com/katarem/lray/internal/config"
	"github.com/katarem/lray/internal/liferay"
	"github.com/katarem/lray/internal/ui"
)

// Execute ejecuta lray y devuelve el código de salida.
func Execute(version string) int {
	root := newRoot()
	root.Version = version
	root.SetVersionTemplate("lray {{.Version}}\n")
	root.Flags().BoolP("version", "v", false, "Muestra la versión")
	if err := root.ExecuteContext(context.Background()); err != nil {
		if errors.Is(err, huh.ErrUserAborted) || errors.Is(err, ui.ErrInterrupted) {
			ui.Say(ui.Sleepy, "Vale, lo dejamos aquí.")
			return 130
		}
		ui.Say(ui.Sad, "No he podido hacerlo", capitalize(err.Error()))
		return 1
	}
	return 0
}

func newRoot() *cobra.Command {
	root := &cobra.Command{
		Use:           "lray",
		Short:         "Gestiona tus entornos Liferay desde la terminal",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ui.Say(ui.Happy, "¡Hola! Soy Faro.",
				"Te ayudo a crear, arrancar y desplegar tus Liferay.",
				"Empieza con "+ui.Code("lray server list")+" o "+ui.Code("lray server init <nombre>")+".")
			return cmd.Help()
		},
	}
	root.PersistentFlags().BoolP("help", "h", false, "Muestra la ayuda")
	cobra.AddTemplateFunc("es", func(s string) string { return strings.Replace(s, "[flags]", "[opciones]", 1) })
	root.SetUsageTemplate(usageES)
	root.SetHelpCommand(&cobra.Command{Hidden: true, Use: "no-help"})
	root.CompletionOptions.HiddenDefaultCmd = true
	root.AddCommand(
		newServer(),
		newCompletion(root),
	)
	return root
}

const usageES = `Uso:{{if .Runnable}}
  {{es .UseLine}}{{end}}{{if .HasAvailableSubCommands}}
  {{.CommandPath}} <comando>{{end}}{{if gt (len .Aliases) 0}}

Alias:
  {{.NameAndAliases}}{{end}}{{if .HasExample}}

Ejemplos:
{{.Example}}{{end}}{{if .HasAvailableSubCommands}}

Comandos:{{range .Commands}}{{if .IsAvailableCommand}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

Opciones:
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}

Opciones globales:
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableSubCommands}}

Usa "{{.CommandPath}} <comando> --help" para ver los detalles de cada comando.{{end}}
`

func newCompletion(root *cobra.Command) *cobra.Command {
	return &cobra.Command{
		Use:       "completion <bash|zsh|fish|powershell>",
		Short:     "Genera el autocompletado (incluye los nombres de tus servers)",
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"bash", "zsh", "fish", "powershell"},
		RunE: func(_ *cobra.Command, args []string) error {
			switch args[0] {
			case "bash":
				return root.GenBashCompletionV2(os.Stdout, true)
			case "zsh":
				return root.GenZshCompletion(os.Stdout)
			case "fish":
				return root.GenFishCompletion(os.Stdout, true)
			case "powershell":
				return root.GenPowerShellCompletionWithDesc(os.Stdout)
			}
			return fmt.Errorf("shell no soportada: %s", args[0])
		},
	}
}

// ------------------------------------------------------------- utilidades

// loadServer busca un server registrado y resuelve su layout.
func loadServer(name string) (*config.Registry, *config.Server, *liferay.Layout, error) {
	reg, err := config.Load()
	if err != nil {
		return nil, nil, nil, err
	}
	s, ok := reg.Get(name)
	if !ok {
		return nil, nil, nil, fmt.Errorf("no tengo ningún server llamado «%s». Revisa los nombres con %s", name, ui.Code("lray server list"))
	}
	l, err := liferay.Resolve(s.Path)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("la ruta de «%s» (%s) ya no es válida: %w", name, ui.ShortPath(s.Path), err)
	}
	if s.Version == "" {
		if v := liferay.DetectVersion(l); v != "" {
			s.Version = v
			_ = reg.Save()
		}
	}
	return reg, s, l, nil
}

// completeServers autocompleta el primer argumento con los servers registrados.
func completeServers(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	reg, err := config.Load()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	out := make([]string, 0, len(reg.Servers))
	for _, s := range reg.Servers {
		out = append(out, s.Name+"\t"+orDash(s.Version))
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// whoUsesPort busca qué server registrado y encendido usa ese puerto.
func whoUsesPort(reg *config.Registry, except string, port int) string {
	for _, s := range reg.Servers {
		if s.Name == except {
			continue
		}
		l, err := liferay.Resolve(s.Path)
		if err != nil || l.Port() != port {
			continue
		}
		if _, alive := l.PID(); alive {
			return s.Name
		}
	}
	return ""
}

// printGradleFailure enseña lo importante de una salida de Gradle fallida.
func printGradleFailure(lines []string) {
	var picked []string
	for _, l := range lines {
		if strings.Contains(l, ": error:") || strings.HasPrefix(l, "e: ") {
			picked = append(picked, ui.BadText(l))
			if len(picked) >= 20 {
				break
			}
		}
	}
	start := -1
	for i, l := range lines {
		if strings.HasPrefix(l, "* What went wrong") {
			start = i
			break
		}
	}
	if start >= 0 {
		for _, l := range lines[start:] {
			if strings.HasPrefix(l, "* Try:") {
				break
			}
			picked = append(picked, l)
		}
	}
	if len(picked) == 0 {
		from := len(lines) - 25
		if from < 0 {
			from = 0
		}
		picked = lines[from:]
	}
	fmt.Println()
	for _, l := range picked {
		fmt.Println("  " + l)
	}
	fmt.Println()
}

// runInitBundle descarga y prepara el bundle del workspace.
func runInitBundle(ws, javaHome string) error {
	var tail []string
	err := ui.RunTask("Descargando y preparando el bundle (initBundle)", "Bundle listo",
		func(ctx context.Context, update func(string)) error {
			lines, err := liferay.RunGradle(ctx, ws, ws, javaHome, []string{"initBundle"}, func(line string) {
				if t := strings.TrimSpace(line); t != "" {
					update(t)
				}
			})
			tail = lines
			return err
		})
	if err != nil && !errors.Is(err, ui.ErrInterrupted) {
		printGradleFailure(tail)
		return errors.New("initBundle ha fallado; arriba tienes el motivo")
	}
	return err
}

func stateLabel(s liferay.State) string {
	switch s {
	case liferay.Running:
		return ui.OkText("● encendido")
	case liferay.Starting:
		return ui.WarnText("◐ arrancando")
	}
	return ui.MutedText("○ apagado")
}

func capitalize(s string) string {
	r, size := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError {
		return s
	}
	return string(unicode.ToUpper(r)) + s[size:]
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func url(port int) string { return fmt.Sprintf("http://localhost:%d", port) }

func durationFlag(cmd *cobra.Command, name string, def time.Duration, usage string) *time.Duration {
	return cmd.Flags().Duration(name, def, usage)
}
