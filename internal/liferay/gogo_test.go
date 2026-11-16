package liferay

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeGogo imita la consola telnet de Equinox con la Gogo shell de Liferay:
// no da el prompt hasta que el cliente dice su tipo de terminal, repite lo
// que se escribe (eco) y entiende las pocas órdenes que manda lray.
type fakeGogo struct {
	t       *testing.T
	ln      net.Listener
	classes map[string]bool // clases que existen
	noPrios bool            // como 7.3: sin getPriorities

	mu     sync.Mutex
	prios  map[string]string
	custom map[string]string
	lines  []string
	closed chan struct{}
}

func newFakeGogo(t *testing.T, class string) *fakeGogo {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeGogo{t: t, ln: ln, classes: map[string]bool{class: true},
		prios:  map[string]string{"com.liferay.portal": "INFO", "com.liferay": "WARN"},
		custom: map[string]string{}, closed: make(chan struct{})}
	t.Cleanup(func() { ln.Close() })
	go f.serve()
	return f
}

func (f *fakeGogo) addr() string { return f.ln.Addr().String() }

var (
	reMarker = regexp.MustCompile(`^echo "LRAY""-(\d+)"; (.*)$`)
	reCall   = regexp.MustCompile(`^\(?\(\(\$\.context bundle 0\) loadclass (\S+)\) (\w+)(?: (\S+) (\S+) (\S+))?(\) tostring)?$`)
)

func (f *fakeGogo) serve() {
	c, err := f.ln.Accept()
	if err != nil {
		return
	}
	defer close(f.closed)
	defer c.Close()
	c.Write([]byte{tnIAC, tnWILL, tnEcho, tnIAC, tnWILL, tnSGA, tnIAC, tnDO, 31, tnIAC, tnDO, tnTType})
	r := bufio.NewReader(c)
	// negociación: espera WILL TTYPE, pide el tipo y espera la respuesta
	var seen []byte
	for !bytes.Contains(seen, []byte{tnIAC, tnWILL, tnTType}) {
		b, err := r.ReadByte()
		if err != nil {
			return
		}
		seen = append(seen, b)
	}
	c.Write([]byte{tnIAC, tnSB, tnTType, 1, tnIAC, tnSE})
	seen = seen[:0]
	for !bytes.Contains(seen, append([]byte("XTERM"), tnIAC, tnSE)) {
		b, err := r.ReadByte()
		if err != nil {
			return
		}
		seen = append(seen, b)
	}
	c.Write([]byte("____________________________\r\nWelcome to Apache Felix Gogo\r\n\r\ng! "))
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			continue
		}
		f.mu.Lock()
		f.lines = append(f.lines, line)
		f.mu.Unlock()
		if strings.HasPrefix(line, "exit") || strings.HasPrefix(line, "shutdown") || strings.HasPrefix(line, "stop 0") {
			f.t.Errorf("lray ha mandado %q: eso para Liferay", line)
		}
		c.Write([]byte(line + "\r\n")) // eco
		c.Write([]byte(f.eval(line) + "g! "))
	}
}

func (f *fakeGogo) eval(line string) string {
	m := reMarker.FindStringSubmatch(line)
	if m == nil {
		return "gogo: CommandNotFoundException: Command not found: " + line + "\r\n"
	}
	out := "LRAY-" + m[1] + "\r\n"
	f.mu.Lock()
	defer f.mu.Unlock()
	cl := reCall.FindStringSubmatch(m[2])
	if cl == nil {
		return out + "gogo: SyntaxError\r\n"
	}
	if !f.classes[cl[1]] {
		return out + "gogo: ClassNotFoundException: " + cl[1] + "\r\n"
	}
	printMap := func(m map[string]string) string {
		if cl[6] == "" { // la tabla de Gogo: no se puede leer bien
			return "tabla sin tostring\r\n"
		}
		var kv []string
		for k, v := range m {
			kv = append(kv, k+"="+v)
		}
		return "{" + strings.Join(kv, ", ") + "}\r\n"
	}
	switch cl[2] {
	case "getname":
		return out + cl[1] + "\r\n"
	case "setlevel":
		f.prios[cl[3]] = cl[4]
		if cl[5] == "true" {
			f.custom[cl[3]] = cl[4]
		}
		return out
	case "getcustomlogsettings":
		return out + printMap(f.custom)
	case "getpriorities":
		if f.noPrios {
			return out + "gogo: IllegalArgumentException: Cannot coerce getpriorities() to any of []\r\n"
		}
		return out + printMap(f.prios)
	}
	return out + "gogo: IllegalArgumentException: " + cl[2] + "\r\n"
}

func dialGogo(t *testing.T, addr string) (*Gogo, context.Context) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	g, err := OpenGogo(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { g.Close() })
	return g, ctx
}

func TestGogoSetLevel(t *testing.T) {
	for _, class := range log4jClasses {
		t.Run(class, func(t *testing.T) {
			f := newFakeGogo(t, class)
			g, ctx := dialGogo(t, f.addr())
			l, err := g.Log4J(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if l.Class != class {
				t.Fatalf("clase = %s, quería %s", l.Class, class)
			}
			// más de 20 caracteres: la tabla de Gogo lo pegaría al valor
			const name = "com.example.foo.con.un.nombre.largo"
			if err := l.SetLevel(ctx, name, "debug"); err != nil {
				t.Fatal(err)
			}
			custom, err := l.Custom(ctx)
			if err != nil || custom[name] != "DEBUG" || len(custom) != 1 {
				t.Fatalf("custom = %v, %v", custom, err)
			}
			prios, ok, err := l.Priorities(ctx)
			if err != nil || !ok || prios["com.liferay.portal"] != "INFO" || prios[name] != "DEBUG" {
				t.Fatalf("priorities = %v, %v, %v", prios, ok, err)
			}
			g.Close()
			<-f.closed
		})
	}
}

func TestGogoOldLiferayWithoutPriorities(t *testing.T) {
	f := newFakeGogo(t, "com.liferay.petra.log4j.Log4JUtil")
	f.noPrios = true
	g, ctx := dialGogo(t, f.addr())
	l, err := g.Log4J(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := l.Priorities(ctx); ok || err != nil {
		t.Fatalf("sin getPriorities: ok = %v, err = %v", ok, err)
	}
	// la sesión sigue sirviendo después del error de Gogo
	if err := l.SetLevel(ctx, "com.example", "WARN"); err != nil {
		t.Fatal(err)
	}
}

func TestGogoRejectsBadInput(t *testing.T) {
	f := newFakeGogo(t, log4jClasses[0])
	g, ctx := dialGogo(t, f.addr())
	l, err := g.Log4J(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"", "com.foo; exit", "com.foo $x", "a\nb", "(x)", ".com"} {
		if err := l.SetLevel(ctx, name, "DEBUG"); err == nil {
			t.Errorf("SetLevel(%q) debería fallar", name)
		}
	}
	if err := l.SetLevel(ctx, "com.foo", "VERBOSE"); err == nil {
		t.Error("VERBOSE no es un nivel")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, line := range f.lines {
		if strings.Contains(line, "setlevel") {
			t.Errorf("no debería haber llegado nada a la Gogo shell: %q", line)
		}
	}
}

func TestGogoNoLog4J(t *testing.T) {
	f := newFakeGogo(t, "com.otra.Cosa")
	g, ctx := dialGogo(t, f.addr())
	if _, err := g.Log4J(ctx); err == nil || !strings.Contains(err.Error(), "Log4JUtil") {
		t.Fatalf("err = %v", err)
	}
}

func TestGogoClosedConnection(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err == nil {
			c.Write([]byte("algo que no es Gogo\r\n"))
			c.Close()
		}
	}()
	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := OpenGogo(ctx, c); err == nil || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, quería que avisara de la conexión cerrada", err)
	}
}

func TestGogoAddr(t *testing.T) {
	for _, tc := range []struct {
		in   string
		host string
		port int
		bad  bool
	}{
		{"", "localhost", 11311, false},
		{"11311", "localhost", 11311, false},
		{"localhost:11311", "localhost", 11311, false},
		{"0.0.0.0:12000", "localhost", 12000, false},
		{"127.0.0.1:11311", "127.0.0.1", 11311, false},
		{"[::1]:11311", "::1", 11311, false},
		{"localhost:", "", 0, true},
		{"abc", "", 0, true},
		{"99999", "", 0, true},
		{"x;y:11311", "", 0, true},
	} {
		host, port, err := GogoAddr(tc.in)
		if (err != nil) != tc.bad || host != tc.host || port != tc.port {
			t.Errorf("GogoAddr(%q) = %q, %d, %v", tc.in, host, port, err)
		}
	}
}

func TestLayoutGogoConfig(t *testing.T) {
	home := t.TempDir()
	l := &Layout{Home: home}
	if _, set := l.GogoConfig(); set {
		t.Fatal("sin ficheros no debería estar puesta")
	}
	os.WriteFile(filepath.Join(home, "portal-ext.properties"), []byte("include-and-override=portal-developer.properties\nmodule.framework.properties.osgi.console=localhost:12311\n"), 0o644)
	if v, set := l.GogoConfig(); !set || v != "localhost:12311" {
		t.Fatalf("portal-ext: %q, %v", v, set)
	}
	os.WriteFile(filepath.Join(home, "portal-setup-wizard.properties"), []byte("module.framework.properties.osgi.console=\n"), 0o644)
	if v, set := l.GogoConfig(); !set || v != "" {
		t.Fatalf("el asistente gana y la desactiva: %q, %v", v, set)
	}
}

func TestEffectiveLevel(t *testing.T) {
	levels := map[string]string{"com.liferay": "WARN", "com.liferay.portal.kernel": "INFO"}
	for _, tc := range []struct{ name, level, from string }{
		{"com.liferay.portal.kernel.util.Foo", "INFO", "com.liferay.portal.kernel"},
		{"com.liferay.portal", "WARN", "com.liferay"},
		{"com.liferay", "WARN", "com.liferay"},
		{"org.apache", "", ""},
	} {
		if lv, from := EffectiveLevel(levels, tc.name); lv != tc.level || from != tc.from {
			t.Errorf("EffectiveLevel(%s) = %s, %s", tc.name, lv, from)
		}
	}
}

// Contra una Gogo shell de verdad (un Liferay arrancado): LRAY_TEST_GOGO=
// localhost:11311 go test -run TestRealGogo ./internal/liferay. Cambia el
// nivel de un logger inventado; no toca nada más.
func TestRealGogo(t *testing.T) {
	addr := os.Getenv("LRAY_TEST_GOGO")
	if addr == "" {
		t.Skip("LRAY_TEST_GOGO no está puesta")
	}
	g, ctx := dialGogo(t, addr)
	l, err := g.Log4J(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Log4JUtil: %s", l.Class)
	if err := l.SetLevel(ctx, "com.example.lray.test", "DEBUG"); err != nil {
		t.Fatal(err)
	}
	custom, err := l.Custom(ctx)
	if err != nil || custom["com.example.lray.test"] != "DEBUG" {
		t.Fatalf("custom = %v, %v", custom, err)
	}
	prios, ok, err := l.Priorities(ctx)
	t.Logf("getPriorities: %v (%d niveles), %v", ok, len(prios), err)
	if err != nil || (ok && prios["com.example.lray.test"] != "DEBUG") {
		t.Fatalf("priorities = %v, %v", prios, err)
	}
}
