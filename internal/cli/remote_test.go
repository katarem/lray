package cli

import "testing"

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
