// Package debuglog apunta en un fichero lo que hace lray (comando, errores,
// cada llamada a ssh...) para poder investigar un fallo después y mandarlo en
// un informe. Siempre está activo, porque el fallo interesante nunca se puede
// repetir con el modo depuración puesto; con SetEcho además se ve en vivo.
package debuglog

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// maxSize es lo que puede crecer el log antes de rotarlo a lray.log.1.
const maxSize = 2 << 20

var (
	mu   sync.Mutex
	file *os.File
	echo io.Writer
	pid  = os.Getpid()
)

// Path es el fichero del log: <caché del usuario>/lray/lray.log.
func Path() string {
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	return filepath.Join(base, "lray", "lray.log")
}

// Open abre el log para añadir líneas, rotándolo si ha crecido demasiado. Si
// no se puede abrir, lray sigue igual sin log.
func Open() {
	mu.Lock()
	defer mu.Unlock()
	if file != nil {
		return
	}
	p := Path()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return
	}
	if fi, err := os.Stat(p); err == nil && fi.Size() > maxSize {
		_ = os.Rename(p, p+".1")
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	file = f
}

// SetEcho copia además cada línea en w (nil para dejar de hacerlo).
func SetEcho(w io.Writer) {
	mu.Lock()
	echo = w
	mu.Unlock()
}

// Close cierra el log.
func Close() {
	mu.Lock()
	defer mu.Unlock()
	if file != nil {
		_ = file.Close()
		file = nil
	}
}

// Printf apunta una línea con la hora y el PID (para separar ejecuciones a la
// vez). Los saltos de línea del mensaje se aplanan.
func Printf(format string, args ...any) {
	mu.Lock()
	defer mu.Unlock()
	if file == nil && echo == nil {
		return
	}
	msg := strings.ReplaceAll(strings.TrimRight(fmt.Sprintf(format, args...), "\n"), "\n", " ⏎ ")
	line := fmt.Sprintf("%s [%d] %s\n", time.Now().Format("2006-01-02 15:04:05.000"), pid, msg)
	if file != nil {
		_, _ = file.WriteString(line)
	}
	if echo != nil {
		_, _ = io.WriteString(echo, "debug: "+line)
	}
}

// Tail devuelve las últimas n líneas del log, tirando de lray.log.1 si hace
// falta.
func Tail(n int) ([]string, error) {
	p := Path()
	var lines []string
	for _, f := range []string{p + ".1", p} {
		got, err := readLines(f)
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		lines = append(lines, got...)
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines, nil
}

func readLines(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return nil, err
	}
	return strings.Split(strings.TrimRight(string(data), "\n"), "\n"), nil
}
