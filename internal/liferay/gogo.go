package liferay

// Gogo shell de Liferay: la consola OSGi por telnet que abre
// module.framework.properties.osgi.console. Por ahí se cambian los niveles de
// log en caliente llamando a Log4JUtil.setLevel, que es exactamente lo que
// hace el panel de control (Administración del servidor → Niveles de log).
//
// OJO: nunca se manda "exit" ni "shutdown". En la consola de Equinox, exit
// pregunta si parar el framework, es decir, apagaría Liferay. Para salir basta
// con cerrar la conexión: la sesión termina sola.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// GogoProperty es la propiedad de portal-ext.properties que abre la consola.
const GogoProperty = "module.framework.properties.osgi.console"

// DefaultGogoAddr es la de portal-developer.properties (la que incluyen los
// workspaces en configs/local). En producción no suele estar puesta.
const DefaultGogoAddr = "localhost:11311"

// ErrGogoDisabled: la propiedad está puesta pero vacía (consola desactivada).
var ErrGogoDisabled = errors.New("la Gogo shell está desactivada en su configuración")

// ErrGogoRefused: no hay nada escuchando en la dirección de la Gogo shell.
var ErrGogoRefused = errors.New("no hay ninguna Gogo shell escuchando")

// GogoConfig lee la propiedad de la consola de portal-ext.properties y
// portal-setup-wizard.properties (gana el último, como en Liferay). set dice
// si estaba en alguno; puesta y vacía = desactivada.
func (l *Layout) GogoConfig() (value string, set bool) {
	for _, f := range []string{"portal-ext.properties", "portal-setup-wizard.properties"} {
		if v, ok := ReadProperties(filepath.Join(l.Home, f))[GogoProperty]; ok {
			value, set = v, true
		}
	}
	return value, set
}

// GogoAddr convierte el valor de la propiedad ("11311", "localhost:11311",
// "0.0.0.0:11311"...) en la dirección a la que conectarse. "" = la de
// portal-developer.properties.
func GogoAddr(v string) (host string, port int, err error) {
	v = strings.TrimSpace(v)
	if v == "" {
		v = DefaultGogoAddr
	}
	host, p := "localhost", v
	if strings.Contains(v, ":") {
		if host, p, err = net.SplitHostPort(v); err != nil {
			return "", 0, fmt.Errorf("dirección de la Gogo shell no válida: %s", v)
		}
	}
	port, err = strconv.Atoi(p)
	if err != nil || port <= 0 || port > 65535 {
		return "", 0, fmt.Errorf("puerto de la Gogo shell no válido: %s", v)
	}
	switch host {
	case "", "*", "0.0.0.0", "::":
		host = "localhost" // escucha en todas: desde la propia máquina vale localhost
	}
	if strings.ContainsAny(host, " \t'\"/\\$`;") {
		return "", 0, fmt.Errorf("máquina de la Gogo shell no válida: %s", host)
	}
	return host, port, nil
}

// ---------------------------------------------------------------- telnet

const (
	tnSE   = 240
	tnSB   = 250
	tnWILL = 251
	tnWONT = 252
	tnDO   = 253
	tnDONT = 254
	tnIAC  = 255

	tnEcho  = 1
	tnSGA   = 3
	tnTType = 24
)

// La consola de Equinox no abre la sesión hasta que el cliente le dice su
// tipo de terminal (o pasan 60 s), así que hay que negociarlo.
var ttypeReply = append(append([]byte{tnIAC, tnSB, tnTType, 0}, "XTERM"...), tnIAC, tnSE)

// Gogo es una sesión abierta en la Gogo shell.
type Gogo struct {
	conn io.ReadWriteCloser
	wmu  sync.Mutex
	data chan []byte // texto que llega, ya sin comandos telnet
	rerr error       // por qué terminó la lectura (tras cerrar data)
	buf  []byte
	seq  int
	stop func() bool
}

// OpenGogo negocia telnet sobre conn y espera al prompt. Si ctx se cancela,
// la conexión se cierra (y cualquier espera termina con error).
func OpenGogo(ctx context.Context, conn io.ReadWriteCloser) (*Gogo, error) {
	g := &Gogo{conn: conn, data: make(chan []byte, 16)}
	g.stop = context.AfterFunc(ctx, func() { _ = conn.Close() })
	go g.readLoop()
	if _, err := g.wait(ctx, func(b []byte) (int, bool) { return 0, promptAt(b) }); err != nil {
		g.Close()
		return nil, err
	}
	g.buf = g.buf[:0]
	return g, nil
}

// Close cierra la conexión, que es la forma segura de salir (ver arriba).
func (g *Gogo) Close() error {
	g.stop()
	err := g.conn.Close()
	go func() {
		for range g.data { // que readLoop no se quede bloqueado enviando
		}
	}()
	return err
}

func (g *Gogo) write(b []byte) error {
	g.wmu.Lock()
	defer g.wmu.Unlock()
	_, err := g.conn.Write(b)
	return err
}

// readLoop lee de la conexión, contesta la negociación telnet y pasa el
// resto a data.
func (g *Gogo) readLoop() {
	defer close(g.data)
	const (
		normal = iota
		iac
		option
		sub
		subIAC
	)
	state, verb := normal, byte(0)
	var sb []byte
	raw := make([]byte, 4096)
	for {
		n, err := g.conn.Read(raw)
		var out []byte
		for _, b := range raw[:n] {
			switch state {
			case normal:
				if b == tnIAC {
					state = iac
				} else {
					out = append(out, b)
				}
			case iac:
				switch b {
				case tnIAC:
					out, state = append(out, b), normal
				case tnWILL, tnWONT, tnDO, tnDONT:
					verb, state = b, option
				case tnSB:
					sb, state = sb[:0], sub
				default:
					state = normal
				}
			case option:
				state = normal
				switch {
				case verb == tnDO && b == tnTType:
					_ = g.write([]byte{tnIAC, tnWILL, b})
				case verb == tnDO:
					_ = g.write([]byte{tnIAC, tnWONT, b}) // NAWS y demás: no
				case verb == tnWILL && (b == tnEcho || b == tnSGA):
					_ = g.write([]byte{tnIAC, tnDO, b})
				case verb == tnWILL:
					_ = g.write([]byte{tnIAC, tnDONT, b})
				}
			case sub:
				if b == tnIAC {
					state = subIAC
				} else {
					sb = append(sb, b)
				}
			case subIAC:
				if b == tnSE {
					state = normal
					if len(sb) >= 2 && sb[0] == tnTType && sb[1] == 1 { // SEND
						_ = g.write(ttypeReply)
					}
				} else {
					sb, state = append(sb, b), sub
				}
			}
		}
		if len(out) > 0 {
			g.data <- out
		}
		if err != nil {
			g.rerr = err
			return
		}
	}
}

// promptAt dice si el texto acaba en el prompt de la consola.
func promptAt(b []byte) bool {
	return bytes.HasSuffix(b, []byte("g! ")) || bytes.HasSuffix(b, []byte("osgi> "))
}

// wait acumula lo que llega hasta que done diga basta; devuelve lo que done
// marque como el principio de lo útil.
func (g *Gogo) wait(ctx context.Context, done func([]byte) (int, bool)) ([]byte, error) {
	for {
		if from, ok := done(g.buf); ok {
			return g.buf[from:], nil
		}
		select {
		case b, ok := <-g.data:
			if !ok {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				if g.rerr == nil || errors.Is(g.rerr, io.EOF) {
					return nil, errors.New("la Gogo shell ha cerrado la conexión")
				}
				return nil, g.rerr
			}
			g.buf = append(g.buf, b...)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// GogoError es un error que ha escrito la propia Gogo shell (gogo: ...).
type GogoError struct{ Msg string }

func (e *GogoError) Error() string { return e.Msg }

// Exec ejecuta una línea en la Gogo shell y devuelve su salida, sin el eco
// ni el prompt. La salida empieza tras un marcador que en el eco va partido
// ("LRAY""-N"), así que no se confunde con lo que se ha escrito.
func (g *Gogo) Exec(ctx context.Context, line string) ([]string, error) {
	if strings.ContainsAny(line, "\r\n") {
		return nil, errors.New("una orden de la Gogo shell no puede tener saltos de línea")
	}
	g.seq++
	marker := []byte(fmt.Sprintf("LRAY-%d\n", g.seq))
	g.buf = g.buf[:0]
	if err := g.write([]byte(fmt.Sprintf(`echo "LRAY""-%d"; %s`+"\r\n", g.seq, line))); err != nil {
		return nil, err
	}
	out, err := g.wait(ctx, func(b []byte) (int, bool) {
		b = bytes.ReplaceAll(b, []byte("\r"), nil)
		i := bytes.Index(b, marker)
		if i < 0 || !promptAt(b[i+len(marker):]) {
			return 0, false
		}
		return 0, true
	})
	if err != nil {
		return nil, err
	}
	text := string(bytes.ReplaceAll(out, []byte("\r"), nil))
	text = text[strings.Index(text, string(marker))+len(marker):]
	text = strings.TrimSuffix(strings.TrimSuffix(text, "g! "), "osgi> ")
	var lines []string
	for _, l := range strings.Split(text, "\n") {
		if l = strings.TrimRight(l, " \t"); l == "" {
			continue
		}
		if strings.HasPrefix(l, "gogo: ") {
			return lines, &GogoError{Msg: strings.TrimPrefix(l, "gogo: ")}
		}
		lines = append(lines, l)
	}
	return lines, nil
}

// ---------------------------------------------------------------- log4j

// log4jClasses: dónde vive Log4JUtil según la versión, de la más nueva a la
// más vieja. Todas tienen setLevel(String, String, boolean) y
// getCustomLogSettings(); getPriorities() falta en 7.3 y anteriores.
var log4jClasses = []string{
	"com.liferay.portal.kernel.log4j.Log4JUtil", // quarterly recientes
	"com.liferay.portal.log4j.Log4JUtil",        // 7.4 GA50+ / U50+
	"com.liferay.petra.log4j.Log4JUtil",         // 7.1 a 7.4 GA4
	"com.liferay.util.log4j.Log4JUtil",          // 7.0
}

// LogLevelNames son los niveles que acepta Liferay, de menos a más ruido.
var LogLevelNames = []string{"OFF", "FATAL", "ERROR", "WARN", "INFO", "DEBUG", "TRACE", "ALL"}

// NormalizeLogLevel pasa un nivel a mayúsculas y dice si existe.
func NormalizeLogLevel(s string) (string, bool) {
	s = strings.ToUpper(strings.TrimSpace(s))
	for _, l := range LogLevelNames {
		if s == l {
			return s, true
		}
	}
	return "", false
}

var loggerRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]*$`)

// ValidLoggerName dice si name es un nombre de logger que se puede mandar a
// la Gogo shell tal cual (paquete o clase: letras, números, _ . -).
func ValidLoggerName(name string) bool { return len(name) <= 300 && loggerRe.MatchString(name) }

// Log4J es el Log4JUtil del portal, visto desde la Gogo shell.
type Log4J struct {
	g     *Gogo
	Class string
}

// Log4J localiza Log4JUtil. Se carga desde el bundle 0, que ve el classpath
// del portal, donde está en todas las versiones.
func (g *Gogo) Log4J(ctx context.Context) (*Log4J, error) {
	var last error
	for _, c := range log4jClasses {
		_, err := g.Exec(ctx, fmt.Sprintf("(($.context bundle 0) loadclass %s) getname", c))
		if err == nil {
			return &Log4J{g: g, Class: c}, nil
		}
		var ge *GogoError
		if !errors.As(err, &ge) {
			return nil, err
		}
		last = err
	}
	return nil, fmt.Errorf("no encuentro Log4JUtil en este Liferay (%w)", last)
}

// call llama a un método estático de Log4JUtil. expr puede cerrar el
// paréntesis para encadenar otra llamada sobre el resultado: "x) tostring".
func (l *Log4J) call(ctx context.Context, expr string) ([]string, error) {
	if strings.Contains(expr, ")") {
		return l.g.Exec(ctx, fmt.Sprintf("((($.context bundle 0) loadclass %s) %s", l.Class, expr))
	}
	return l.g.Exec(ctx, fmt.Sprintf("(($.context bundle 0) loadclass %s) %s", l.Class, expr))
}

// SetLevel cambia el nivel de un logger en caliente y lo marca como cambiado
// a mano, igual que el panel de control. Se pierde al reiniciar.
func (l *Log4J) SetLevel(ctx context.Context, name, level string) error {
	if !ValidLoggerName(name) {
		return fmt.Errorf("«%s» no es un nombre de paquete o clase válido", name)
	}
	lv, ok := NormalizeLogLevel(level)
	if !ok {
		return fmt.Errorf("«%s» no es un nivel de log (%s)", level, strings.Join(LogLevelNames, ", "))
	}
	_, err := l.call(ctx, fmt.Sprintf("setlevel %s %s true", name, lv))
	return err
}

// Custom son los niveles cambiados a mano desde que arrancó (panel de
// control o lray).
func (l *Log4J) Custom(ctx context.Context) (map[string]string, error) {
	lines, err := l.call(ctx, "getcustomlogsettings) tostring")
	if err != nil {
		return nil, err
	}
	return parseJavaMap(lines), nil
}

// Priorities son todos los niveles configurados. ok = false si esta versión
// de Liferay no sabe darlos (7.3 y anteriores).
func (l *Log4J) Priorities(ctx context.Context) (levels map[string]string, ok bool, err error) {
	lines, err := l.call(ctx, "getpriorities) tostring")
	var ge *GogoError
	if errors.As(err, &ge) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return parseJavaMap(lines), true, nil
}

// parseJavaMap lee el toString de un Map de Java: {clave=valor, ...}. Se pide
// así porque la tabla que pinta Gogo pega la clave al valor si la clave pasa
// de 20 caracteres.
func parseJavaMap(lines []string) map[string]string {
	s := strings.TrimSpace(strings.Join(lines, ""))
	s = strings.TrimSuffix(strings.TrimPrefix(s, "{"), "}")
	m := map[string]string{}
	for _, kv := range strings.Split(s, ", ") {
		if k, v, ok := strings.Cut(kv, "="); ok && k != "" {
			m[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return m
}

// EffectiveLevel busca el nivel que se aplica a name: el suyo o el del
// paquete padre más cercano que tenga uno. from dice de cuál sale.
func EffectiveLevel(levels map[string]string, name string) (level, from string) {
	for n := name; n != ""; {
		if lv, ok := levels[n]; ok {
			return lv, n
		}
		i := strings.LastIndexByte(n, '.')
		if i < 0 {
			break
		}
		n = n[:i]
	}
	return "", ""
}
