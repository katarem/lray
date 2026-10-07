package liferay

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// Gradlew devuelve la ruta del wrapper del workspace.
func Gradlew(ws string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(ws, "gradlew.bat")
	}
	return filepath.Join(ws, "gradlew")
}

// RunGradle ejecuta el wrapper en dir (que decide qué proyecto/s se construyen,
// igual que hace Gradle al lanzarlo desde una subcarpeta). Cada línea de salida
// llega a onLine y se devuelven las últimas líneas para diagnosticar fallos.
func RunGradle(ctx context.Context, ws, dir, javaHome string, args []string, onLine func(string)) ([]string, error) {
	gw := Gradlew(ws)
	if _, err := os.Stat(gw); err != nil {
		return nil, fmt.Errorf("no encuentro el wrapper de Gradle en %s", gw)
	}
	MakeExecutable(gw)

	cmd := exec.CommandContext(ctx, gw, append(args, "--console=plain")...)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	if javaHome != "" {
		cmd.Env = append(cmd.Env, "JAVA_HOME="+javaHome)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	const keep = 400
	tail := make([]string, 0, keep)
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if len(tail) == keep {
			copy(tail, tail[1:])
			tail = tail[:keep-1]
		}
		tail = append(tail, line)
		onLine(line)
	}
	return tail, cmd.Wait()
}
