package logview

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
)

// La búsqueda (f o /) mira el texto de las entradas sin colores y con el JSON
// ya formateado, sin distinguir mayúsculas. Mientras se escribe salta a la
// coincidencia más cercana por encima de la selección (lo más reciente
// primero, que en un log es lo que interesa); Enter la acepta y n/m recorren
// las demás hacia arriba/abajo. Si la coincidencia está en el cuerpo de una
// entrada plegada, la despliega, y la vuelve a plegar al pasar a otra.

// searchState guarda lo necesario para deshacer la búsqueda con Esc.
type searchState struct {
	cursor, top, skip int
	follow            bool
	query, shown      string
}

// lower pasa a minúsculas runa a runa, para que el texto conserve el mismo
// número de runas y se puedan casar posiciones con el original.
func lower(s string) string { return strings.Map(unicode.ToLower, s) }

// searchText devuelve el texto en el que se busca: cabecera y cuerpo sin
// colores, en minúsculas.
func (m *model) searchText(it *item) string {
	if !it.searchable && it.sep {
		it.searchable = true // sin texto: nunca coincide
	}
	if !it.searchable {
		b := m.plain.Render(it.e)
		head := lower(b.Head)
		it.text = head
		if len(b.Body) > 0 {
			it.text += "\n" + lower(strings.Join(b.Body, "\n"))
		}
		it.headLen = len(head)
		it.searchable = true
	}
	return it.text
}

func (m *model) matches(it *item, q string) bool {
	return q != "" && strings.Contains(m.searchText(it), q)
}

// find busca desde la entrada from (incluida) en la dirección dir (-1 hacia
// arriba, +1 hacia abajo); si llega al extremo, sigue por el otro. Devuelve
// -1 si no hay ninguna.
func (m *model) find(q string, from, dir int) (idx int, wrapped bool) {
	n := len(m.visible)
	if q == "" || n == 0 {
		return -1, false
	}
	from = min(max(from, -1), n)
	for i := from; i >= 0 && i < n; i += dir {
		if m.matches(m.visible[i], q) {
			return i, false
		}
	}
	start := n - 1
	if dir > 0 {
		start = 0
	}
	for i := start; i != from && i >= 0 && i < n; i += dir {
		if m.matches(m.visible[i], q) {
			return i, true
		}
	}
	return -1, false
}

// jump selecciona la coincidencia i y la enseña.
func (m *model) jump(i int) {
	it := m.visible[i]
	if m.autoOpen != nil && m.autoOpen != it {
		m.autoOpen.open = false
		m.autoOpen = nil
	}
	if m.rows(it); !it.open && it.block.Collapsible() && !strings.Contains(m.searchText(it)[:it.headLen], m.query) {
		it.open = true
		m.autoOpen = it
	}
	m.follow = false
	m.cursor = i
	m.anchor = -1
	if m.top >= len(m.visible) || m.skip >= m.rowCount(m.top) {
		m.skip = 0
	}
	if i <= m.top {
		m.top, m.skip = i, 0
	}
	m.reveal()
	m.settle()
	m.hitItem = it
	m.recount()
}

// recount cuenta las coincidencias visibles y la posición de la última a la
// que se ha saltado.
func (m *model) recount() {
	m.hits, m.hit = 0, 0
	if m.query == "" {
		return
	}
	for _, it := range m.visible {
		if m.matches(it, m.query) {
			m.hits++
			if it == m.hitItem {
				m.hit = m.hits
			}
		}
	}
}

func (m *model) startSearch() {
	m.searching = true
	m.input = nil
	m.autoOpen = nil // lo que abrió una búsqueda anterior se queda abierto
	m.saved = searchState{cursor: m.cursor, top: m.top, skip: m.skip, follow: m.follow, query: m.query, shown: m.shown}
}

// restore vuelve a donde estaba la vista antes de empezar a escribir.
func (m *model) restore() {
	if m.autoOpen != nil {
		m.autoOpen.open = false
		m.autoOpen = nil
	}
	s := m.saved
	m.cursor, m.top, m.skip, m.follow = s.cursor, s.top, s.skip, s.follow
	if m.top < len(m.visible) && m.skip >= m.rowCount(m.top) {
		m.skip = 0
	}
	m.settle()
}

// incremental salta a la primera coincidencia de lo escrito hasta ahora,
// contando desde donde estaba la vista al empezar.
func (m *model) incremental() {
	m.restore()
	m.shown = string(m.input)
	m.query = lower(m.shown)
	m.hitItem = nil
	if i, _ := m.find(m.query, m.saved.cursor, -1); i >= 0 {
		m.jump(i)
		return
	}
	m.recount()
}

func (m *model) searchKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.Type {
	case tea.KeyCtrlC:
		return tea.Quit
	case tea.KeyEsc:
		m.searching = false
		m.restore()
		m.query, m.shown = m.saved.query, m.saved.shown
		m.recount()
	case tea.KeyEnter:
		m.searching = false
		switch {
		case m.query == "":
			m.clearSearch()
		case m.hits == 0:
			return m.say("✘ Nada coincide con «" + m.shown + "»")
		}
	case tea.KeyBackspace:
		if len(m.input) > 0 {
			m.input = m.input[:len(m.input)-1]
			m.incremental()
		}
	case tea.KeyCtrlU:
		m.input = nil
		m.incremental()
	case tea.KeyCtrlW:
		s := strings.TrimRightFunc(string(m.input), unicode.IsSpace)
		if i := strings.LastIndexFunc(s, unicode.IsSpace); i >= 0 {
			_, w := utf8.DecodeRuneInString(s[i:])
			s = s[:i+w]
		} else {
			s = ""
		}
		m.input = []rune(s)
		m.incremental()
	case tea.KeySpace, tea.KeyRunes:
		n := len(m.input)
		for _, r := range msg.Runes {
			if !unicode.IsControl(r) {
				m.input = append(m.input, r)
			}
		}
		if len(m.input) != n {
			m.incremental()
		}
	}
	return nil
}

// next salta a la siguiente coincidencia en la dirección dir (-1 más antigua,
// +1 más nueva).
func (m *model) next(dir int) tea.Cmd {
	if m.query == "" {
		return m.say("Pulsa f para buscar")
	}
	i, wrapped := m.find(m.query, m.cursor+dir, dir)
	if i < 0 {
		return m.say("✘ Nada coincide con «" + m.shown + "»")
	}
	m.jump(i)
	if wrapped && dir < 0 {
		return m.say("↺ Sigo desde el final")
	}
	if wrapped {
		return m.say("↺ Sigo desde el principio")
	}
	return nil
}

func (m *model) clearSearch() {
	m.query, m.shown, m.hitItem, m.autoOpen = "", "", nil, nil
	m.hits, m.hit = 0, 0
}

// highlight invierte los colores de las apariciones de q (en minúsculas) en
// una fila con secuencias ANSI, sin tocar sus estilos.
func highlight(row, q string) string {
	if q == "" {
		return row
	}
	var vis []rune
	eachRune(row, func(_ string, r rune, esc bool) {
		if !esc {
			vis = append(vis, unicode.ToLower(r))
		}
	})
	qr := []rune(q)
	mark := make([]bool, len(vis))
	found := false
	for i := 0; i+len(qr) <= len(vis); {
		if slices.Equal(vis[i:i+len(qr)], qr) {
			for j := range qr {
				mark[i+j] = true
			}
			i += len(qr)
			found = true
		} else {
			i++
		}
	}
	if !found {
		return row
	}
	var b strings.Builder
	in, k := false, 0
	eachRune(row, func(s string, _ rune, esc bool) {
		if esc {
			b.WriteString(s)
			if in { // un reset en medio no debe cortar el resaltado
				b.WriteString("\x1b[7m")
			}
			return
		}
		if on := mark[k]; on != in {
			if on {
				b.WriteString("\x1b[7m")
			} else {
				b.WriteString("\x1b[27m")
			}
			in = on
		}
		b.WriteString(s)
		k++
	})
	if in {
		b.WriteString("\x1b[27m")
	}
	return b.String()
}

// eachRune recorre s dando cada secuencia de escape entera (esc = true) o
// cada runa visible.
func eachRune(s string, fn func(s string, r rune, esc bool)) {
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			j := escEnd(s, i)
			fn(s[i:j], 0, true)
			i = j
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		fn(s[i:i+size], r, false)
		i += size
	}
}

// escEnd devuelve dónde acaba la secuencia de escape que empieza en i.
func escEnd(s string, i int) int {
	j := i + 1
	if j >= len(s) {
		return j
	}
	switch s[j] {
	case '[': // CSI: parámetros y un byte final entre @ y ~
		for j++; j < len(s); j++ {
			if s[j] >= 0x40 && s[j] <= 0x7e {
				return j + 1
			}
		}
		return j
	case ']': // OSC: hasta BEL o ESC \
		for j++; j < len(s); j++ {
			if s[j] == 0x07 {
				return j + 1
			}
			if s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\' {
				return j + 2
			}
		}
		return j
	}
	return j + 1
}
