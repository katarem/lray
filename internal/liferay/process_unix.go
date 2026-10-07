//go:build !windows

package liferay

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// SIGTERM dispara el shutdown hook de Tomcat: parada ordenada sin depender
// del puerto de shutdown (8005), que a veces está desactivado.
func terminate(_ *Layout, pid int, _ []string) error {
	return syscall.Kill(pid, syscall.SIGTERM)
}

func kill(pid int) error { return syscall.Kill(pid, syscall.SIGKILL) }

func startTomcat(l *Layout, env []string) error {
	bin := filepath.Join(l.Tomcat, "bin")
	ensureExecutable(bin)
	cmd := exec.Command(filepath.Join(bin, "catalina.sh"), "start")
	cmd.Dir = bin
	cmd.Env = env
	// Grupo de procesos propio: un Ctrl+C en la terminal no mata a Tomcat.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("catalina.sh start ha fallado: %w\n%s", err, out)
	}
	return nil
}

// Algunos bundles descomprimidos pierden el bit de ejecución de los .sh.
func ensureExecutable(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".sh") {
			continue
		}
		p := filepath.Join(dir, e.Name())
		if st, err := os.Stat(p); err == nil && st.Mode()&0o111 == 0 {
			_ = os.Chmod(p, st.Mode()|0o755)
		}
	}
}

// MakeExecutable da permisos de ejecución a un fichero concreto.
func MakeExecutable(p string) {
	if st, err := os.Stat(p); err == nil && st.Mode()&0o111 == 0 {
		_ = os.Chmod(p, st.Mode()|0o755)
	}
}
