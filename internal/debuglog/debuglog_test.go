package debuglog

import (
	"os"
	"strings"
	"testing"
)

func TestPrintfAndTail(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", dir)
	t.Setenv("HOME", dir)
	t.Setenv("LOCALAPPDATA", dir)

	Open()
	Printf("hola %s", "mundo")
	Printf("varias\nlíneas")
	Close()

	lines, err := Tail(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 || !strings.HasSuffix(lines[0], "hola mundo") || !strings.HasSuffix(lines[1], "varias ⏎ líneas") {
		t.Fatalf("Tail = %q", lines)
	}
	if fi, err := os.Stat(Path()); err != nil || fi.Mode().Perm()&0o077 != 0 && os.PathSeparator == '/' {
		t.Errorf("el log debe ser privado: %v %v", fi.Mode(), err)
	}

	// Al pasar de maxSize, rota a .1 y Tail sigue viendo lo anterior.
	if err := os.WriteFile(Path(), []byte(strings.Repeat("x", maxSize+1)+"\nvieja\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	Open()
	Printf("nueva")
	Close()
	lines, _ = Tail(2)
	if len(lines) != 2 || lines[0] != "vieja" || !strings.HasSuffix(lines[1], "nueva") {
		t.Fatalf("tras rotar, Tail = %q", lines)
	}
}
