package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// El autocompletado instalado tiene que cargarse de verdad en una shell nueva
// (lo que hace la terminal al abrirse) y registrar lray.
func TestInstalledCompletionLoads(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("las rutas de arranque cambian en macOS y Windows")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("ZDOTDIR", "")
	t.Setenv("SHELL", "/bin/bash")
	for _, rc := range []string{".bashrc", ".zshrc"} {
		if err := os.WriteFile(filepath.Join(home, rc), []byte("# mío\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	rcs := installCompletions(newRoot("v1.0.0"), "v1.0.0")
	if len(rcs) != 2 {
		t.Fatalf("ficheros de arranque tocados = %v", rcs)
	}

	checks := map[string]string{
		"bash": `source "$HOME/.bashrc"; complete -p lray`,
		"zsh":  `source "$HOME/.zshrc"; print -r -- $_comps[lray]`,
	}
	for sh, script := range checks {
		if _, err := exec.LookPath(sh); err != nil {
			t.Logf("sin %s, me lo salto", sh)
			continue
		}
		out, err := exec.Command(sh, "-c", script).CombinedOutput()
		if err != nil || !strings.Contains(string(out), "lray") {
			t.Errorf("%s no registra lray: %v\n%s", sh, err, out)
		}
	}
}

func TestCompletionCommandIsGone(t *testing.T) {
	root := newRoot("v1.0.0")
	if c, _, err := root.Find([]string{"completion"}); err == nil && c != root {
		t.Errorf("sigue existiendo el comando %q", c.Name())
	}
}
