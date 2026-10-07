package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/katarem/lray/internal/liferay"
	"github.com/katarem/lray/internal/logs"
	"github.com/katarem/lray/internal/logview"
	"github.com/katarem/lray/internal/ui"
)

func newLogs() *cobra.Command {
	var lines int
	var view logView
	cmd := &cobra.Command{
		Use:   "logs <nombre>",
		Short: "Engancha la terminal a los logs del server, coloreados por nivel",
		Long: `Engancha la terminal a los logs del server, coloreados por nivel.

Abre un visor a pantalla completa que sigue el log en vivo: los stack traces y
el JSON se agrupan con la línea a la que pertenecen y se pliegan y despliegan
con Enter. El JSON se indenta y colorea (--json).

Con --plain (o sin terminal) las líneas se imprimen seguidas, como un tail -f;
--collapse resume ahí cada stack trace o JSON en una línea.`,
		Example: `  lray server logs tienda
  lray server logs tienda -n 500 --level warn
  lray server logs tienda --only error,debug
  lray server logs tienda --plain --collapse`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeServers,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := view.parse(); err != nil {
				return err
			}
			_, s, l, err := loadServer(args[0])
			if err != nil {
				return err
			}
			if !l.HasBundle() {
				return liferay.ErrNoBundle
			}
			path := l.LogFile()
			return followLogs(cmd.Context(), s.Name, path, logs.StartOffset(path, lines), view)
		},
	}
	cmd.Flags().IntVarP(&lines, "lines", "n", 100, "Líneas anteriores que mostrar al engancharse")
	view.flags(cmd)
	return cmd
}

func newDev() *cobra.Command {
	var view logView
	cmd := &cobra.Command{
		Use:               "dev <nombre>",
		Short:             "Arranca el server y te enseña sus logs en directo (start + logs)",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeServers,
	}
	timeout := durationFlag(cmd, "stop-timeout", 60*time.Second, "Al salir, cuánto esperar antes de forzar la parada")
	view.flags(cmd)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if err := view.parse(); err != nil {
			return err
		}
		reg, s, l, err := loadServer(args[0])
		if err != nil {
			return err
		}
		res, err := startServer(reg, s, l)
		if err != nil {
			return err
		}
		path, offset := l.CatalinaOut(), res.offset
		if !res.started { // ya estaba encendido: enseña un poco de contexto
			path = l.LogFile()
			offset = logs.StartOffset(path, 50)
		}
		if err := followLogs(cmd.Context(), s.Name, path, offset, view); err != nil {
			return err
		}

		if _, alive := l.PID(); !alive || !ui.IsTTY() {
			return nil
		}
		stop, err := ui.Confirm(fmt.Sprintf("¿Apago también «%s»?", s.Name),
			"Si no, seguirá encendido en segundo plano.", false)
		if err != nil || !stop {
			ui.Say(ui.Happy, fmt.Sprintf("«%s» sigue encendido", s.Name),
				ui.MutedText("Vuelve a sus logs con ")+ui.Code("lray server logs "+s.Name))
			return nil
		}
		return stopServer(s, l, *timeout)
	}
	return cmd
}

// logView reúne las opciones de visualización que comparten logs y dev.
type logView struct {
	level, only, json string
	collapse, plain   bool

	filter logs.Filter
	mode   logs.JSONMode
}

func (v *logView) flags(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringVar(&v.level, "level", os.Getenv("LRAY_LOGS_LEVEL"), "Nivel mínimo: trace, debug, info, warn o error")
	f.StringVar(&v.only, "only", "", "Solo estos niveles, separados por comas (p. ej. error,debug)")
	f.StringVar(&v.json, "json", os.Getenv("LRAY_LOGS_JSON"), "Formato del JSON: pretty (indentado, por defecto), compact o raw")
	f.BoolVarP(&v.plain, "plain", "p", false, "Imprime las líneas seguidas en la terminal en vez de abrir el visor")
	f.BoolVarP(&v.collapse, "collapse", "c", false, "Con --plain, resume stack traces y JSON en una línea (implica --plain)")
}

func (v *logView) parse() error {
	var err error
	if v.filter, err = logs.ParseFilter(v.level, v.only); err != nil {
		return err
	}
	if v.mode, err = logs.ParseJSONMode(v.json); err != nil {
		return err
	}
	return nil
}

// interactive indica si se abre el visor: por defecto sí, salvo con --plain,
// --collapse o sin terminal (tuberías, scripts).
func (v *logView) interactive() bool {
	return !v.plain && !v.collapse && ui.IsTTY()
}

// followLogs abre el visor (o pinta el log seguido) hasta que el usuario salga
// con q o Ctrl+C.
func followLogs(parent context.Context, name, path string, offset int64, v logView) error {
	ctx, cancel := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer cancel()

	opts := logs.Options{Color: ui.IsTTY() && os.Getenv("NO_COLOR") == "", JSON: v.mode}
	if v.interactive() {
		return logview.Run(ctx, logview.Config{
			Title:  "logs de «" + name + "»",
			Detail: ui.ShortPath(path),
			Filter: v.filter,
			Render: opts,
		}, path, offset)
	}

	ui.Header("logs de «"+name+"»", ui.ShortPath(path)+"   Ctrl+C para salir")
	g := logs.NewGrouper()
	r := logs.NewRenderer(opts)
	w := bufio.NewWriterSize(os.Stdout, 64*1024)
	emit := func(e *logs.Entry) {
		if e == nil || !v.filter.Allows(e.Level) {
			return
		}
		b := r.Render(e)
		w.WriteString(b.Head)
		w.WriteByte('\n')
		if v.collapse && b.Collapsible() {
			w.WriteString(b.Summary)
			w.WriteByte('\n')
			return
		}
		for _, l := range b.Body {
			w.WriteString(l)
			w.WriteByte('\n')
		}
	}
	err := logs.Follow(ctx, path, offset, func(line string) { emit(g.Add(line)) }, func() {
		emit(g.Flush())
		_ = w.Flush()
	})
	emit(g.Flush())
	_ = w.Flush()
	fmt.Println()
	return err
}
