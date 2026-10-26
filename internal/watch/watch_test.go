package watch

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestMatcher(t *testing.T) {
	m := NewMatcher(append(DefaultIgnore, `custom\gen`)...)
	cases := map[string]bool{
		"bundles":                            true,
		"modules/bundles":                    false, // sin **/ solo vale en la raíz
		"build":                              true,
		"modules/foo/build":                  true,
		"modules/foo/build.gradle":           false,
		"modules/foo/node_modules":           true,
		"modules/foo/src/main/.view.jsp.swp": true,
		"modules/foo/src/main/view.jsp~":     true,
		".git":                               true,
		"custom/gen":                         true,
		"modules/foo/src":                    false,
	}
	for rel, want := range cases {
		if got := m.Match(rel); got != want {
			t.Errorf("Match(%q) = %v, quiero %v", rel, got, want)
		}
	}
	fast := NewMatcher(DefaultFast...)
	for rel, want := range map[string]bool{
		"a.css": true,
		"modules/foo/src/main/resources/META-INF/resources/view.jsp": true,
		"modules/foo/src/main/java/A.java":                           false,
	} {
		if got := fast.Match(rel); got != want {
			t.Errorf("fast.Match(%q) = %v, quiero %v", rel, got, want)
		}
	}
}

func snap(paths ...string) Snapshot {
	s := Snapshot{}
	for _, p := range paths {
		s[p] = Entry{}
	}
	return s
}

func TestProjects(t *testing.T) {
	s := snap("modules/a/src", "modules/a/src/main/x/src", "modules/b/src", "client-extensions/c/client-extension.yaml",
		"modules/a-b/src", "README.md")
	got := Projects(s, DefaultMarkers)
	want := []string{"client-extensions/c", "modules/a", "modules/a-b", "modules/b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Projects = %v, quiero %v", got, want)
	}
	if got := Projects(snap("src", "sub/src"), DefaultMarkers); !reflect.DeepEqual(got, []string{""}) {
		t.Fatalf("con la raíz como proyecto = %v", got)
	}
}

func TestPlan(t *testing.T) {
	projects := []string{"modules/a", "modules/b", "modules/c"}
	changes := []Change{
		{"build.gradle", Modified},
		{"modules/a/src/main/resources/view.jsp", Modified},
		{"modules/a/src/main/resources/main.scss", Created},
		{"modules/b/src/main/java/B.java", Modified},
		{"modules/b/src/main/resources/b.js", Modified},
		{"modules/c/src/main/java/Old.java", Deleted},
	}
	got := Plan(changes, projects, NewMatcher(DefaultFast...))
	want := map[string][]string{
		"":          {"deploy", "-a"},
		"modules/a": {"deployFast", "-a"},
		"modules/b": {"deploy", "-a"},
		"modules/c": {"clean", "deploy"},
	}
	if len(got) != len(want) {
		t.Fatalf("Plan da %d pasos: %+v", len(got), got)
	}
	for _, s := range got {
		if !reflect.DeepEqual(s.Tasks, want[s.Project]) {
			t.Errorf("%q: tareas %v, quiero %v", s.Project, s.Tasks, want[s.Project])
		}
	}
	if got[0].Project != "" || len(got[1].Changes) != 2 {
		t.Errorf("orden o agrupación inesperados: %+v", got)
	}
}

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestWatcher(t *testing.T) {
	root := t.TempDir()
	write(t, root, "settings.gradle", "x")
	write(t, root, "modules/a/src/main/java/A.java", "a")
	write(t, root, "modules/b/src/main/resources/view.jsp", "b")
	write(t, root, "modules/b/build/libs/b.jar", "jar")

	w, err := New(root, Options{Ignore: NewMatcher(DefaultIgnore...), Fast: NewMatcher(DefaultFast...), Markers: DefaultMarkers})
	if err != nil {
		t.Fatal(err)
	}
	if got := w.Projects(); !reflect.DeepEqual(got, []string{"modules/a", "modules/b"}) {
		t.Fatalf("Projects = %v", got)
	}
	if steps, _ := w.Poll(); steps != nil {
		t.Fatalf("sin cambios no debería haber pasos: %+v", steps)
	}

	write(t, root, "modules/b/src/main/resources/view.jsp", "bb")
	write(t, root, "modules/b/build/libs/b.jar", "otro jar") // ignorado
	write(t, root, "modules/a/src/main/java/.A.java.swp", "swap")
	steps, err := w.Poll()
	if err != nil || steps != nil {
		t.Fatalf("la primera pasada con cambios espera a que se calmen: %+v %v", steps, err)
	}
	// Un temporal que aparece y desaparece antes de entregar no cuenta.
	write(t, root, "modules/a/src/main/java/A.java.tmp", "t")
	if steps, _ := w.Poll(); steps != nil {
		t.Fatalf("seguía cambiando: %+v", steps)
	}
	if err := os.Remove(filepath.Join(root, "modules/a/src/main/java/A.java.tmp")); err != nil {
		t.Fatal(err)
	}
	if steps, _ := w.Poll(); steps != nil {
		t.Fatalf("seguía cambiando: %+v", steps)
	}
	steps, err = w.Poll()
	if err != nil {
		t.Fatal(err)
	}
	want := []Step{{Project: "modules/b", Tasks: []string{"deployFast", "-a"},
		Changes: []Change{{"modules/b/src/main/resources/view.jsp", Modified}}}}
	if !reflect.DeepEqual(steps, want) {
		t.Fatalf("Poll = %+v, quiero %+v", steps, want)
	}
	if steps, _ := w.Poll(); steps != nil {
		t.Fatalf("ya se había entregado: %+v", steps)
	}

	// Un módulo borrado entero sigue siendo su proyecto, no la raíz.
	if err := os.RemoveAll(filepath.Join(root, "modules/a")); err != nil {
		t.Fatal(err)
	}
	w.Poll()
	steps, _ = w.Poll()
	if len(steps) != 1 || steps[0].Project != "modules/a" || steps[0].Tasks[0] != "clean" {
		t.Fatalf("borrar un módulo: %+v", steps)
	}

	// Mismo tamaño, otra fecha: también es un cambio.
	p := filepath.Join(root, "settings.gradle")
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(p, later, later); err != nil {
		t.Fatal(err)
	}
	w.Poll()
	steps, _ = w.Poll()
	if len(steps) != 1 || steps[0].Project != "" || steps[0].Tasks[0] != "deploy" {
		t.Fatalf("cambio en la raíz: %+v", steps)
	}
}
