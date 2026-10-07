//go:build windows

package liferay

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

func processAlive(pid int) bool {
	out, err := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/NH").Output()
	return err == nil && strings.Contains(string(out), strconv.Itoa(pid))
}

func terminate(l *Layout, _ int, env []string) error {
	cmd := exec.Command("cmd.exe", "/C", filepath.Join(l.Tomcat, "bin", "catalina.bat"), "stop")
	cmd.Dir = filepath.Join(l.Tomcat, "bin")
	cmd.Env = env
	_ = cmd.Run() // si falla, Stop acabará forzando con taskkill
	return nil
}

func kill(pid int) error {
	return exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(pid)).Run()
}

// En Windows "catalina.bat start" abre otra ventana, así que lanzamos
// "catalina.bat run" desacoplado y redirigimos la salida a catalina.out.
func startTomcat(l *Layout, env []string) error {
	out, err := os.OpenFile(l.CatalinaOut(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()
	bin := filepath.Join(l.Tomcat, "bin")
	cmd := exec.Command("cmd.exe", "/C", filepath.Join(bin, "catalina.bat"), "run")
	cmd.Dir = bin
	cmd.Env = env
	cmd.Stdout, cmd.Stderr = out, out
	const createNewProcessGroup, createNoWindow = 0x00000200, 0x08000000
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewProcessGroup | createNoWindow}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("no he podido lanzar catalina.bat: %w", err)
	}
	if err := os.WriteFile(l.PIDFile(), []byte(strconv.Itoa(cmd.Process.Pid)), 0o644); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// MakeExecutable no aplica en Windows.
func MakeExecutable(string) {}
