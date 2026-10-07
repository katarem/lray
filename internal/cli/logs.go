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
	"github.com/katarem/lray/internal/ui"
)

func newLogs() *cobra.Command {
	var lines int
	var level string
	cmd := &cobra.Command{
		Use:   "logs <nombre>",
		Short: "Engancha la terminal a los logs del server, coloreados por nivel",
		Example: `  lray logs tienda
  lray logs tienda -n 500 --level warn`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeServers,
		RunE: func(cmd *cobra.Command, args []string) error {
			min, err := logs.ParseLevel(level)
			if err != nil {
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
			return followLogs(cmd.Context(), s.Name, path, logs.StartOffset(path, lines), min)
		},
	}
	cmd.Flags().IntVarP(&lines, "lines", "n", 100, "Líneas anteriores que mostrar al engancharse")
	cmd.Flags().StringVar(&level, "level", "", "Nivel mínimo: trace, debug, info, warn o error")
	return cmd
}

func newDev() *cobra.Command {
	var level string
	cmd := &cobra.Command{
		Use:               "dev <nombre>",
		Short:             "Arranca el server y te enseña sus logs en directo (start + logs)",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeServers,
	}
	timeout := durationFlag(cmd, "stop-timeout", 60*time.Second, "Al salir, cuánto esperar antes de forzar la parada")
	cmd.Flags().StringVar(&level, "level", "", "Nivel mínimo: trace, debug, info, warn o error")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		min, err := logs.ParseLevel(level)
		if err != nil {
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
		if err := followLogs(cmd.Context(), s.Name, path, offset, min); err != nil {
			return err
		}

		if _, alive := l.PID(); !alive || !ui.IsTTY() {
			return nil
		}
		stop, err := ui.Confirm(fmt.Sprintf("¿Apago también «%s»?", s.Name),
			"Si no, seguirá encendido en segundo plano.", false)
		if err != nil || !stop {
			ui.Say(ui.Happy, fmt.Sprintf("«%s» sigue encendido", s.Name),
				ui.MutedText("Vuelve a sus logs con ")+ui.Code("lray logs "+s.Name))
			return nil
		}
		return stopServer(s, l, *timeout)
	}
	return cmd
}

// followLogs pinta el log en vivo hasta que el usuario pulse Ctrl+C.
func followLogs(parent context.Context, name, path string, offset int64, min logs.Level) error {
	ctx, cancel := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer cancel()

	ui.Header("logs de «"+name+"»", ui.ShortPath(path)+"   Ctrl+C para salir")
	color := ui.IsTTY() && os.Getenv("NO_COLOR") == ""
	h := logs.NewHighlighter(color, min)
	w := bufio.NewWriterSize(os.Stdout, 64*1024)
	err := logs.Follow(ctx, path, offset, func(line string) {
		if out, ok := h.Format(line); ok {
			w.WriteString(out)
			w.WriteByte('\n')
		}
	}, func() { _ = w.Flush() })
	_ = w.Flush()
	fmt.Println()
	return err
}
