package ssh

import (
	"errors"
	"os/exec"
	"runtime"
	"testing"
)

// Remote tiene que llegar intacto al script aunque los argumentos lleven
// espacios, comillas o cosas que sh interpretaría.
func TestRemoteQuoting(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("necesita sh")
	}
	args := []string{"/opt/mi liferay", "it's", "$(id)", "`id`", "a;b", "*", ""}
	line := Remote(`for a in "$@"; do printf '[%s]' "$a"; done`, args...)
	out, err := exec.Command("sh", "-c", line).Output() // lo que hace el sshd remoto
	if err != nil {
		t.Fatal(err)
	}
	want := "[/opt/mi liferay][it's][$(id)][`id`][a;b][*][]"
	if string(out) != want {
		t.Errorf("salida = %s\nwant      %s", out, want)
	}
}

func TestValidDest(t *testing.T) {
	for _, ok := range []string{"D169497@DC0GVALLRP001", "pre-liferay", "yo@10.0.0.5", "yo@maquina.empresa.local"} {
		if err := ValidDest(ok); err != nil {
			t.Errorf("ValidDest(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"", "-oProxyCommand=id", "yo@host;id", "a b", "$(id)"} {
		if ValidDest(bad) == nil {
			t.Errorf("ValidDest(%q) debería fallar", bad)
		}
	}
}

func TestClassify(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("necesita sh")
	}
	e255 := exec.Command("sh", "-c", "exit 255").Run()
	cases := []struct {
		stderr string
		want   error
	}{
		{"ssh: Could not resolve hostname dc0gvallrp001: Name or service not known", ErrUnreachable},
		{"ssh: connect to host 10.1.2.3 port 22: Connection timed out", ErrUnreachable},
		{"ssh: connect to host 10.1.2.3 port 22: No route to host", ErrUnreachable},
		{"D169497@host: Permission denied (publickey,password).", ErrAuth},
	}
	for _, c := range cases {
		if err := classify("host", e255, []byte(c.stderr)); !errors.Is(err, c.want) {
			t.Errorf("classify(%q) = %v, want %v", c.stderr, err, c.want)
		}
	}
	// Un fallo del propio comando remoto no es un problema de conexión.
	e3 := exec.Command("sh", "-c", "exit 3").Run()
	err := classify("host", e3, []byte("no hay ningún log que encaje con /opt/liferay/logs/liferay.*.log\n"))
	if errors.Is(err, ErrUnreachable) || errors.Is(err, ErrAuth) || err == nil {
		t.Errorf("classify(exit 3) = %v", err)
	}
	if got := err.Error(); got != "no hay ningún log que encaje con /opt/liferay/logs/liferay.*.log (código 3)" {
		t.Errorf("mensaje = %q", got)
	}
}
