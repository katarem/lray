// Package ssh habla con máquinas remotas a través del cliente ssh del sistema,
// así se respeta tal cual ~/.ssh/config (alias, ProxyJump, claves, agente) y
// known_hosts. Reutiliza una única conexión por máquina (ControlMaster): la
// contraseña se pide una vez y los comandos siguientes van por esa conexión
// mientras siga viva.
package ssh

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	// ErrUnreachable: no se llega a la máquina (sin red, sin VPN, nombre que no resuelve...).
	ErrUnreachable = errors.New("no llego a la máquina")
	// ErrAuth: la máquina responde pero no acepta las credenciales.
	ErrAuth = errors.New("la máquina no ha aceptado el acceso")
	// ErrNeedsLogin: hace falta contraseña y no hay terminal para pedirla.
	ErrNeedsLogin = errors.New("hace falta iniciar sesión y no hay terminal para pedir la contraseña")
)

// DefaultPersist es cuánto sigue viva la conexión sin usarse.
const DefaultPersist = "4h"

// Conn es una máquina remota a la que se llega por SSH.
type Conn struct {
	Dest string // usuario@máquina o alias de ~/.ssh/config
	Port int    // 0 = el de ~/.ssh/config o el 22
}

// New prepara la conexión (no conecta todavía).
func New(dest string, port int) *Conn { return &Conn{Dest: dest, Port: port} }

// ValidDest comprueba que el destino sea algo que ssh entienda como máquina
// y no como opción.
func ValidDest(dest string) error {
	if dest == "" || strings.HasPrefix(dest, "-") || strings.ContainsAny(dest, " \t\r\n'\"`$;|&<>") {
		return fmt.Errorf("destino SSH no válido %q: usa usuario@máquina o un alias de ~/.ssh/config", dest)
	}
	return nil
}

// multiplex indica si se puede reutilizar la conexión. El OpenSSH de Windows
// no soporta ControlMaster: allí cada comando abre su propia conexión.
func multiplex() bool { return runtime.GOOS != "windows" }

// Persist es cuánto sigue viva la conexión compartida sin usarse
// (LRAY_SSH_PERSIST o DefaultPersist), en formato de ssh: 30m, 4h...
func Persist() string {
	if p := os.Getenv("LRAY_SSH_PERSIST"); p != "" {
		return p
	}
	return DefaultPersist
}

// controlPath es el socket de la conexión compartida. %C es un hash del
// destino, así la ruta no pasa del límite de los sockets Unix.
func controlPath() string {
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	dir := filepath.Join(base, "lray", "ssh")
	_ = os.MkdirAll(dir, 0o700)
	return filepath.Join(dir, "%C")
}

// opts son las opciones comunes. batch evita que ssh pregunte nada.
func (c *Conn) opts(batch bool, master string) []string {
	o := []string{
		"-o", "ConnectTimeout=8",
		"-o", "ServerAliveInterval=15",
		"-o", "ServerAliveCountMax=3",
	}
	if batch {
		o = append(o, "-o", "BatchMode=yes")
	}
	if c.Port != 0 {
		o = append(o, "-p", strconv.Itoa(c.Port))
	}
	if multiplex() {
		o = append(o, "-o", "ControlPath="+controlPath(), "-o", "ControlMaster="+master)
		if master != "no" {
			o = append(o, "-o", "ControlPersist="+Persist())
		}
	}
	return o
}

func (c *Conn) command(ctx context.Context, args ...string) *exec.Cmd {
	if ctx == nil {
		ctx = context.Background()
	}
	return exec.CommandContext(ctx, "ssh", args...)
}

// Connected dice si hay una conexión compartida viva.
func (c *Conn) Connected() bool {
	if !multiplex() {
		return false
	}
	args := []string{"-o", "ControlPath=" + controlPath(), "-O", "check"}
	if c.Port != 0 {
		args = append(args, "-p", strconv.Itoa(c.Port))
	}
	return c.command(context.Background(), append(args, "--", c.Dest)...).Run() == nil
}

// Connect deja abierta la conexión compartida. Primero lo intenta sin
// preguntar (claves, agente); si la máquina pide contraseña y interactive es
// true, ssh la pide en la terminal (lray nunca la ve). before se llama justo
// antes de que ssh pueda preguntar.
func (c *Conn) Connect(ctx context.Context, interactive bool, before func()) error {
	if !multiplex() || c.Connected() {
		return nil
	}
	err := c.master(ctx, true)
	if err == nil || errors.Is(err, ErrUnreachable) {
		return err
	}
	if !interactive {
		if errors.Is(err, ErrAuth) {
			return ErrNeedsLogin
		}
		return err
	}
	if before != nil {
		before()
	}
	return c.master(ctx, false)
}

// master abre la conexión compartida en segundo plano (-f -N). stderr va a un
// fichero y no a una tubería: el proceso que se queda en segundo plano la
// heredaría y nos dejaría esperando para siempre.
func (c *Conn) master(ctx context.Context, batch bool) error {
	errf, err := os.CreateTemp("", "lray-ssh-*.log")
	if err != nil {
		return err
	}
	defer os.Remove(errf.Name())
	defer errf.Close()

	args := append(c.opts(batch, "yes"), "-f", "-N", "--", c.Dest)
	cmd := c.command(ctx, args...)
	cmd.Stderr = errf
	if !batch {
		cmd.Stdin = os.Stdin // ssh pregunta por /dev/tty, pero alguna variante mira stdin
	}
	runErr := cmd.Run()
	if runErr == nil {
		return nil
	}
	msg, _ := os.ReadFile(errf.Name())
	return classify(c.Dest, runErr, msg)
}

// Disconnect cierra la conexión compartida, si la hay.
func (c *Conn) Disconnect() error {
	if !c.Connected() {
		return nil
	}
	args := []string{"-o", "ControlPath=" + controlPath(), "-O", "exit"}
	if c.Port != 0 {
		args = append(args, "-p", strconv.Itoa(c.Port))
	}
	return c.command(context.Background(), append(args, "--", c.Dest)...).Run()
}

// Run ejecuta script con sh en la máquina remota y devuelve su salida.
// Los args llegan al script como $1, $2...
func (c *Conn) Run(ctx context.Context, script string, args ...string) ([]byte, error) {
	var out, errb bytes.Buffer
	cmd := c.command(ctx, append(c.opts(true, "no"), "--", c.Dest, Remote(script, args...))...)
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		if ctx != nil && ctx.Err() != nil {
			return out.Bytes(), ctx.Err()
		}
		return out.Bytes(), classify(c.Dest, err, errb.Bytes())
	}
	return out.Bytes(), nil
}

// Stream ejecuta script y devuelve su salida según llega. La entrada estándar
// del script se queda abierta mientras el lector siga abierto: al cerrarlo, el
// script lo nota (EOF) y puede limpiar lo que dejó corriendo. Close devuelve
// el error del script, si lo hubo.
func (c *Conn) Stream(ctx context.Context, script string, args ...string) (io.ReadCloser, error) {
	cmd := c.command(ctx, append(c.opts(true, "no"), "--", c.Dest, Remote(script, args...))...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	s := &stream{cmd: cmd, stdin: stdin, stdout: stdout, dest: c.Dest}
	cmd.Stderr = &s.errb
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return s, nil
}

type stream struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	errb   bytes.Buffer
	dest   string
	once   sync.Once
	err    error
}

func (s *stream) Read(p []byte) (int, error) { return s.stdout.Read(p) }

func (s *stream) Close() error {
	s.once.Do(func() {
		_ = s.stdin.Close()
		done := make(chan error, 1)
		go func() { done <- s.cmd.Wait() }()
		var err error
		select {
		case err = <-done:
		case <-time.After(3 * time.Second):
			_ = s.cmd.Process.Kill()
			<-done
			return // lo hemos cortado nosotros: no es un error del script
		}
		if err != nil {
			s.err = classify(s.dest, err, s.errb.Bytes())
		}
	})
	return s.err
}

// Interactive ejecuta command con terminal (ssh -t), conectado a la tuya: lo
// que pregunte (p. ej. la contraseña de sudo) te lo pregunta a ti.
func (c *Conn) Interactive(ctx context.Context, command string) error {
	args := append(c.opts(false, "no"), "-t", "--", c.Dest, command)
	cmd := c.command(ctx, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() != 255 {
			return fmt.Errorf("el comando ha terminado con código %d", ee.ExitCode())
		}
		return classify(c.Dest, err, nil)
	}
	return nil
}

// Remote monta la línea que ssh manda a la máquina: sh -c 'script' lray args...
// Todo va entrecomillado, así ninguna ruta se interpreta como comando.
func Remote(script string, args ...string) string {
	var b strings.Builder
	b.WriteString("sh -c ")
	b.WriteString(Quote(script))
	b.WriteString(" lray")
	for _, a := range args {
		b.WriteByte(' ')
		b.WriteString(Quote(a))
	}
	return b.String()
}

// Quote protege s para el sh remoto.
func Quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// classify traduce los fallos de ssh (código 255) a errores con sentido.
func classify(dest string, err error, stderr []byte) error {
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() != 255 {
		if msg := lastLine(stderr); msg != "" {
			return fmt.Errorf("%s (código %d)", msg, ee.ExitCode())
		}
		return fmt.Errorf("el comando remoto ha terminado con código %d", ee.ExitCode())
	}
	if errors.Is(err, exec.ErrNotFound) {
		return errors.New("no encuentro el comando ssh; instala OpenSSH")
	}
	msg := string(stderr)
	low := strings.ToLower(msg)
	for _, s := range []string{
		"could not resolve", "name or service not known", "nodename nor servname",
		"timed out", "no route to host", "connection refused", "network is unreachable",
		"temporary failure in name resolution",
	} {
		if strings.Contains(low, s) {
			return fmt.Errorf("%w %s (%s)", ErrUnreachable, dest, lastLine(stderr))
		}
	}
	if strings.Contains(low, "permission denied") || strings.Contains(low, "too many authentication failures") {
		return fmt.Errorf("%w (%s)", ErrAuth, lastLine(stderr))
	}
	if l := lastLine(stderr); l != "" {
		return fmt.Errorf("ssh %s: %s", dest, l)
	}
	return fmt.Errorf("ssh %s: %w", dest, err)
}

func lastLine(b []byte) string {
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}
