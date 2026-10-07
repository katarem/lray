package config

import (
	"errors"
	"testing"
)

func TestControlCommands(t *testing.T) {
	cases := []struct {
		name                       string
		c                          Control
		start, stop, status, since string
	}{
		{"systemd con sudo", Control{Kind: ControlSystemd, Service: "jboss", Sudo: true},
			"sudo systemctl start jboss", "sudo systemctl stop jboss", "systemctl is-active --quiet jboss",
			"systemctl show -p ActiveEnterTimestamp jboss"},
		{"service sin sudo", Control{Kind: ControlService, Service: "jboss-eap"},
			"service jboss-eap start", "service jboss-eap stop", "service jboss-eap status", ""},
		{"propios", Control{Kind: ControlCustom, Start: "/opt/arranca.sh", Stop: "/opt/para.sh", Sudo: true},
			"sudo /opt/arranca.sh", "sudo /opt/para.sh", "", ""},
		{"nombre raro", Control{Kind: ControlSystemd, Service: "mi servicio;rm"},
			"systemctl start 'mi servicio;rm'", "systemctl stop 'mi servicio;rm'", "systemctl is-active --quiet 'mi servicio;rm'",
			"systemctl show -p ActiveEnterTimestamp 'mi servicio;rm'"},
		{"sin gestionar", Control{}, "", "", "", ""},
	}
	for _, c := range cases {
		if got := c.c.StartCommand(); got != c.start {
			t.Errorf("%s: start = %q, want %q", c.name, got, c.start)
		}
		if got := c.c.StopCommand(); got != c.stop {
			t.Errorf("%s: stop = %q, want %q", c.name, got, c.stop)
		}
		if got := c.c.StatusCommand(); got != c.status {
			t.Errorf("%s: status = %q, want %q", c.name, got, c.status)
		}
		if got := c.c.SinceCommand(); got != c.since {
			t.Errorf("%s: since = %q, want %q", c.name, got, c.since)
		}
	}
}

func TestShellQuote(t *testing.T) {
	for in, want := range map[string]string{
		"jboss":     "jboss",
		"jboss-eap": "jboss-eap",
		"":          "''",
		"a b":       "'a b'",
		"it's":      `'it'\''s'`,
		"$(id)":     "'$(id)'",
	} {
		if got := ShellQuote(in); got != want {
			t.Errorf("ShellQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStatesKeepLastKnown(t *testing.T) {
	t.Setenv("LRAY_HOME", t.TempDir())
	RecordState("pre", "running", nil)
	RecordState("pre", "", errors.New("sin vpn"))

	st, err := LoadStates()
	if err != nil {
		t.Fatal(err)
	}
	snap := st.Servers["pre"]
	if snap.State != "running" || snap.CheckedAt.IsZero() {
		t.Errorf("se ha perdido el último estado bueno: %+v", snap)
	}
	if snap.Reachable() || snap.Error != "sin vpn" {
		t.Errorf("el fallo no ha quedado apuntado: %+v", snap)
	}

	RecordState("pre", "stopped", nil)
	st, _ = LoadStates()
	if snap := st.Servers["pre"]; !snap.Reachable() || snap.State != "stopped" {
		t.Errorf("tras volver a conectar: %+v", snap)
	}

	ForgetState("pre")
	st, _ = LoadStates()
	if _, ok := st.Servers["pre"]; ok {
		t.Error("ForgetState no lo ha borrado")
	}
}
