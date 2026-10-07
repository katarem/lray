package logview

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/katarem/lray/internal/logs"
)

func entries(lines ...string) entriesMsg {
	g := logs.NewGrouper()
	var out entriesMsg
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

func send(m *model, msgs ...tea.Msg) {
	for _, msg := range msgs {
		m.Update(msg)
	}
}

func key(k string) tea.Msg {
	switch k {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
}

func screen(m *model) string { return ansi.Strip(m.View()) }

var stack = []string{
	"2025-06-01 10:00:00.000 ERROR [main][X:1] Fallo",
	"java.lang.RuntimeException: arriba",
	"\tat a.B.c(B.java:1)",
	"\tat a.B.d(B.java:2)",
}

func TestCollapseAndExpand(t *testing.T) {
	m := newModel(Config{Title: "logs de «t»"})
	send(m, tea.WindowSizeMsg{Width: 100, Height: 12},
		entries(append([]string{"2025-06-01 09:59:59.000 INFO  [main][X:1] Arrancando"}, stack...)...))

	s := screen(m)
	if !strings.Contains(s, "▸ java.lang.RuntimeException: arriba · 3 líneas") || strings.Contains(s, "a.B.d") {
		t.Fatalf("el stack trace debería empezar plegado:\n%s", s)
	}
	if !strings.Contains(s, "● en vivo") || !strings.Contains(s, "2 entradas") {
		t.Errorf("barra de estado:\n%s", s)
	}

	send(m, key("enter")) // la seleccionada es la última (se sigue el log)
	if s := screen(m); !strings.Contains(s, "\tat a.B.d") && !strings.Contains(s, "at a.B.d(B.java:2)") {
		t.Fatalf("Enter debería desplegar:\n%s", s)
	}
	send(m, key("c"))
	if s := screen(m); strings.Contains(s, "a.B.d") {
		t.Errorf("c debería plegarlo todo:\n%s", s)
	}
	send(m, key("e"))
	if s := screen(m); !strings.Contains(s, "a.B.d") {
		t.Errorf("e debería desplegarlo todo:\n%s", s)
	}
}

func TestLevelCycle(t *testing.T) {
	m := newModel(Config{Filter: logs.Filter{Min: logs.LevelWarn}})
	send(m, tea.WindowSizeMsg{Width: 100, Height: 12}, entries(
		"2025-06-01 10:00:00.000 DEBUG [main][X:1] depurando",
		"2025-06-01 10:00:01.000 WARN  [main][X:1] cuidado",
		"2025-06-01 10:00:02.000 ERROR [main][X:1] roto",
	))
	if s := screen(m); strings.Contains(s, "depurando") || !strings.Contains(s, "cuidado") {
		t.Fatalf("--level warn no filtra:\n%s", s)
	}
	send(m, key("l")) // warn → error
	if s := screen(m); strings.Contains(s, "cuidado") || !strings.Contains(s, "≥ error") {
		t.Errorf("l debería pasar a error:\n%s", s)
	}
	send(m, key("l")) // error → todo
	if s := screen(m); !strings.Contains(s, "depurando") || !strings.Contains(s, "3 entradas") {
		t.Errorf("l debería volver a enseñarlo todo:\n%s", s)
	}
}

func TestScrollPausesAndResumes(t *testing.T) {
	m := newModel(Config{})
	var lines []string
	for i := 0; i < 50; i++ {
		lines = append(lines, fmt.Sprintf("2025-06-01 10:00:%02d.000 INFO  [main][X:1] línea %d", i%60, i))
	}
	send(m, tea.WindowSizeMsg{Width: 80, Height: 10}, entries(lines...))
	if s := screen(m); !strings.Contains(s, "línea 49") || strings.Contains(s, "línea 40") {
		t.Fatalf("debería verse el final:\n%s", s)
	}

	send(m, key("up"), key("g"))
	if s := screen(m); !strings.Contains(s, "línea 0") || !strings.Contains(s, "en pausa") {
		t.Fatalf("g debería ir al principio y pausar:\n%s", s)
	}
	send(m, entries("2025-06-01 10:01:00.000 INFO  [main][X:1] nueva"))
	if s := screen(m); strings.Contains(s, "] nueva") || !strings.Contains(s, "1 nueva") {
		t.Errorf("en pausa no debería moverse, solo avisar:\n%s", s)
	}
	send(m, key("G"))
	if s := screen(m); !strings.Contains(s, "] nueva") || !strings.Contains(s, "en vivo") {
		t.Errorf("G debería volver al final:\n%s", s)
	}
}

func TestTallEntryScrollsLineByLine(t *testing.T) {
	m := newModel(Config{})
	lines := []string{"2025-06-01 10:00:00.000 ERROR [main][X:1] Fallo", "java.lang.RuntimeException: x"}
	for i := 0; i < 30; i++ {
		lines = append(lines, fmt.Sprintf("\tat a.B.m%d(B.java:%d)", i, i))
	}
	lines = append(lines, "2025-06-01 10:00:01.000 INFO  [main][X:1] después")
	send(m, tea.WindowSizeMsg{Width: 80, Height: 10}, entries(lines...))
	send(m, key("up"), key("e")) // selecciona el error y lo despliega
	if s := screen(m); !strings.Contains(s, "] Fallo") || strings.Contains(s, "m29(") {
		t.Fatalf("debería verse el principio de la entrada:\n%s", s)
	}
	for i := 0; i < 40; i++ {
		send(m, key("down"))
	}
	if s := screen(m); !strings.Contains(s, "m29(") || !strings.Contains(s, "después") {
		t.Errorf("bajando debería recorrer la entrada y llegar a la siguiente:\n%s", s)
	}
}

func TestWrapCarriesColor(t *testing.T) {
	rows := wrap([]string{"\x1b[31m" + strings.Repeat("x", 25) + "\x1b[0m"}, 10)
	if len(rows) != 3 {
		t.Fatalf("filas = %q", rows)
	}
	if !strings.HasPrefix(rows[1], "\x1b[31m") || !strings.HasSuffix(rows[0], "\x1b[0m") {
		t.Errorf("el color no pasa a la siguiente fila: %q", rows)
	}
}

func TestBurstOfKeys(t *testing.T) {
	m := newModel(Config{})
	send(m, tea.WindowSizeMsg{Width: 100, Height: 12}, entries("2025-06-01 10:00:00.000 ERROR [main][X:1] roto"))
	send(m, key("lll")) // todo → debug → info → warn
	if s := screen(m); !strings.Contains(s, "≥ warn") {
		t.Errorf("las teclas seguidas deberían contar una a una:\n%s", s)
	}
}
