package cli

import (
	"testing"

	"github.com/katarem/lray/internal/config"
	"github.com/katarem/lray/internal/liferay"
)

func TestParseRemoteSpec(t *testing.T) {
	cases := []struct {
		arg        string
		dest, path string
		port       int
		remote     bool
		wantErr    bool
	}{
		{arg: "D169497@DC0GVALLRP001:/opt/liferay", dest: "D169497@DC0GVALLRP001", path: "/opt/liferay", remote: true},
		{arg: "pre-liferay:/opt/liferay", dest: "pre-liferay", path: "/opt/liferay", remote: true},
		{arg: "pre:", dest: "pre", path: "~", remote: true},
		{arg: "ssh://yo@maquina:2222/opt/liferay", dest: "yo@maquina", port: 2222, path: "/opt/liferay", remote: true},
		{arg: "ssh://maquina", dest: "maquina", path: "~", remote: true},
		{arg: `C:\liferay`},
		{arg: "C:/liferay"},
		{arg: "~/proyectos/tienda"},
		{arg: "./a:b/c"},
		{arg: "-oProxyCommand=x:/tmp", remote: true, wantErr: true},
	}
	for _, c := range cases {
		dest, port, path, remote, err := parseRemoteSpec(c.arg)
		if (err != nil) != c.wantErr {
			t.Errorf("%q: err = %v", c.arg, err)
			continue
		}
		if remote != c.remote {
			t.Errorf("%q: remote = %v, want %v", c.arg, remote, c.remote)
			continue
		}
		if c.wantErr || !remote {
			continue
		}
		if dest != c.dest || port != c.port || path != c.path {
			t.Errorf("%q = (%q, %d, %q), want (%q, %d, %q)", c.arg, dest, port, path, c.dest, c.port, c.path)
		}
	}
}

func TestLearn(t *testing.T) {
	s := &config.Server{Name: "pre", Port: 8080, Host: "yo@pre"}

	// JBoss aún desplegando (404): no se aprende el puerto.
	if learn(s, &liferay.RemoteInfo{HTTP: 404, URL: "http://127.0.0.1:8180/"}) || s.Port != 8080 {
		t.Errorf("con 404 no debería cambiar nada: %+v", s)
	}
	// El portal responde en otro puerto y dice su versión: se guardan.
	info := &liferay.RemoteInfo{HTTP: 302, URL: "http://10.0.0.5:8180/", Version: "DXP 7.4.13 Update 92"}
	if !learn(s, info) || s.Port != 8180 || s.Version != "DXP 7.4.13 Update 92" {
		t.Errorf("tras aprender: %+v", s)
	}
	if learn(s, info) {
		t.Error("la segunda vez no hay nada nuevo")
	}
}
