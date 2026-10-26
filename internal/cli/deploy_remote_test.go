package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestChangedFiles(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if snap, err := snapshotDir(filepath.Join(dir, "no-existe")); err != nil || len(snap) != 0 {
		t.Fatalf("carpeta que no existe = %v, %v", snap, err)
	}

	write("osgi/modules/a.jar", "a1")
	write("osgi/modules/b.jar", "b1")
	before, err := snapshotDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	write("osgi/modules/a.jar", "a1") // Gradle lo vuelve a copiar igual: no cuenta
	write("osgi/modules/b.jar", "b2")
	write("osgi/war/tema.war", "w")
	after, err := snapshotDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join("osgi", "modules", "b.jar"), filepath.Join("osgi", "war", "tema.war")}
	if got := changedFiles(before, after, time.Time{}); !reflect.DeepEqual(got, want) {
		t.Errorf("changedFiles = %v, quería %v", got, want)
	}

	// Con --clean cuenta todo lo escrito desde el principio, aunque sea igual.
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(dir, "osgi", "war", "tema.war"), old, old); err != nil {
		t.Fatal(err)
	}
	since := time.Now().Add(-time.Minute)
	after, err = snapshotDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	want = []string{filepath.Join("osgi", "modules", "a.jar"), filepath.Join("osgi", "modules", "b.jar")}
	if got := changedFiles(after, after, since); !reflect.DeepEqual(got, want) {
		t.Errorf("changedFiles con since = %v, quería %v", got, want)
	}
}
