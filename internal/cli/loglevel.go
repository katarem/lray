package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/katarem/lray/internal/config"
	"github.com/katarem/lray/internal/debuglog"
	"github.com/katarem/lray/internal/liferay"
	"github.com/katarem/lray/internal/ui"
)

func newLogLevel() *cobra.Command {
	var gogo string
	var all bool
	cmd := &cobra.Command{
		Use:     "log-level <nombre> [paquete [nivel]]",
		Aliases: []string{"loglevel"},
		Short:   "Cambia en caliente el nivel de log de un paquete o clase en Liferay",
		Long: `Cambia en caliente el nivel de log de un paquete o clase dentro de Liferay,
igual que el panel de control (Administración del servidor → Niveles de log):
sin reiniciar, y se pierde al reiniciar. No tiene nada que ver con el filtro
--level de lray server logs: esto cambia lo que Liferay escribe.

  lray server log-level <nombre> com.liferay.foo DEBUG   lo pone en DEBUG
  lray server log-level <nombre> com.liferay.foo         dice en qué nivel está
  lray server log-level <nombre>                         los cambiados a mano
  lray server log-level <nombre> --all                   todos los configurados

Niveles: OFF, FATAL, ERROR, WARN, INFO, DEBUG, TRACE, ALL.

Va por la Gogo shell (module.framework.properties.osgi.console en
portal-ext.properties; en local viene puesta en localhost:11311 con
portal-developer.properties). En los remotos la conexión se hace desde la
propia máquina, por SSH. Con un clúster cambia solo el nodo de ese server.`,
		Args:              cobra.RangeArgs(1, 3),
		ValidArgsFunction: completeLogLevel,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, s, err := findServer(args[0])
			if err != nil {
				return err
			}
			var name, level string
			if len(args) > 1 {
				name = args[1]
				if !liferay.ValidLoggerName(name) {
					return fmt.Errorf("«%s» no es un nombre de paquete o clase válido", name)
				}
			}
			if len(args) > 2 {
				lv, ok := liferay.NormalizeLogLevel(args[2])
				if !ok {
					return fmt.Errorf("«%s» no es un nivel de log; usa uno de estos: %s", args[2], strings.Join(liferay.LogLevelNames, ", "))
				}
				level = lv
			}
			if all && name != "" {
				return errors.New("--all es para listar: no lo combines con un paquete")
			}
			return runLogLevel(cmd.Context(), s, gogo, name, level, all)
		},
	}
	cmd.Flags().StringVar(&gogo, "gogo", "", "Dónde escucha la Gogo shell ([máquina:]puerto); por defecto, la de portal-ext.properties o localhost:11311")
	cmd.Flags().BoolVar(&all, "all", false, "Listar todos los niveles configurados, no solo los cambiados a mano")
	return cmd
}

// completeLogLevel: el server, nada para el paquete y los niveles.
func completeLogLevel(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	switch len(args) {
	case 0:
		return completeServers(cmd, args, toComplete)
	case 2:
		return liferay.LogLevelNames, cobra.ShellCompDirectiveNoFileComp
	}
	return nil, cobra.ShellCompDirectiveNoFileComp
}

// gogoTarget es a dónde y cómo conectarse con la Gogo shell de un server.
type gogoTarget struct {
	host string
	port int
	set  bool // la dirección viene de su configuración (o de --gogo)
	dial func(ctx context.Context) (io.ReadWriteCloser, error)
	off  bool // desactivada en su configuración
	l    *liferay.Layout
}

func (t gogoTarget) addr() string { return net.JoinHostPort(t.host, strconv.Itoa(t.port)) }

// resolveGogo averigua la dirección de la Gogo shell (sin conectarse aún a
// ella) y prepara cómo llegar: directo en local, desde la máquina en remoto.
func resolveGogo(ctx context.Context, s *config.Server, flag string) (gogoTarget, error) {
	var t gogoTarget
	value, set := flag, flag != ""
	var dialRemote func(ctx context.Context, host string, port int) (io.ReadWriteCloser, error)
	if s.IsRemote() {
		conn, r := remoteOf(s)
		if err := connect(ctx, s, conn); err != nil {
			return t, err
		}
		if !set {
			var err error
			if value, set, err = r.GogoConfig(ctx); err != nil {
				return t, remoteErr(s, err)
			}
		}
		dialRemote = func(ctx context.Context, host string, port int) (io.ReadWriteCloser, error) {
			rw, err := r.Gogo(ctx, host, port)
			return rw, remoteErr(s, err)
		}
	} else {
		l, err := liferay.Resolve(s.Path)
		if err != nil {
			return t, fmt.Errorf("la ruta de «%s» (%s) ya no es válida: %w", s.Name, ui.ShortPath(s.Path), err)
		}
		t.l = l
		if !set {
			value, set = l.GogoConfig()
		}
	}
	if set && strings.TrimSpace(value) == "" {
		t.off = true
		return t, nil
	}
	host, port, err := liferay.GogoAddr(value)
	if err != nil {
		return t, err
	}
	t.host, t.port, t.set = host, port, set
	if dialRemote != nil {
		t.dial = func(ctx context.Context) (io.ReadWriteCloser, error) { return dialRemote(ctx, host, port) }
	} else {
		t.dial = func(ctx context.Context) (io.ReadWriteCloser, error) {
			var d net.Dialer
			c, err := d.DialContext(ctx, "tcp", t.addr())
			if err != nil {
				return nil, fmt.Errorf("%w (%v)", liferay.ErrGogoRefused, err)
			}
			return c, nil
		}
	}
	return t, nil
}

// gogoErr explica por qué no se ha podido usar la Gogo shell.
func gogoErr(s *config.Server, t gogoTarget, err error) error {
	enable := fmt.Sprintf("añade %s a su portal-ext.properties y reinícialo (o dime dónde escucha con --gogo)",
		ui.Code(liferay.GogoProperty+"="+liferay.DefaultGogoAddr))
	switch {
	case t.off:
		return fmt.Errorf("«%s» tiene la Gogo shell desactivada (%s vacía en su configuración); para usarla, %s",
			s.Name, liferay.GogoProperty, enable)
	case errors.Is(err, liferay.ErrGogoRefused) && t.l != nil && t.l.State() == liferay.Stopped:
		return fmt.Errorf("nada escucha en %s: «%s» parece apagado; arráncalo con %s", t.addr(), s.Name, ui.Code("lray server start "+s.Name))
	case errors.Is(err, liferay.ErrGogoRefused) && !t.set:
		return fmt.Errorf("nada escucha en %s, donde suele estar la Gogo shell de «%s». Si Liferay está arrancado es que no la tiene activada: %s",
			t.addr(), s.Name, enable)
	case errors.Is(err, liferay.ErrGogoRefused):
		return fmt.Errorf("nada escucha en %s, donde está configurada la Gogo shell de «%s». ¿Está arrancado Liferay?", t.addr(), s.Name)
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("la Gogo shell de «%s» (%s) no contesta", s.Name, t.addr())
	}
	return err
}

func runLogLevel(ctx context.Context, s *config.Server, flag, name, level string, all bool) error {
	if ctx == nil {
		ctx = context.Background()
	}
	t, err := resolveGogo(ctx, s, flag)
	if err != nil {
		return err
	}
	if t.off {
		return gogoErr(s, t, nil)
	}

	var (
		class          string
		custom, before map[string]string
		after          map[string]string
		havePrios      bool
	)
	title, done := fmt.Sprintf("Mirando los niveles de log de «%s»", s.Name), "Niveles leídos"
	if level != "" {
		title, done = fmt.Sprintf("Poniendo %s en %s en «%s»", name, level, s.Name), "Nivel cambiado"
	}
	err = ui.RunTask(title, done, func(tctx context.Context, update func(string)) error {
		tctx, cancel := context.WithTimeout(tctx, 30*time.Second)
		defer cancel()
		update("conectando con la Gogo shell en " + t.addr())
		rw, err := t.dial(tctx)
		if err != nil {
			return err
		}
		g, err := liferay.OpenGogo(tctx, rw)
		if err != nil {
			return err
		}
		defer g.Close()
		l, err := g.Log4J(tctx)
		if err != nil {
			return err
		}
		class = l.Class
		if before, havePrios, err = l.Priorities(tctx); err != nil {
			return err
		}
		if level != "" {
			update("cambiando el nivel")
			if err := l.SetLevel(tctx, name, level); err != nil {
				return err
			}
			if havePrios {
				if after, _, err = l.Priorities(tctx); err != nil {
					return err
				}
			}
		}
		custom, err = l.Custom(tctx)
		return err
	})
	if err != nil {
		return gogoErr(s, t, err)
	}
	debuglog.Printf("log-level %s: Gogo en %s, %s", s.Name, t.addr(), class)

	switch {
	case level != "":
		return showSetLevel(s, name, level, before, after, havePrios)
	case name != "":
		showLevel(s, name, before, custom, havePrios)
	case all:
		if !havePrios {
			return errors.New("este Liferay (7.3 o anterior) no deja ver todos sus niveles desde la Gogo shell; sin --all te enseño los cambiados a mano")
		}
		printLevels(before, custom)
	default:
		if len(custom) == 0 {
			ui.Say(ui.Happy, fmt.Sprintf("Nadie ha cambiado niveles a mano en «%s» desde que arrancó", s.Name),
				"Cambia uno con "+ui.Code(fmt.Sprintf("lray server log-level %s <paquete> DEBUG", s.Name)),
				ui.MutedText("Todos los configurados: ")+ui.Code(fmt.Sprintf("lray server log-level %s --all", s.Name)))
			return nil
		}
		ui.Say(ui.Happy, fmt.Sprintf("Niveles cambiados a mano en «%s»", s.Name),
			append(levelLines(custom), "", ui.MutedText("Se pierden al reiniciar."))...)
	}
	return nil
}

// levelDesc describe el nivel que se aplica a name: "INFO" o "INFO (de
// com.liferay)" si lo hereda.
func levelDesc(levels map[string]string, name string) string {
	lv, from := liferay.EffectiveLevel(levels, name)
	switch {
	case lv == "":
		return "el del logger raíz"
	case from != name:
		return lv + ui.MutedText(" (lo hereda de "+from+")")
	}
	return lv
}

func showSetLevel(s *config.Server, name, level string, before, after map[string]string, havePrios bool) error {
	if havePrios {
		if lv, ok := after[name]; ok && lv != level {
			return fmt.Errorf("he pedido %s para %s pero Liferay dice que está en %s", level, name, lv)
		}
	}
	lines := []string{}
	prev, _ := liferay.EffectiveLevel(before, name)
	if havePrios {
		lines = append(lines, ui.MutedText("Antes  ")+levelDesc(before, name))
	}
	lines = append(lines, ui.MutedText("Como en el panel de control: se pierde al reiniciar."))
	if s.IsRemote() && (level == "DEBUG" || level == "TRACE" || level == "ALL") {
		lines = append(lines, ui.WarnText("Ojo: en "+level+" puede escribir mucho en un server compartido; déjalo como estaba al acabar."))
	}
	if prev != "" && prev != level {
		lines = append(lines, "", ui.MutedText("Para dejarlo como estaba: ")+ui.Code(fmt.Sprintf("lray server log-level %s %s %s", s.Name, name, prev)))
	}
	ui.Say(ui.Happy, fmt.Sprintf("%s ahora está en %s en «%s»", name, level, s.Name), lines...)
	return nil
}

func showLevel(s *config.Server, name string, levels, custom map[string]string, havePrios bool) {
	if !havePrios {
		if lv, ok := custom[name]; ok {
			ui.Say(ui.Happy, fmt.Sprintf("%s está en %s en «%s»", name, lv, s.Name), ui.MutedText("Cambiado a mano; se pierde al reiniciar."))
			return
		}
		ui.Say(ui.Thinking, fmt.Sprintf("%s no se ha cambiado a mano en «%s»", name, s.Name),
			ui.MutedText("Este Liferay (7.3 o anterior) no me deja ver el resto de niveles desde la Gogo shell."))
		return
	}
	lines := []string{}
	if _, ok := custom[name]; ok {
		lines = append(lines, ui.MutedText("Cambiado a mano; se pierde al reiniciar."))
	}
	ui.Say(ui.Happy, fmt.Sprintf("%s está en %s en «%s»", name, levelDesc(levels, name), s.Name), lines...)
}

// levelLines pinta un mapa de niveles en columnas, por nombre.
func levelLines(levels map[string]string) []string {
	names := make([]string, 0, len(levels))
	width := 0
	for n := range levels {
		names = append(names, n)
		width = max(width, len(n))
	}
	sort.Strings(names)
	lines := make([]string, 0, len(names))
	for _, n := range names {
		lines = append(lines, fmt.Sprintf("%-*s  %s", width, n, levels[n]))
	}
	return lines
}

// printLevels vuelca todos los niveles, uno por línea (se puede pasar a grep).
func printLevels(levels, custom map[string]string) {
	for _, l := range levelLines(levels) {
		if n := strings.Fields(l)[0]; custom[n] != "" {
			l += ui.MutedText("  (a mano)")
		}
		fmt.Println(l)
	}
}
