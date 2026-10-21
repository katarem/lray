package ssh

import (
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
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

// Con un socket caducado de la sesión compartida, ssh avisa con "Connection
// refused" antes de conectar por su cuenta; eso no quiere decir que la
// máquina no responda.
func TestClassifyIgnoresStaleSocket(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("necesita sh")
	}
	e255 := exec.Command("sh", "-c", "exit 255").Run()
	stderr := "Control socket connect(/home/yo/.cache/lray/ssh/851e2af0): Connection refused\n" +
		"D169497@dc1glablrp004: Permission denied (publickey,password)."
	if err := classify("host", e255, []byte(stderr)); !errors.Is(err, ErrAuth) {
		t.Errorf("classify = %v, want ErrAuth", err)
	}
	stderr = "ControlSocket /home/yo/.cache/lray/ssh/851e2af0 already exists, disabling multiplexing\n" +
		"ssh: connect to host 10.1.2.3 port 22: Connection timed out"
	if err := classify("host", e255, []byte(stderr)); !errors.Is(err, ErrUnreachable) {
		t.Errorf("classify = %v, want ErrUnreachable", err)
	}
}

// Un socket que sobrevive a un reinicio tiene que desaparecer antes de abrir
// la sesión; si no, ssh no puede crear la nueva.
func TestClearStale(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("rutas de socket cortas solo garantizadas en Linux")
	}
	if _, err := exec.LookPath("ssh"); err != nil {
		t.Skip("sin ssh")
	}
	dir, err := os.MkdirTemp("", "lray")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CACHE_HOME", dir)

	c := New("yo@maquina.invalid", 0)
	p := c.socket()
	if !strings.HasPrefix(p, filepath.Join(dir, "lray", "ssh")) {
		t.Fatalf("socket = %q, fuera de %s", p, dir)
	}
	l, err := net.Listen("unix", p)
	if err != nil {
		t.Fatal(err)
	}
	l.(*net.UnixListener).SetUnlinkOnClose(false)
	l.Close() // queda el fichero sin nadie escuchando, como tras un reinicio

	if c.Connected() {
		t.Fatal("Connected = true con un socket muerto")
	}
	c.clearStale()
	if _, err := os.Lstat(p); !os.IsNotExist(err) {
		t.Errorf("el socket caducado sigue ahí: %v", err)
	}
}
