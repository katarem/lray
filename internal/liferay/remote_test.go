package liferay

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestJBossPort(t *testing.T) {
	cases := []struct {
		name, xml, jvm string
		want           int
	}{
		{"defaults", `<socket-binding-group name="standard-sockets" default-interface="public" port-offset="${jboss.socket.binding.port-offset:0}">
	<socket-binding name="ajp" port="${jboss.ajp.port:8009}"/>
	<socket-binding name="http" port="${jboss.http.port:8080}"/>
	<socket-binding name="https" port="${jboss.https.port:8443}"/>`, "", 8080},
		{"offset", `<socket-binding-group name="s" port-offset="${jboss.socket.binding.port-offset:100}">
	<socket-binding name="http" port="8080"/>`, "", 8180},
		{"attrs in other order", `<socket-binding-group name="s" port-offset="0">
	<socket-binding port="${jboss.http.port:9080}" name="http"/>`, "", 9080},
		{"no http", `<socket-binding name="ajp" port="8009"/>`, "", 0},
		{"property without default", `<socket-binding name="http" port="${jboss.http.port}"/>`, "", 0},
		{"port from command line", `<socket-binding-group name="s" port-offset="${jboss.socket.binding.port-offset:0}">
	<socket-binding name="http" port="${jboss.http.port:8080}"/>`,
			"/usr/bin/java -Xmx4g -Djboss.http.port=9090 -jar jboss-modules.jar", 9090},
		{"offset from command line", `<socket-binding-group name="s" port-offset="${jboss.socket.binding.port-offset:0}">
	<socket-binding name="http" port="${jboss.http.port:8080}"/>`,
			"java -Djboss.socket.binding.port-offset=200 -c standalone-full.xml", 8280},
		{"property without default from command line", `<socket-binding name="http" port="${jboss.http.port}"/>`,
			"java -Djboss.http.port=8081", 8081},
	}
	for _, c := range cases {
		if got := jbossPort([]byte(c.xml), jvmProps(c.jvm)); got != c.want {
			t.Errorf("%s: jbossPort = %d, want %d", c.name, got, c.want)
		}
	}
}

func TestJBossBind(t *testing.T) {
	cases := map[string]string{
		"java -jar jboss-modules.jar -b 10.1.2.3 -c standalone.xml":  "10.1.2.3",
		"java -jar jboss-modules.jar -b=10.1.2.4":                    "10.1.2.4",
		"java -Djboss.bind.address=10.1.2.5 -jar jboss-modules.jar":  "10.1.2.5",
		"java -jar jboss-modules.jar -b 0.0.0.0":                     "",
		"java -jar jboss-modules.jar -Djboss.bind.address=127.0.0.1": "",
		"java -jar jboss-modules.jar":                                "",
	}
	for jvm, want := range cases {
		if got := jbossBind(jvm, jvmProps(jvm)); got != want {
			t.Errorf("jbossBind(%q) = %q, want %q", jvm, got, want)
		}
	}
}

func TestRemoteState(t *testing.T) {
	cases := []struct {
		active string
		code   int
		want   State
	}{
		{"0", -1, Stopped},
		{"0", 200, Stopped},
		{"1", 200, Running},
		{"1", 302, Running},
		{"1", 405, Running},  // HEAD no permitido: el portal responde
		{"1", 500, Running},  // portal con errores, pero arrancado
		{"1", 404, Starting}, // JBoss responde 404 hasta que Liferay se despliega
		{"1", 503, Starting},
		{"1", 0, Starting},
		{"1", -1, Running}, // sin curl: nos fiamos del servicio
		{"", 200, Running},
		{"", 0, Stopped},
	}
	for _, c := range cases {
		if got := remoteState(c.active, c.code); got != c.want {
			t.Errorf("remoteState(%q, %d) = %v, want %v", c.active, c.code, got, c.want)
		}
	}
}

func TestParseInspect(t *testing.T) {
	out := strings.Join([]string{
		"home=/opt/liferay",
		"kind=jboss",
		"app=/opt/liferay/jboss-eap-7.4",
		"jvm=/usr/lib/jvm/java-11/bin/java -D[Standalone] -Xmx8g -Djboss.socket.binding.port-offset=100 -jar /opt/liferay/jboss-eap-7.4/jboss-modules.jar -mp /opt/liferay/jboss-eap-7.4/modules org.jboss.as.standalone -Djboss.home.dir=/opt/liferay/jboss-eap-7.4 -b 10.20.30.40 -c standalone-full.xml",
		"conf=/opt/liferay/jboss-eap-7.4/standalone/configuration/standalone-full.xml",
		"@@conf",
		`<socket-binding-group name="s" port-offset="${jboss.socket.binding.port-offset:0}">`,
		`<socket-binding name="http" port="${jboss.http.port:8080}"/>`,
		"@@end",
		"log=/opt/liferay/logs/liferay.2026-10-07.log",
		"starting=2026-10-07 08:00:01.123 INFO  [main][StartupHelperUtil:72] Starting Liferay Digital Experience Platform 7.4.13 Update 92 (Cavanaugh / Build 7413 / March 1, 2024)",
		"active=1",
		"since=ActiveEnterTimestamp=Tue 2026-10-07 08:00:00 CEST",
	}, "\n")
	info, err := parseInspect([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	want := RemoteInfo{
		Home: "/opt/liferay", AppServer: "jboss", AppDir: "/opt/liferay/jboss-eap-7.4",
		Config: "/opt/liferay/jboss-eap-7.4/standalone/configuration/standalone-full.xml",
		Port:   8180, Bind: "10.20.30.40", LogFile: "/opt/liferay/logs/liferay.2026-10-07.log",
		Version: "DXP 7.4.13 Update 92", Active: "1", HTTP: -1,
		Since: "Tue 2026-10-07 08:00:00 CEST",
	}
	if *info != want {
		t.Errorf("parseInspect =\n%+v\nwant\n%+v", *info, want)
	}
}

func TestParseProbe(t *testing.T) {
	cases := []struct {
		name, out    string
		code         int
		url, version string
	}{
		{"primero listo", "probe=http://10.0.0.1:8080/ 200\nportal=Liferay Digital Experience Platform 7.4.13 Update 92 (Cavanaugh / Build 7413 / March 1, 2024)",
			200, "http://10.0.0.1:8080/", "DXP 7.4.13 Update 92"},
		{"404 y luego listo", "probe=http://127.0.0.1:8080/ 000\nprobe=http://maquina:8080/ 404\nprobe=http://127.0.0.1:8180/ 302",
			302, "http://127.0.0.1:8180/", ""},
		{"nadie listo: el que respondió", "probe=http://127.0.0.1:8080/ 000\nprobe=http://maquina:8080/ 404",
			404, "http://maquina:8080/", ""},
		{"cabecera sin versión", "probe=http://127.0.0.1:8080/ 200\nportal=Liferay Digital Experience Platform",
			200, "http://127.0.0.1:8080/", ""},
		{"sin curl", "", -1, "", ""},
	}
	for _, c := range cases {
		info := &RemoteInfo{}
		parseProbe([]byte(c.out), info)
		if info.HTTP != c.code || info.URL != c.url || info.Version != c.version {
			t.Errorf("%s: = (%d, %q, %q), want (%d, %q, %q)", c.name, info.HTTP, info.URL, info.Version, c.code, c.url, c.version)
		}
	}
}

func TestParseInspectErrors(t *testing.T) {
	for _, out := range []string{"error=nodir", "error=noaccess", "error=notliferay", ""} {
		if _, err := parseInspect([]byte(out)); err == nil {
			t.Errorf("parseInspect(%q): want error", out)
		}
	}
}

func TestLogPattern(t *testing.T) {
	cases := []struct{ path, log, want string }{
		{"/opt/liferay", "", "/opt/liferay/logs/liferay.*.log"},
		{"/opt/liferay", "logs/otro.log", "/opt/liferay/logs/otro.log"},
		{"/opt/liferay", "/var/log/jboss/server.log", "/var/log/jboss/server.log"},
	}
	for _, c := range cases {
		r := &Remote{Path: c.path, Log: c.log}
		if got := r.LogPattern(); got != c.want {
			t.Errorf("LogPattern(%q, %q) = %q, want %q", c.path, c.log, got, c.want)
		}
	}
}

// fakeExec responde a los scripts con salidas fijas y apunta las llamadas.
type fakeExec struct {
	out    map[string]string // por script
	stream string
	calls  [][]string
}

func (f *fakeExec) Run(_ context.Context, script string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, args)
	return []byte(f.out[script]), nil
}

func (f *fakeExec) Stream(_ context.Context, _ string, args ...string) (io.ReadCloser, error) {
	f.calls = append(f.calls, args)
	return io.NopCloser(strings.NewReader(f.stream)), nil
}

func TestRemoteFollowAndInspectArgs(t *testing.T) {
	f := &fakeExec{stream: "una\ndos\r\ntres", out: map[string]string{
		inspectScript: "home=/opt/liferay\nkind=jboss\napp=/opt/liferay/jboss\n@@conf\n<socket-binding name=\"http\" port=\"8080\"/>\n@@end\nactive=1\n",
		probeScript:   "probe=http://127.0.0.1:8080/ 404\nprobe=http://127.0.0.1:9080/ 200\n",
	}}
	r := &Remote{Exec: f, Path: "/opt/liferay", Port: 9080, Status: "systemctl is-active --quiet jboss"}

	var lines []string
	if err := r.Follow(context.Background(), 20, func(l string) { lines = append(lines, l) }, func() {}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(lines, "|"); got != "una|dos|tres" {
		t.Errorf("Follow lines = %q", got)
	}
	if got := strings.Join(f.calls[0], " "); got != "/opt/liferay/logs/liferay.*.log 20" {
		t.Errorf("Follow args = %q", got)
	}

	f.calls = nil
	info, err := r.Inspect(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if info.State != Running || info.HTTP != 200 || info.Port != 8080 {
		t.Errorf("Inspect = %+v", info)
	}
	want := [][]string{
		{"/opt/liferay", "", "systemctl is-active --quiet jboss", "", "1"},
		{"", "8080", "9080"}, // el de la configuración y el registrado
	}
	if len(f.calls) != 2 || strings.Join(f.calls[0], "\x00") != strings.Join(want[0], "\x00") ||
		strings.Join(f.calls[1], "\x00") != strings.Join(want[1], "\x00") {
		t.Errorf("llamadas = %q, want %q", f.calls, want)
	}

	// Parado: no se sondea.
	f.out[inspectScript] = "home=/opt/liferay\nactive=0\n"
	f.calls = nil
	if info, err := r.Inspect(context.Background(), false); err != nil || info.State != Stopped || len(f.calls) != 1 {
		t.Errorf("parado: %+v, %v, %d llamadas", info, err, len(f.calls))
	}
}

func TestReleaseVersion(t *testing.T) {
	cases := []struct {
		lines []string
		want  string
	}{
		// Cadenas reales de ReleaseInfo.class (portal-kernel 166.0.0); "#" y ","
		// son el byte de longitud de la constante.
		{[]string{"#Liferay Digital Experience Platform", "7.4.13 Update 137", ",Liferay Digital Experience Platform / 7.4.13", "Liferay, Inc.", "7.4.13"},
			"DXP 7.4.13 Update 137"},
		{[]string{"#Liferay Digital Experience Platform", "\x0d2025.Q1.5 LTS"}, "DXP 2025.Q1.5 LTS"},
		{[]string{" Liferay Community Edition Portal", "7.4.3.132 GA132", "7.4.3"}, "Portal CE 7.4.3.132 GA132"},
		{[]string{"Liferay Portal", "7.2.1"}, "Portal 7.2.1"},
		{[]string{"Liferay, Inc."}, ""},
		{nil, ""},
	}
	for _, c := range cases {
		if got := releaseVersion(c.lines); got != c.want {
			t.Errorf("releaseVersion(%q) = %q, want %q", c.lines, got, c.want)
		}
	}
}
