package liferay

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestJBossPort(t *testing.T) {
	cases := []struct {
		name, xml string
		want      int
	}{
		{"defaults", `<socket-binding-group name="standard-sockets" default-interface="public" port-offset="${jboss.socket.binding.port-offset:0}">
	<socket-binding name="ajp" port="${jboss.ajp.port:8009}"/>
	<socket-binding name="http" port="${jboss.http.port:8080}"/>
	<socket-binding name="https" port="${jboss.https.port:8443}"/>`, 8080},
		{"offset", `<socket-binding-group name="s" port-offset="${jboss.socket.binding.port-offset:100}">
	<socket-binding name="http" port="8080"/>`, 8180},
		{"attrs in other order", `<socket-binding-group name="s" port-offset="0">
	<socket-binding port="${jboss.http.port:9080}" name="http"/>`, 9080},
		{"no http", `<socket-binding name="ajp" port="8009"/>`, 0},
		{"property without default", `<socket-binding name="http" port="${jboss.http.port}"/>`, 0},
	}
	for _, c := range cases {
		if got := jbossPort([]byte(c.xml)); got != c.want {
			t.Errorf("%s: jbossPort = %d, want %d", c.name, got, c.want)
		}
	}
}

func TestRemoteState(t *testing.T) {
	cases := []struct {
		active string
		code   int
		probed bool
		want   State
	}{
		{"0", -1, true, Stopped},
		{"1", 200, true, Running},
		{"1", 302, true, Running},
		{"1", 404, true, Starting}, // JBoss responde 404 hasta que Liferay se despliega
		{"1", 0, true, Starting},
		{"1", -1, true, Running}, // sin curl: nos fiamos del servicio
		{"1", -1, false, Running},
		{"", 200, true, Running},
		{"", 0, true, Stopped},
	}
	for _, c := range cases {
		if got := remoteState(c.active, c.code, c.probed); got != c.want {
			t.Errorf("remoteState(%q, %d, %v) = %v, want %v", c.active, c.code, c.probed, got, c.want)
		}
	}
}

func TestParseInspect(t *testing.T) {
	out := strings.Join([]string{
		"home=/opt/liferay",
		"kind=jboss",
		"app=/opt/liferay/jboss-eap-7.4",
		"@@conf",
		`<socket-binding-group name="s" port-offset="${jboss.socket.binding.port-offset:0}">`,
		`<socket-binding name="http" port="${jboss.http.port:8080}"/>`,
		"@@end",
		"log=/opt/liferay/logs/liferay.2026-10-07.log",
		"starting=2026-10-07 08:00:01.123 INFO  [main][StartupHelperUtil:72] Starting Liferay Digital Experience Platform 7.4.13 Update 92 (Cavanaugh / Build 7413 / March 1, 2024)",
		"active=1",
		"since=ActiveEnterTimestamp=Tue 2026-10-07 08:00:00 CEST",
		"http=200",
	}, "\n")
	info, err := parseInspect([]byte(out), 8080)
	if err != nil {
		t.Fatal(err)
	}
	want := RemoteInfo{
		Home: "/opt/liferay", AppServer: "jboss", AppDir: "/opt/liferay/jboss-eap-7.4",
		Port: 8080, LogFile: "/opt/liferay/logs/liferay.2026-10-07.log",
		Version: "DXP 7.4.13 Update 92", Active: "1", HTTP: 200,
		Since: "Tue 2026-10-07 08:00:00 CEST", State: Running,
	}
	if *info != want {
		t.Errorf("parseInspect =\n%+v\nwant\n%+v", *info, want)
	}
}

func TestParseInspectErrors(t *testing.T) {
	for _, out := range []string{"error=nodir", "error=noaccess", "error=notliferay", ""} {
		if _, err := parseInspect([]byte(out), 0); err == nil {
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

// fakeExec responde a los scripts con salidas fijas.
type fakeExec struct {
	out    string
	stream string
	args   []string
}

func (f *fakeExec) Run(_ context.Context, _ string, args ...string) ([]byte, error) {
	f.args = args
	return []byte(f.out), nil
}

func (f *fakeExec) Stream(_ context.Context, _ string, args ...string) (io.ReadCloser, error) {
	f.args = args
	return io.NopCloser(strings.NewReader(f.stream)), nil
}

func TestRemoteFollowAndInspectArgs(t *testing.T) {
	f := &fakeExec{stream: "una\ndos\r\ntres", out: "home=/opt/liferay\nactive=0\n"}
	r := &Remote{Exec: f, Path: "/opt/liferay", Port: 8080, Status: "systemctl is-active --quiet jboss"}

	var lines []string
	if err := r.Follow(context.Background(), 20, func(l string) { lines = append(lines, l) }, func() {}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(lines, "|"); got != "una|dos|tres" {
		t.Errorf("Follow lines = %q", got)
	}
	if got := strings.Join(f.args, " "); got != "/opt/liferay/logs/liferay.*.log 20" {
		t.Errorf("Follow args = %q", got)
	}

	info, err := r.Inspect(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if info.State != Stopped {
		t.Errorf("State = %v, want Stopped", info.State)
	}
	want := []string{"/opt/liferay", "", "8080", "systemctl is-active --quiet jboss", "", "1"}
	if strings.Join(f.args, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("Inspect args = %q, want %q", f.args, want)
	}
}
