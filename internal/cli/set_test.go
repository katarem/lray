package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/katarem/lray/internal/config"
	"github.com/katarem/lray/internal/liferay"
)

// fakeBundle crea un liferay home con un Tomcat mínimo en el puerto 8080.
func fakeBundle(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	tomcat := filepath.Join(home, "tomcat-9.0.1")
	for _, d := range []string{"conf", "bin"} {
		if err := os.MkdirAll(filepath.Join(tomcat, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	xml := `<Server port="8005" shutdown="SHUTDOWN">
  <Service name="Catalina">
    <Connector port="8080" protocol="HTTP/1.1" redirectPort="8443" />
  </Service>
</Server>
`
	if err := os.WriteFile(filepath.Join(tomcat, "conf", "server.xml"), []byte(xml), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tomcat, "bin", "catalina.sh"), nil, 0o755); err != nil {
		t.Fatal(err)
	}
	return home
}

func registryWith(t *testing.T, servers ...config.Server) *config.Registry {
	t.Helper()
	t.Setenv("LRAY_HOME", t.TempDir())
	reg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range servers {
		reg.Put(s)
	}
	return reg
}

func ptr[T any](v T) *T { return &v }

func TestApplyChangesLocal(t *testing.T) {
	home := fakeBundle(t)
	reg := registryWith(t, config.Server{Name: "uno", Path: home, Port: 8080, Version: "7.4"})
	config.RecordState("uno", "stopped", nil)
	s, _ := reg.Get("uno")

	old, lines, write, err := applyChanges(reg, s, serverChanges{name: ptr("dos"), port: ptr(9180)})
	if err != nil {
		t.Fatal(err)
	}
	if old.Name != "uno" || s.Name != "dos" || s.Port != 9180 || len(lines) != 2 {
		t.Fatalf("old=%+v s=%+v lines=%q", old, s, lines)
	}
	l, _ := liferay.Resolve(home)
	if l.Port() != 8080 {
		t.Fatalf("el server.xml se ha tocado antes de guardar: %d", l.Port())
	}

	if _, err := saveChanges(reg, s, old, write); err != nil {
		t.Fatal(err)
	}
	if l, _ = liferay.Resolve(home); l.Port() != 9180 {
		t.Errorf("server.xml en %d, quería 9180", l.Port())
	}
	saved, _ := config.Load()
	if got, ok := saved.Get("dos"); !ok || got.Port != 9180 || got.Version != "7.4" {
		t.Errorf("registro guardado: %+v", saved.Servers)
	}
	st, _ := config.LoadStates()
	if _, ok := st.Servers["uno"]; ok {
		t.Error("el estado sigue con el nombre viejo")
	}
	if _, ok := st.Servers["dos"]; !ok {
		t.Error("el estado no ha pasado al nombre nuevo")
	}
}

func TestApplyChangesLocalPath(t *testing.T) {
	a, b := fakeBundle(t), fakeBundle(t)
	reg := registryWith(t, config.Server{Name: "uno", Path: a, Port: 9080})
	s, _ := reg.Get("uno")
	_, lines, _, err := applyChanges(reg, s, serverChanges{path: ptr(b)})
	if err != nil {
		t.Fatal(err)
	}
	if s.Path != b || s.Port != 8080 || len(lines) != 2 {
		t.Errorf("s=%+v lines=%q (quería la ruta nueva con el puerto de su bundle)", s, lines)
	}

	_, lines, _, err = applyChanges(reg, s, serverChanges{name: ptr("uno"), version: ptr("")})
	if err != nil || len(lines) != 0 {
		t.Errorf("sin cambios: lines=%q err=%v", lines, err)
	}
}

func TestApplyChangesJava(t *testing.T) {
	jdk := t.TempDir()
	if err := os.MkdirAll(filepath.Join(jdk, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(jdk, "bin", "java"), nil, 0o755); err != nil {
		t.Fatal(err)
	}
	reg := registryWith(t, config.Server{Name: "uno", Path: fakeBundle(t), Port: 8080})
	s, _ := reg.Get("uno")

	if _, _, _, err := applyChanges(reg, s, serverChanges{javaHome: ptr(t.TempDir())}); err == nil {
		t.Error("una carpeta sin bin/java no debería valer como JAVA_HOME")
	}
	if _, lines, _, err := applyChanges(reg, s, serverChanges{javaHome: ptr(jdk)}); err != nil || s.JavaHome != jdk || len(lines) != 1 {
		t.Errorf("java=%q lines=%q err=%v", s.JavaHome, lines, err)
	}
	if _, lines, _, err := applyChanges(reg, s, serverChanges{javaHome: ptr("")}); err != nil || s.JavaHome != "" || len(lines) != 1 {
		t.Errorf("al borrarlo: java=%q lines=%q err=%v", s.JavaHome, lines, err)
	}
}

func TestApplyChangesRejects(t *testing.T) {
	home := fakeBundle(t)
	reg := registryWith(t,
		config.Server{Name: "uno", Path: home, Port: 8080},
		config.Server{Name: "pre", Path: "/opt/liferay", Host: "yo@pre", Remote: &config.Remote{}},
	)
	cases := map[string]struct {
		server string
		c      serverChanges
	}{
		"nombre ocupado":       {"uno", serverChanges{name: ptr("pre")}},
		"nombre raro":          {"uno", serverChanges{name: ptr("mal nombre")}},
		"puerto fuera":         {"uno", serverChanges{port: ptr(70000)}},
		"local a remoto":       {"uno", serverChanges{path: ptr("yo@otra:/opt/liferay")}},
		"máquina en un local":  {"uno", serverChanges{host: ptr("yo@otra")}},
		"logs en un local":     {"uno", serverChanges{log: ptr("logs/*.log")}},
		"java en un remoto":    {"pre", serverChanges{javaHome: ptr("/usr/lib/jvm/java-21")}},
		"máquina no válida":    {"pre", serverChanges{host: ptr("-oProxyCommand=x")}},
		"ruta local que falta": {"uno", serverChanges{path: ptr(filepath.Join(home, "no-existe"))}},
	}
	for name, tc := range cases {
		s, _ := reg.Get(tc.server)
		before := *s
		if _, _, _, err := applyChanges(reg, s, tc.c); err == nil {
			t.Errorf("%s: debería fallar", name)
		}
		*s = before
	}
}

func TestApplyChangesRemote(t *testing.T) {
	ctrl := config.Control{Kind: config.ControlSystemd, Service: "jboss", Sudo: true}
	reg := registryWith(t, config.Server{Name: "pre", Path: "/opt/liferay", Port: 8080, Host: "yo@pre",
		Remote: &config.Remote{Control: ctrl}})
	config.RecordState("pre", "running", nil)
	s, _ := reg.Get("pre")

	newCtrl := config.Control{Kind: config.ControlCustom, Start: "/opt/arranca.sh"}
	old, lines, _, err := applyChanges(reg, s, serverChanges{
		path: ptr("ssh://yo@otra:2222/opt/liferay-74"), log: ptr("logs/*.log"), control: &newCtrl})
	if err != nil {
		t.Fatal(err)
	}
	if s.Host != "yo@otra" || s.Path != "/opt/liferay-74" || s.Remote.SSHPort != 2222 || s.Remote.Log != "logs/*.log" || s.Remote.Control != newCtrl {
		t.Errorf("s=%+v remote=%+v", s, s.Remote)
	}
	if old.Remote.SSHPort != 0 || old.Remote.Control != ctrl {
		t.Errorf("se ha tocado el original: %+v", old.Remote)
	}
	if len(lines) != 4 { // ruta, puerto SSH, log, gestión
		t.Errorf("lines=%q", lines)
	}

	// Otra ruta en la misma máquina: el estado guardado ya no vale.
	s, _ = reg.Get("pre")
	*s = old
	old, _, write, err := applyChanges(reg, s, serverChanges{path: ptr("/opt/liferay-74")})
	if err != nil {
		t.Fatal(err)
	}
	hint, err := saveChanges(reg, s, old, write)
	if err != nil || len(hint) == 0 {
		t.Fatalf("hint=%q err=%v", hint, err)
	}
	st, _ := config.LoadStates()
	if _, ok := st.Servers["pre"]; ok {
		t.Error("el estado de la ruta vieja sigue ahí")
	}
}
