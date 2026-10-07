//go:build !windows

package liferay

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// localExec ejecuta los scripts "remotos" con el sh de esta máquina, igual
// que lo haría ssh en la otra: así se prueban los scripts de verdad.
type localExec struct{}

func (localExec) Run(ctx context.Context, script string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, "sh", append([]string{"-c", script, "lray"}, args...)...).Output()
}

func (localExec) Stream(ctx context.Context, script string, args ...string) (io.ReadCloser, error) {
	cmd := exec.Command("sh", append([]string{"-c", script, "lray"}, args...)...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &localStream{cmd: cmd, stdin: stdin, ReadCloser: stdout}, nil
}

type localStream struct {
	io.ReadCloser
	cmd   *exec.Cmd
	stdin io.WriteCloser
	once  sync.Once
}

// Close hace lo mismo que ssh al cortar: cierra la entrada del script.
func (s *localStream) Close() error {
	s.once.Do(func() {
		_ = s.stdin.Close()
		_ = s.cmd.Wait()
	})
	return nil
}

// fakeJBoss monta un liferay home con JBoss (port-offset 100) y dos logs
// diarios; la versión solo aparece en el de ayer.
func fakeJBoss(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	write := func(rel, content string) string {
		p := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	write("jboss-eap-7.4/bin/standalone.sh", "#!/bin/sh\n")
	write("jboss-eap-7.4/standalone/configuration/standalone.xml", `<server>
  <socket-binding-group name="standard-sockets" port-offset="${jboss.socket.binding.port-offset:100}">
    <socket-binding name="http" port="${jboss.http.port:8080}"/>
  </socket-binding-group>
</server>`)
	ayer := write("logs/liferay.2026-10-06.log",
		"2026-10-06 09:00:00.000 INFO  [main][StartupHelperUtil:72] Starting Liferay Digital Experience Platform 7.4.13 Update 92 (Cavanaugh / Build 7413 / March 1, 2024)\n")
	write("logs/liferay.2026-10-07.log", "2026-10-07 07:00:00.000 INFO  [main][Foo:1] hoy\n")
	old := time.Now().Add(-24 * time.Hour)
	if err := os.Chtimes(ayer, old, old); err != nil {
		t.Fatal(err)
	}
	return home
}

func TestInspectScript(t *testing.T) {
	home := fakeJBoss(t)
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	port := srv.Listener.Addr().(*net.TCPAddr).Port

	for _, path := range []string{home, filepath.Join(home, "jboss-eap-7.4")} {
		r := &Remote{Exec: localExec{}, Path: path, Port: port, Status: "true"}
		info, err := r.Inspect(context.Background(), true)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if info.Home != home || info.AppServer != "jboss" || info.AppDir != filepath.Join(home, "jboss-eap-7.4") {
			t.Errorf("%s: dónde = %q %q %q", path, info.Home, info.AppServer, info.AppDir)
		}
		if info.Port != 8180 {
			t.Errorf("%s: Port = %d, want 8180 (8080 + offset 100)", path, info.Port)
		}
		if info.LogFile != filepath.Join(home, "logs", "liferay.2026-10-07.log") {
			t.Errorf("%s: LogFile = %q, want el de hoy", path, info.LogFile)
		}
		if info.Version != "DXP 7.4.13 Update 92" {
			t.Errorf("%s: Version = %q", path, info.Version)
		}
		if info.State != Running || info.HTTP != 200 {
			t.Errorf("%s: State = %v, HTTP = %d", path, info.State, info.HTTP)
		}
	}

	// Servicio parado: apagado aunque algo responda en el puerto.
	r := &Remote{Exec: localExec{}, Path: home, Port: port, Status: "false"}
	if info, err := r.Inspect(context.Background(), false); err != nil || info.State != Stopped || info.Version != "" {
		t.Errorf("parado: %+v, %v", info, err)
	}
}

func TestInspectScriptErrors(t *testing.T) {
	cases := map[string]string{
		filepath.Join(t.TempDir(), "no-existe"): "no existe",
		t.TempDir():                             "no parece",
	}
	for path, want := range cases {
		r := &Remote{Exec: localExec{}, Path: path}
		if _, err := r.Inspect(context.Background(), false); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want %q", path, err, want)
		}
	}
}

func TestFollowScriptRotates(t *testing.T) {
	if testing.Short() {
		t.Skip("espera a que el script detecte el log nuevo")
	}
	home := fakeJBoss(t)
	r := &Remote{Exec: localExec{}, Path: home}

	got := make(chan string, 10)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- r.Follow(ctx, 1, func(l string) { got <- l }, func() {})
	}()
	expect := func(want string) {
		t.Helper()
		select {
		case l := <-got:
			if !strings.Contains(l, want) {
				t.Fatalf("línea = %q, want %q", l, want)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("no ha llegado %q", want)
		}
	}
	appendTo := func(name, line string) {
		f, err := os.OpenFile(filepath.Join(home, "logs", name), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintln(f, line)
		f.Close()
	}

	expect("hoy") // última línea del log más reciente
	appendTo("liferay.2026-10-07.log", "nueva de hoy")
	expect("nueva de hoy")
	// Rotación a medianoche: el de ayer deja de escribirse y aparece otro.
	old := time.Now().Add(-time.Minute)
	if err := os.Chtimes(filepath.Join(home, "logs", "liferay.2026-10-07.log"), old, old); err != nil {
		t.Fatal(err)
	}
	appendTo("liferay.2026-10-08.log", "primera de mañana")
	expect("primera de mañana")

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Follow = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Follow no termina al cancelar")
	}
}

func TestFollowScriptNoLogs(t *testing.T) {
	r := &Remote{Exec: localExec{}, Path: t.TempDir()}
	done := make(chan struct{})
	go func() {
		// localStream no traduce el código de salida: basta con que no se cuelgue.
		_ = r.Follow(context.Background(), 10, func(string) {}, func() {})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Follow se queda colgado sin logs")
	}
}

// Sin comando de estado se busca un proceso java que mencione la carpeta del
// servidor de aplicaciones.
func TestInspectScriptFindsJavaProcess(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("hace falta bash para disfrazar un proceso de java")
	}
	home := fakeJBoss(t)
	app := filepath.Join(home, "jboss-eap-7.4")
	r := &Remote{Exec: localExec{}, Path: home}

	if info, err := r.Inspect(context.Background(), false); err != nil || info.Active != "0" {
		t.Fatalf("sin proceso: %+v, %v", info, err)
	}

	fake := exec.Command(bash, "-c", `exec -a /usr/lib/jvm/bin/java sh -c 'sleep 30; :' "-Djboss.home.dir=$1"`, "x", app)
	if err := fake.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fake.Process.Kill(); _ = fake.Wait() }()
	time.Sleep(200 * time.Millisecond)

	info, err := r.Inspect(context.Background(), false)
	if err != nil || info.Active != "1" || info.State != Running {
		t.Errorf("con proceso: %+v, %v", info, err)
	}
}

// La versión sale de la cabecera Liferay-Portal y, si el portal no la
// dice, de un log de hace semanas.
func TestInspectScriptVersion(t *testing.T) {
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("hace falta curl")
	}
	home := fakeJBoss(t)
	srv := httptest.NewServer(portalHandler())
	defer srv.Close()
	port := srv.Listener.Addr().(*net.TCPAddr).Port

	r := &Remote{Exec: localExec{}, Path: home, Port: port, Status: "true"}
	info, err := r.Inspect(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if info.State != Running || info.HTTP != 302 {
		t.Errorf("State = %v, HTTP = %d (%s)", info.State, info.HTTP, info.URL)
	}
	if info.Version != "DXP 7.4.13 Update 95" {
		t.Errorf("Version = %q, want la de la cabecera", info.Version)
	}

	logs := filepath.Join(home, "logs")
	for i := 1; i <= 10; i++ {
		f := filepath.Join(logs, fmt.Sprintf("liferay.2026-09-%02d.log", 20+i))
		if err := os.WriteFile(f, []byte("2026-09 INFO  [main][Foo:1] nada\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		old := time.Now().Add(-time.Duration(30-i) * 24 * time.Hour)
		_ = os.Chtimes(f, old, old)
	}
	old := time.Now().Add(-40 * 24 * time.Hour) // el que tiene el arranque, el más viejo
	_ = os.Chtimes(filepath.Join(logs, "liferay.2026-10-06.log"), old, old)
	r.Port = 1 // nadie escucha: sin cabecera
	if info, err = r.Inspect(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if info.Version != "DXP 7.4.13 Update 92" {
		t.Errorf("Version = %q, want la del log viejo", info.Version)
	}
}

// En los servidores suele haber http_proxy en el entorno: la sonda no debe ir
// por él. curl ya no usa el proxy para IPs de loopback, así que hace falta
// escuchar en la IP de la máquina (si no tiene, se salta).
func TestInspectScriptIgnoresProxy(t *testing.T) {
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("hace falta curl")
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(hostIP(t), "0"))
	if err != nil {
		t.Skip("no puedo escuchar en la IP de la máquina:", err)
	}
	srv := httptest.NewUnstartedServer(portalHandler())
	srv.Listener.Close()
	srv.Listener = ln
	srv.Start()
	defer srv.Close()
	t.Setenv("http_proxy", "http://127.0.0.1:9") // si curl lo usara, no llegaría nunca

	r := &Remote{Exec: localExec{}, Path: fakeJBoss(t), Port: srv.Listener.Addr().(*net.TCPAddr).Port, Status: "true"}
	info, err := r.Inspect(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if info.State != Running || info.HTTP != 302 {
		t.Errorf("State = %v, HTTP = %d (%s)", info.State, info.HTTP, info.URL)
	}
}

func portalHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Liferay-Portal", "Liferay Digital Experience Platform 7.4.13 Update 95 (Cavanaugh / Build 7413 / May 1, 2024)")
		w.WriteHeader(http.StatusFound)
	})
}

// El puerto sale de la configuración que usa de verdad (-c) y de las
// propiedades de su línea de comandos.
func TestInspectScriptReadsJavaCommandLine(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("hace falta bash para disfrazar un proceso de java")
	}
	home := fakeJBoss(t)
	app := filepath.Join(home, "jboss-eap-7.4")
	full := `<server><socket-binding-group name="s" port-offset="${jboss.socket.binding.port-offset:0}">
<socket-binding name="http" port="${jboss.http.port:8080}"/></socket-binding-group></server>`
	if err := os.WriteFile(filepath.Join(app, "standalone", "configuration", "standalone-full.xml"), []byte(full), 0o644); err != nil {
		t.Fatal(err)
	}
	fake := exec.Command(bash, "-c",
		`exec -a /usr/lib/jvm/bin/java sh -c 'sleep 30; :' -Djboss.socket.binding.port-offset=300 "-Djboss.home.dir=$1" -b 10.9.8.7 -c standalone-full.xml`, "x", app)
	if err := fake.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fake.Process.Kill(); _ = fake.Wait() }()
	time.Sleep(200 * time.Millisecond)

	out, err := localExec{}.Run(context.Background(), inspectScript, home, "", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	info, err := parseInspect(out)
	if err != nil {
		t.Fatal(err)
	}
	if info.Port != 8380 || info.Bind != "10.9.8.7" || !strings.HasSuffix(info.Config, "standalone-full.xml") || info.Active != "1" {
		t.Errorf("= %+v", info)
	}
}

// hostIP es la primera IPv4 (no de loopback) a la que resuelve el nombre de
// la máquina.
func hostIP(t *testing.T) string {
	t.Helper()
	name, err := os.Hostname()
	if err != nil {
		t.Skip("sin nombre de máquina")
	}
	addrs, _ := net.LookupHost(name)
	for _, a := range addrs {
		if ip := net.ParseIP(a); ip != nil && ip.To4() != nil && !ip.IsLoopback() {
			return a
		}
	}
	t.Skip("el nombre de la máquina no resuelve a una IP que no sea de loopback")
	return ""
}
