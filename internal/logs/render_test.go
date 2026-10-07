package logs

import (
	"reflect"
	"strings"
	"testing"
)

func group(lines ...string) []*Entry {
	g := NewGrouper()
	var out []*Entry
	for _, l := range lines {
		if e := g.Add(l); e != nil {
			out = append(out, e)
		}
	}
	if e := g.Flush(); e != nil {
		out = append(out, e)
	}
	return out
}

func TestGrouper(t *testing.T) {
	entries := group(
		"2025-06-01 10:00:00.123 ERROR [http-nio-8080-exec-1][PortletServlet:123] Fallo al pintar",
		"java.lang.NullPointerException: vacío",
		"\tat com.tienda.Foo.bar(Foo.java:10)",
		"Caused by: java.lang.IllegalStateException: raíz",
		"\t... 12 more",
		"hola desde System.out",
		"",
		"01-Jun-2025 10:00:01.000 INFO [main] org.apache.catalina.startup.Catalina.start Server startup in [1234] milliseconds",
	)
	if len(entries) != 4 {
		t.Fatalf("quiero 4 entradas, hay %d: %+v", len(entries), entries)
	}
	if e := entries[0]; e.Level != LevelError || e.Where != "[http-nio-8080-exec-1][PortletServlet:123]" || e.Msg != "Fallo al pintar" || len(e.Extra) != 4 {
		t.Errorf("entrada de error mal agrupada: %+v", e)
	}
	if e := entries[1]; e.Parsed() || e.Msg != "hola desde System.out" || e.Level != LevelError {
		t.Errorf("System.out debería ir aparte heredando el nivel: %+v", e)
	}
	if e := entries[3]; e.Level != LevelInfo || !e.Parsed() {
		t.Errorf("línea de Tomcat mal leída: %+v", e)
	}
}

func TestFindJSON(t *testing.T) {
	tests := []struct {
		in, prefix, js string
		ok             bool
	}{
		{`Respuesta: {"id":1,"tags":["a"]}`, "Respuesta: ", `{"id":1,"tags":["a"]}`, true},
		{`[{"a":1},{"a":2}]`, "", `[{"a":1},{"a":2}]`, true},
		{`STARTED com.tienda.web_1.0.0 [1234]`, "", "", false},
		{`[main] algo {}`, "", "", false},
		{`roto {"a":1`, "", "", false},
		{`[x] {"a":1} `, "[x] ", `{"a":1}`, true},
	}
	for _, tt := range tests {
		p, js, ok := findJSON(tt.in)
		if ok != tt.ok || p != tt.prefix || js != tt.js {
			t.Errorf("findJSON(%q) = %q, %q, %v; quiero %q, %q, %v", tt.in, p, js, ok, tt.prefix, tt.js, tt.ok)
		}
	}
}

func TestRenderJSON(t *testing.T) {
	line := `2025-06-01 10:00:00.123 INFO  [main][Api:1] Respuesta: {"id":1,"name":"tienda"}`
	e := group(line)[0]

	b := NewRenderer(Options{JSON: JSONPretty}).Render(e)
	if b.Head != `2025-06-01 10:00:00.123 INFO  [main][Api:1] Respuesta:` {
		t.Errorf("cabecera = %q", b.Head)
	}
	want := []string{"  {", `    "id": 1,`, `    "name": "tienda"`, "  }"}
	if !reflect.DeepEqual(b.Body, want) {
		t.Errorf("cuerpo = %q; quiero %q", b.Body, want)
	}
	if !b.Collapsible() || !strings.Contains(b.Summary, `{"id":1,"name":"tienda"}`) || !strings.Contains(b.Summary, "4 líneas") {
		t.Errorf("resumen = %q", b.Summary)
	}

	if b := NewRenderer(Options{JSON: JSONRaw}).Render(e); b.Head != line || len(b.Body) != 0 {
		t.Errorf("raw no debería tocar nada: %+v", b)
	}

	// JSON ya indentado en varias líneas: compact lo junta en la cabecera.
	e = group(`2025-06-01 10:00:00.123 DEBUG [main][Api:1] Petición:`, "{", `  "a": 1,`, `  "b": [1, 2]`, "}")[0]
	b = NewRenderer(Options{JSON: JSONCompact}).Render(e)
	if b.Head != `2025-06-01 10:00:00.123 DEBUG [main][Api:1] Petición: {"a":1,"b":[1,2]}` || len(b.Body) != 0 {
		t.Errorf("compact = %+v", b)
	}

	// Objeto pequeño: no merece varias líneas.
	e = group(`2025-06-01 10:00:00.123 INFO  [main][Api:1] ok {"a": 1}`)[0]
	if b := NewRenderer(Options{JSON: JSONPretty}).Render(e); b.Head != `2025-06-01 10:00:00.123 INFO  [main][Api:1] ok {"a":1}` || len(b.Body) != 0 {
		t.Errorf("objeto pequeño = %+v", b)
	}
}

func TestRenderStackSummary(t *testing.T) {
	e := group(
		"2025-06-01 10:00:00.123 ERROR [main][X:1] Fallo",
		"java.lang.RuntimeException: arriba",
		"\tat a.B.c(B.java:1)",
		"Caused by: java.io.IOException: abajo",
		"\tat a.B.d(B.java:2)",
	)[0]
	b := NewRenderer(Options{}).Render(e)
	if want := "  ▸ java.lang.RuntimeException: arriba  ⤷ java.io.IOException: abajo · 4 líneas"; b.Summary != want {
		t.Errorf("resumen = %q; quiero %q", b.Summary, want)
	}
	if len(b.Body) != 4 || b.Head != e.Raw {
		t.Errorf("bloque = %+v", b)
	}
}

func TestFilter(t *testing.T) {
	f, err := ParseFilter("warn", "")
	if err != nil || f.Allows(LevelInfo) || !f.Allows(LevelError) || f.String() != "≥ warn" {
		t.Errorf("--level warn: %+v %v", f, err)
	}
	f, err = ParseFilter("warn", "error, debug")
	if err != nil || !f.Allows(LevelDebug) || f.Allows(LevelWarn) || f.String() != "solo error, debug" {
		t.Errorf("--only manda sobre --level: %+v %v", f, err)
	}
	if _, err := ParseFilter("", "error,mucho"); err == nil {
		t.Error("--only con un nivel desconocido debería fallar")
	}
	if f, _ := ParseFilter("", ""); f.String() != "todo" || !f.Allows(LevelTrace) {
		t.Errorf("sin filtro: %+v", f)
	}
}

func TestColorJSON(t *testing.T) {
	var sb strings.Builder
	colorJSON(&sb, `  "k": "v", "n": -1.5e3, "b": true`)
	got := sb.String()
	for _, want := range []string{cyan + `"k"`, green + `"v"`, yellow + `-1.5e3`, magenta + `true`} {
		if !strings.Contains(got, want) {
			t.Errorf("falta %q en %q", want, got)
		}
	}
}
