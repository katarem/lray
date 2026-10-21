package shellcomp

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeHome deja HOME y las carpetas XDG dentro de un directorio temporal.
func fakeHome(t *testing.T, shell string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("ZDOTDIR", "")
	t.Setenv("SHELL", shell)
	return home
}

func fakeGen(name string) func(io.Writer) error {
	return func(w io.Writer) error {
		_, err := fmt.Fprintf(w, "# script %s de lray\n", name)
		return err
	}
}

var gens = Generators{Bash: fakeGen("bash"), Zsh: fakeGen("zsh"), Fish: fakeGen("fish"), PowerShell: fakeGen("ps")}

func TestInstallIdempotent(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("las rutas de arranque cambian en macOS y Windows")
	}
	home := fakeHome(t, "/bin/bash")
	bashrc := filepath.Join(home, ".bashrc")
	before := "export PATH=$PATH:/opt/bin\nsource <(lray completion bash)\n# lray completion zsh (comentada)\nalias ll='ls -l'"
	if err := os.WriteFile(bashrc, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := Install("v1.0.0", gens)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(res.Shells, ",") != "bash" || len(res.RCs) != 1 || res.RCs[0] != bashrc {
		t.Fatalf("res = %+v", res)
	}
	got, _ := os.ReadFile(bashrc)
	text := string(got)
	for _, want := range []string{
		"export PATH=$PATH:/opt/bin\n",
		legacyNote + "source <(lray completion bash)\n",
		"\n# lray completion zsh (comentada)\n",
		"alias ll='ls -l'\n\n" + begin + "\n",
		filepath.Join(home, ".local", "share", "lray", "completion", "lray.bash"),
	} {
		if !strings.Contains(text, want) {
			t.Errorf("falta %q en:\n%s", want, text)
		}
	}
	if fi, _ := os.Stat(bashrc); fi.Mode().Perm() != 0o600 {
		t.Errorf("permisos = %v, se han perdido", fi.Mode().Perm())
	}
	if Stale("v1.0.0") || !Stale("v1.1.0") {
		t.Error("la marca de versión no se ha guardado")
	}

	// Otra vez: no cambia nada ni duplica el bloque.
	res, err = Install("v1.0.0", gens)
	if err != nil || len(res.RCs) != 0 {
		t.Fatalf("segunda vez: res = %+v, err = %v", res, err)
	}
	again, _ := os.ReadFile(bashrc)
	if string(again) != text {
		t.Errorf("la segunda instalación ha cambiado el fichero:\n%s", again)
	}
	if n := strings.Count(string(again), begin); n != 1 {
		t.Errorf("el bloque aparece %d veces", n)
	}
}

// Con zsh por defecto se crea su .zshrc aunque no exista; bash, sin .bashrc,
// no se toca. Fish se instala si tiene su carpeta.
func TestInstallPicksShells(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("las rutas de arranque cambian en macOS y Windows")
	}
	home := fakeHome(t, "/usr/bin/zsh")
	if err := os.MkdirAll(filepath.Join(home, ".config", "fish"), 0o755); err != nil {
		t.Fatal(err)
	}
	res, err := Install("v1.0.0", gens)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(res.Shells, ",") != "zsh,fish" {
		t.Errorf("shells = %v", res.Shells)
	}
	if _, err := os.Stat(filepath.Join(home, ".bashrc")); !os.IsNotExist(err) {
		t.Error("ha creado un .bashrc que no existía")
	}
	zshrc, _ := os.ReadFile(filepath.Join(home, ".zshrc"))
	if !strings.Contains(string(zshrc), "compinit") || !strings.HasPrefix(string(zshrc), begin) {
		t.Errorf(".zshrc =\n%s", zshrc)
	}
	fish, err := os.ReadFile(filepath.Join(home, ".config", "fish", "completions", "lray.fish"))
	if err != nil || !strings.Contains(string(fish), "script fish") {
		t.Errorf("lray.fish = %q, %v", fish, err)
	}
}

// Un bloque viejo (otra ruta) se sustituye en su sitio, y si el .bashrc es un
// enlace (dotfiles) se escribe en el fichero enlazado sin romper el enlace.
func TestInstallReplacesBlockThroughSymlink(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("las rutas de arranque cambian en macOS y Windows")
	}
	home := fakeHome(t, "/bin/bash")
	real := filepath.Join(home, "dotfiles", "bashrc")
	if err := os.MkdirAll(filepath.Dir(real), 0o755); err != nil {
		t.Fatal(err)
	}
	old := "antes\n" + begin + "\n. /ruta/vieja\n" + end + "\ndespués\n"
	if err := os.WriteFile(real, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(home, ".bashrc")); err != nil {
		t.Fatal(err)
	}
	if _, err := Install("v1.0.0", gens); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(filepath.Join(home, ".bashrc")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("el enlace .bashrc se ha sustituido por un fichero")
	}
	got, _ := os.ReadFile(real)
	text := string(got)
	if strings.Contains(text, "/ruta/vieja") || !strings.HasPrefix(text, "antes\n"+begin) || !strings.HasSuffix(text, end+"\ndespués\n") {
		t.Errorf("bloque mal sustituido:\n%s", text)
	}
}
