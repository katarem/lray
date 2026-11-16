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

	RenameState("pre", "preproduccion")
	st, _ = LoadStates()
	if _, ok := st.Servers["pre"]; ok {
		t.Error("RenameState ha dejado el nombre viejo")
	}
	if snap := st.Servers["preproduccion"]; snap.State != "stopped" {
		t.Errorf("RenameState ha perdido el estado: %+v", snap)
	}

	ForgetState("preproduccion")
	st, _ = LoadStates()
	if _, ok := st.Servers["preproduccion"]; ok {
		t.Error("ForgetState no lo ha borrado")
	}
}

// Un puntero de Get no puede cambiar de server al quitar otro ni al guardar
// (Save ordena por nombre). Antes, rm enseñaba la ruta del server siguiente.
func TestGetPointerSurvivesRemoveAndSave(t *testing.T) {
	t.Setenv("LRAY_HOME", t.TempDir())
	r, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	r.Put(Server{Name: "local-turismo", Path: "/home/yo/turismo"})
	r.Put(Server{Name: "validacion-turismo", Path: "/opt/liferay", Host: "yo@maquina"})
	r.Put(Server{Name: "zeta", Path: "/z"})

	s, _ := r.Get("local-turismo")
	r.Remove("local-turismo")
	if s.Name != "local-turismo" || s.Path != "/home/yo/turismo" {
		t.Fatalf("tras Remove el puntero es de %q (%s)", s.Name, s.Path)
	}
	if _, ok := r.Get("local-turismo"); ok || len(r.Servers) != 2 {
		t.Fatalf("Remove no lo ha quitado: %+v", r.Servers)
	}

	z, _ := r.Get("zeta")
	z.Name = "alfa" // ahora va primero al ordenar
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}
	if z.Name != "alfa" || z.Path != "/z" {
		t.Fatalf("tras Save el puntero es de %q (%s)", z.Name, z.Path)
	}
	again, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Servers) != 2 || again.Servers[0].Name != "alfa" || again.Servers[1].Name != "validacion-turismo" {
		t.Fatalf("servers.json no está ordenado: %+v", again.Servers)
	}
}
