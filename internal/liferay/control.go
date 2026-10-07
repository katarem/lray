package liferay

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/katarem/lray/internal/logs"
)

// State es el estado de un server.
type State int

const (
	Stopped  State = iota // apagado
	Starting              // proceso vivo pero aún no responde HTTP
	Running               // responde HTTP
)

var (
	// ErrNoBundle: el workspace todavía no tiene Tomcat descargado.
	ErrNoBundle = errors.New("este server aún no tiene bundle de Liferay (falta la carpeta tomcat)")
	// ErrNotRunning: se intentó parar algo que no está encendido.
	ErrNotRunning = errors.New("el server no está encendido")
)

// PID lee el PID guardado y dice si el proceso sigue vivo.
func (l *Layout) PID() (int, bool) {
	data, err := os.ReadFile(l.PIDFile())
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return 0, false
	}
	return pid, processAlive(pid)
}

// State combina el PID con una sonda HTTP para distinguir arrancando/encendido.
func (l *Layout) State() State {
	if _, alive := l.PID(); !alive {
		return Stopped
	}
	if httpUp(l.Port()) {
		return Running
	}
	return Starting
}

// PortInUse comprueba si alguien escucha ya en ese puerto.
func PortInUse(port int) bool {
	c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 300*time.Millisecond)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

// Tomcat abre el socket pronto pero no atiende peticiones hasta terminar de
// arrancar, así que una respuesta HTTP (la que sea) significa "listo".
func httpUp(port int) bool {
	client := &http.Client{
		Timeout:       2 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Head(fmt.Sprintf("http://127.0.0.1:%d/", port))
	if err != nil {
		return false
	}
	resp.Body.Close()
	return true
}

// Start lanza Tomcat en segundo plano (independiente de la terminal).
func Start(l *Layout, javaHome string) error {
	if !l.HasBundle() {
		return ErrNoBundle
	}
	if pid, alive := l.PID(); alive {
		return fmt.Errorf("ya está en marcha (PID %d)", pid)
	}
	_ = os.Remove(l.PIDFile())
	if err := os.MkdirAll(filepath.Join(l.Tomcat, "logs"), 0o755); err != nil {
		return err
	}
	env := append(os.Environ(), "CATALINA_PID="+l.PIDFile())
	if javaHome != "" {
		env = append(env, "JAVA_HOME="+javaHome, "JRE_HOME="+javaHome)
	}
	return startTomcat(l, env)
}

// Stop para el server: primero con cortesía y, pasado timeout, a la fuerza.
// Devuelve true si hubo que forzarlo.
func Stop(l *Layout, javaHome string, timeout time.Duration) (bool, error) {
	pid, alive := l.PID()
	if !alive {
		_ = os.Remove(l.PIDFile())
		return false, ErrNotRunning
	}
	env := os.Environ()
	if javaHome != "" {
		env = append(env, "JAVA_HOME="+javaHome, "JRE_HOME="+javaHome)
	}
	if err := terminate(l, pid, env); err != nil {
		return false, err
	}
	if waitDead(pid, timeout) {
		_ = os.Remove(l.PIDFile())
		return false, nil
	}
	if err := kill(pid); err != nil {
		return true, err
	}
	waitDead(pid, 10*time.Second)
	_ = os.Remove(l.PIDFile())
	return true, nil
}

func waitDead(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			return true
		}
		time.Sleep(300 * time.Millisecond)
	}
	return !processAlive(pid)
}

// WaitReady sigue catalina.out desde offset hasta ver "Server startup in".
// progress recibe cada línea nueva (sin fecha ni nivel) para mostrarla.
func WaitReady(ctx context.Context, l *Layout, offset int64, timeout time.Duration, progress func(string)) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ready := make(chan struct{})
	var once sync.Once
	go func() {
		_ = logs.Follow(ctx, l.CatalinaOut(), offset, func(line string) {
			if strings.Contains(line, "Server startup in") {
				once.Do(func() { close(ready) })
			}
			if msg := logs.Message(line); msg != "" {
				progress(msg)
			}
		}, func() {})
	}()

	check := time.NewTicker(2 * time.Second)
	defer check.Stop()
	for {
		select {
		case <-ready:
			return nil
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return fmt.Errorf("no ha terminado de arrancar en %s; revisa los logs", timeout)
			}
			return ctx.Err()
		case <-check.C:
			if _, alive := l.PID(); !alive {
				return errors.New("el proceso de Liferay se ha detenido durante el arranque; revisa los logs")
			}
		}
	}
}
