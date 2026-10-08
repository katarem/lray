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

func TestMouse(t *testing.T) {
	m := newModel(Config{})
	send(m, tea.WindowSizeMsg{Width: 100, Height: 12},
		entries(append(stack, "2025-06-01 10:00:01.000 INFO  [main][X:1] después")...))
	// Fila 0: cabecera del error; fila 1: su resumen; fila 2: "después".
	send(m, tea.MouseMsg{X: 5, Y: 1, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if s := screen(m); !strings.Contains(s, "a.B.d") || !strings.Contains(s, "en pausa") || m.cursor != 0 {
		t.Fatalf("el clic debería seleccionar y desplegar el error:\n%s", s)
	}
	send(m, tea.MouseMsg{X: 5, Y: 0, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if s := screen(m); strings.Contains(s, "a.B.d") {
		t.Errorf("otro clic debería plegarlo:\n%s", s)
	}
	send(m, tea.MouseMsg{X: 5, Y: 9, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if m.cursor != 0 {
		t.Errorf("un clic en una fila vacía no debería cambiar la selección (cursor %d)", m.cursor)
	}
}

func TestWheel(t *testing.T) {
	m := newModel(Config{})
	var lines []string
	for i := 0; i < 50; i++ {
		lines = append(lines, fmt.Sprintf("2025-06-01 10:00:%02d.000 INFO  [main][X:1] línea %d", i%60, i))
	}
	send(m, tea.WindowSizeMsg{Width: 80, Height: 10}, entries(lines...))
	send(m, tea.MouseMsg{Button: tea.MouseButtonWheelUp, Action: tea.MouseActionPress})
	if s := screen(m); strings.Contains(s, "línea 49") || !strings.Contains(s, "en pausa") {
		t.Fatalf("la rueda hacia arriba debería subir y pausar:\n%s", s)
	}
	send(m, tea.MouseMsg{Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	if s := screen(m); !strings.Contains(s, "línea 49") || !strings.Contains(s, "en vivo") {
		t.Errorf("la rueda hacia abajo debería volver al final:\n%s", s)
	}
}

func TestCopy(t *testing.T) {
	m := newModel(Config{Render: logs.Options{Color: true}})
	var got string
	m.copy = func(s string) error { got = s; return nil }
	send(m, tea.WindowSizeMsg{Width: 100, Height: 12},
		entries(`2025-06-01 10:00:00.000 INFO  [main][X:1] Respuesta: {"a":1,"b":2}`))
	_, cmd := m.Update(key("y"))
	if cmd == nil {
		t.Fatal("y debería copiar")
	}
	send(m, cmd())
	want := "2025-06-01 10:00:00.000 INFO  [main][X:1] Respuesta:\n  {\n    \"a\": 1,\n    \"b\": 2\n  }"
	if got != want {
		t.Errorf("copiado %q; quiero %q", got, want)
	}
	if s := screen(m); !strings.Contains(s, "Copiada al portapapeles (5 líneas)") {
		t.Errorf("falta el aviso:\n%s", s)
	}
	send(m, noticeDoneMsg(m.noticeN))
	if s := screen(m); strings.Contains(s, "Copiada") {
		t.Errorf("el aviso debería desaparecer:\n%s", s)
	}
}

func typeKeys(m *model, s string) {
	for _, r := range s {
		send(m, key(string(r)))
	}
}

func TestSearch(t *testing.T) {
	m := newModel(Config{})
	var lines []string
	for i := 0; i < 50; i++ {
		msg := fmt.Sprintf("línea %d", i)
		if i == 10 || i == 30 {
			msg = fmt.Sprintf("Pedido %d rechazado", i)
		}
		lines = append(lines, fmt.Sprintf("2025-06-01 10:00:%02d.000 INFO  [main][X:1] %s", i%60, msg))
	}
	send(m, tea.WindowSizeMsg{Width: 100, Height: 10}, entries(lines...))

	send(m, key("f"))
	typeKeys(m, "RECHAZ")
	if s := screen(m); !strings.Contains(s, "Buscar: RECHAZ") || !strings.Contains(s, "Pedido 30") || m.cursor != 30 {
		t.Fatalf("debería saltar a la coincidencia más reciente mientras escribe (cursor %d):\n%s", m.cursor, s)
	}
	if !strings.Contains(m.View(), "\x1b[7mrechaz\x1b[27m") {
		t.Errorf("la coincidencia debería verse resaltada:\n%q", m.View())
	}
	send(m, key("enter"))
	if s := screen(m); !strings.Contains(s, "«RECHAZ» 2/2") || !strings.Contains(s, "en pausa") {
		t.Fatalf("Enter debería aceptar la búsqueda:\n%s", s)
	}

	send(m, key("n"))
	if s := screen(m); m.cursor != 10 || !strings.Contains(s, "«RECHAZ» 1/2") {
		t.Fatalf("n debería ir a la anterior (cursor %d):\n%s", m.cursor, s)
	}
	send(m, key("n"))
	if s := screen(m); m.cursor != 30 || !strings.Contains(s, "Sigo desde el final") {
		t.Errorf("n en la primera debería dar la vuelta (cursor %d):\n%s", m.cursor, s)
	}
	send(m, key("N"))
	if m.cursor != 10 {
		t.Errorf("N debería ir a la siguiente dando la vuelta (cursor %d)", m.cursor)
	}

	send(m, tea.KeyMsg{Type: tea.KeyEsc})
	if s := screen(m); strings.Contains(s, "«RECHAZ»") || m.query != "" {
		t.Errorf("Esc debería quitar la búsqueda:\n%s", s)
	}
}

func TestSearchCancelRestores(t *testing.T) {
	m := newModel(Config{})
	var lines []string
	for i := 0; i < 50; i++ {
		lines = append(lines, fmt.Sprintf("2025-06-01 10:00:%02d.000 INFO  [main][X:1] línea %d", i%60, i))
	}
	send(m, tea.WindowSizeMsg{Width: 100, Height: 10}, entries(lines...))
	send(m, key("f"))
	typeKeys(m, "línea 3")
	if m.cursor != 39 || m.follow {
		t.Fatalf("debería estar en «línea 39», la más reciente (cursor %d)", m.cursor)
	}
	send(m, tea.KeyMsg{Type: tea.KeyBackspace}, tea.KeyMsg{Type: tea.KeyBackspace})
	if m.cursor != 49 {
		t.Errorf("«línea » debería volver a la más reciente (cursor %d)", m.cursor)
	}
	send(m, tea.KeyMsg{Type: tea.KeyEsc})
	if s := screen(m); m.searching || !m.follow || m.cursor != 49 || !strings.Contains(s, "en vivo") || m.query != "" {
		t.Errorf("Esc mientras se escribe debería volver a donde estaba:\n%s", s)
	}
	send(m, key("f"))
	typeKeys(m, "nada así")
	send(m, key("enter"))
	if s := screen(m); !strings.Contains(s, "Nada coincide con «nada así»") || m.cursor != 49 {
		t.Errorf("sin coincidencias debería avisar y no moverse:\n%s", s)
	}
}

func TestSearchOpensCollapsed(t *testing.T) {
	m := newModel(Config{})
	send(m, tea.WindowSizeMsg{Width: 100, Height: 20},
		entries(append(stack, "2025-06-01 10:00:01.000 INFO  [main][X:1] después")...))
	send(m, key("/"))
	typeKeys(m, "b.java:2")
	send(m, key("enter"))
	if s := screen(m); m.cursor != 0 || !strings.Contains(s, "a.B.d(B.java:2)") {
		t.Fatalf("debería desplegar la entrada con la coincidencia en el cuerpo:\n%s", s)
	}
	send(m, key("f"))
	typeKeys(m, "después")
	if s := screen(m); m.cursor != 1 || !strings.Contains(s, "a.B.d") {
		t.Errorf("lo desplegado por una búsqueda aceptada se queda abierto:\n%s", s)
	}
	send(m, key("enter"), key("n"))
	if m.cursor != 1 {
		t.Errorf("la única coincidencia es «después» (cursor %d)", m.cursor)
	}
}

func TestHighlight(t *testing.T) {
	row := "\x1b[31mERROR\x1b[0m pedido \x1b[1mPEDIDO\x1b[0m"
	got := highlight(row, "pedido")
	if ansi.Strip(got) != ansi.Strip(row) {
		t.Fatalf("el texto no debería cambiar: %q", got)
	}
	if !strings.Contains(got, "\x1b[7mpedido\x1b[27m") || !strings.Contains(got, "\x1b[1m\x1b[7mPEDIDO") {
		t.Errorf("resaltado: %q", got)
	}
	if highlight(row, "nada") != row {
		t.Error("sin coincidencias no debería tocar la fila")
	}
}
