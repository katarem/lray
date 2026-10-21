package cli

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/katarem/lray/internal/config"
	"github.com/katarem/lray/internal/debuglog"
	"github.com/katarem/lray/internal/shellcomp"
	"github.com/katarem/lray/internal/ui"
	"github.com/katarem/lray/internal/update"
)

// newDebug agrupa lo que sirve para investigar fallos de lray.
func newDebug() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "debug",
		Short: "Investiga fallos de lray: su log y un informe para mandarlo",
		Long: `lray apunta siempre lo que hace (cada comando, sus errores y cada llamada a
ssh con lo que contestó) en un log de depuración. Si algo falla, míralo con
lray debug log o genera un informe con lray debug report y mándalo.

Con --debug (o LRAY_DEBUG=1) en cualquier comando, además lo ves en pantalla.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(newDebugLog(), newDebugReport())
	return cmd
}

func newDebugLog() *cobra.Command {
	var lines int
	var path bool
	cmd := &cobra.Command{
		Use:   "log",
		Short: "Enseña lo último del log de depuración",
		Example: `  lray debug log
  lray debug log -n 200
  lray debug log --path`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if path {
				fmt.Println(debuglog.Path())
				return nil
			}
			got, err := debuglog.Tail(lines)
			if err != nil {
				return fmt.Errorf("no puedo leer el log de depuración: %w", err)
			}
			if len(got) == 0 {
				ui.Say(ui.Sleepy, "Todavía no hay nada apuntado", ui.MutedText(debuglog.Path()))
				return nil
			}
			for _, l := range got {
				fmt.Println(l)
			}
			return nil
		},
	}
	cmd.Flags().IntVarP(&lines, "lines", "n", 50, "Cuántas líneas enseñar")
	cmd.Flags().BoolVar(&path, "path", false, "Solo decir dónde está el fichero")
	return cmd
}

func newDebugReport() *cobra.Command {
	var out string
	var lines int
	cmd := &cobra.Command{
		Use:   "report",
		Short: "Genera un informe para mandar si lray falla",
		Long: `Reúne en un fichero de texto lo necesario para investigar un fallo: versión
de lray y del sistema, versión de ssh, tus servers registrados y su último
estado, las sesiones SSH abiertas, el autocompletado y lo último del log de
depuración.

Lleva rutas y nombres de máquinas, pero nunca contraseñas (lray no las ve).
Revísalo antes de mandarlo.`,
		Example: `  lray debug report
  lray debug report -o informe.txt
  lray debug report -o -    # por la salida estándar`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			report := buildReport(cmd.Root().Version, lines)
			if out == "-" {
				_, err := os.Stdout.Write(report)
				return err
			}
			if out == "" {
				out = "lray-report-" + time.Now().Format("20060102-150405") + ".txt"
			}
			if err := os.WriteFile(out, report, 0o600); err != nil {
				return fmt.Errorf("no puedo escribir el informe: %w", err)
			}
			abs, _ := filepath.Abs(out)
			ui.Say(ui.Happy, "Informe listo",
				ui.Code(ui.ShortPath(abs)),
				"",
				"Revísalo antes de mandarlo: lleva rutas y nombres de máquinas, nunca contraseñas.",
				"Mándalo abriendo un issue en "+ui.Code("https://github.com/"+update.Repo()+"/issues/new")+" y adjúntalo.")
			return nil
		},
	}
	cmd.Flags().StringVarP(&out, "output", "o", "", "Dónde guardarlo (- para la salida estándar; por defecto lray-report-<fecha>.txt aquí)")
	cmd.Flags().IntVarP(&lines, "lines", "n", 400, "Cuántas líneas del log de depuración incluir")
	return cmd
}

// buildReport monta el informe. Lo que no consiga leer lo apunta en su sitio
// en vez de fallar: un informe a medias sigue sirviendo.
func buildReport(version string, lines int) []byte {
	var b bytes.Buffer
	section := func(title string) { fmt.Fprintf(&b, "\n== %s\n", title) }

	fmt.Fprintf(&b, "Informe de lray · %s\n", time.Now().Format(time.RFC3339))
	section("Sistema")
	fmt.Fprintf(&b, "lray      %s\n", orDash(version))
	fmt.Fprintf(&b, "sistema   %s/%s (%s)\n", runtime.GOOS, runtime.GOARCH, runtime.Version())
	if exe, err := os.Executable(); err == nil {
		fmt.Fprintf(&b, "binario   %s\n", exe)
	}
	fmt.Fprintf(&b, "shell     %s\n", orDash(os.Getenv("SHELL")))
	fmt.Fprintf(&b, "TERM      %s\n", orDash(os.Getenv("TERM")))
	fmt.Fprintf(&b, "terminal  %v\n", ui.IsTTY())
	fmt.Fprintf(&b, "ssh       %s\n", sshVersion())
	agent := "no"
	if os.Getenv("SSH_AUTH_SOCK") != "" {
		agent = "sí"
	}
	fmt.Fprintf(&b, "agente    %s\n", agent)

	section("Variables")
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "LRAY_") || strings.HasPrefix(kv, "NO_COLOR=") {
			env = append(env, kv)
		}
	}
	sort.Strings(env)
	if len(env) == 0 {
		b.WriteString("(ninguna)\n")
	}
	for _, kv := range env {
		b.WriteString(kv + "\n")
	}

	section("Configuración")
	dir, err := config.Dir()
	if err != nil {
		fmt.Fprintf(&b, "(no sé dónde está: %v)\n", err)
	}
	for _, name := range []string{"servers.json", "state.json"} {
		p := filepath.Join(dir, name)
		fmt.Fprintf(&b, "--- %s\n", p)
		data, err := os.ReadFile(p)
		if err != nil {
			fmt.Fprintf(&b, "(%v)\n", err)
			continue
		}
		b.Write(bytes.TrimSpace(data))
		b.WriteString("\n")
	}

	section("Sesiones SSH")
	if reg, err := config.Load(); err != nil {
		fmt.Fprintf(&b, "(%v)\n", err)
	} else {
		found := false
		for i := range reg.Servers {
			s := &reg.Servers[i]
			if !s.IsRemote() {
				continue
			}
			found = true
			conn, _ := remoteOf(s)
			state := "sin sesión abierta"
			if conn.Connected() {
				state = "sesión abierta"
			}
			fmt.Fprintf(&b, "%s (%s): %s\n", s.Name, s.Host, state)
		}
		if !found {
			b.WriteString("(no hay servers remotos)\n")
		}
	}

	section("Autocompletado")
	b.WriteString(shellcomp.Status() + "\n")

	section(fmt.Sprintf("Log de depuración (%s, últimas %d líneas)", debuglog.Path(), lines))
	got, err := debuglog.Tail(lines)
	if err != nil {
		fmt.Fprintf(&b, "(%v)\n", err)
	}
	for _, l := range got {
		b.WriteString(l + "\n")
	}
	return b.Bytes()
}

func sshVersion() string {
	var out bytes.Buffer
	cmd := exec.Command("ssh", "-V")
	cmd.Stdout, cmd.Stderr = io.Discard, &out
	if err := cmd.Run(); err != nil && out.Len() == 0 {
		return "no encontrado (" + err.Error() + ")"
	}
	return strings.TrimSpace(out.String())
}
