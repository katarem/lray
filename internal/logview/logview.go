// Package logview es el visor de logs a pantalla completa: sigue el fichero en
// vivo y deja plegar y desplegar stack traces y JSON, y cambiar de nivel sin
// salir.
package logview

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	"github.com/aymanbagabas/go-osc52/v2"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/katarem/lray/internal/logs"
	"github.com/katarem/lray/internal/ui"
)

// maxItems limita la memoria: al pasarse se descartan las entradas más viejas.
const maxItems = 20000

// Config describe qué se enseña y cómo.
type Config struct {
	Title  string // "logs de «tienda»"
	Detail string // ruta del fichero
	Filter logs.Filter
	Render logs.Options
}

// Run abre el visor y sigue src hasta que el usuario salga. Si src falla
// (por ejemplo, se corta la conexión con un server remoto), el visor se
// cierra y Run devuelve ese error.
func Run(parent context.Context, cfg Config, src logs.Source) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	p := tea.NewProgram(newModel(cfg), tea.WithAltScreen(), tea.WithMouseCellMotion(), tea.WithContext(ctx))
	done := make(chan struct{})
	var srcErr error
	go func() {
		defer close(done)
		g := logs.NewGrouper()
		var batch []*logs.Entry
		add := func(e *logs.Entry) {
			if e != nil {
				batch = append(batch, e)
			}
		}
		err := src(ctx, func(line string) { add(g.Add(line)) }, func() {
			add(g.Flush())
			if len(batch) > 0 {
				p.Send(entriesMsg(batch))
				batch = nil
			}
		})
		if err != nil && ctx.Err() == nil {
			srcErr = err
			p.Quit()
		}
	}()
	_, err := p.Run()
	cancel()
	<-done
	if srcErr != nil {
		return srcErr
	}
	if errors.Is(err, tea.ErrProgramKilled) && parent.Err() != nil {
		return nil // Ctrl+C fuera del visor (señal): salir sin más
	}
	return err
}

type (
	entriesMsg []*logs.Entry
	copiedMsg  struct {
		lines int
		err   error
	}
	noticeDoneMsg int
)

type item struct {
	e       *logs.Entry
	block   logs.Block
	ready   bool
	open    bool
	rowsKey int
	rows    []string
	// para buscar: cabecera y cuerpo sin colores en minúsculas
	text       string
	headLen    int
	searchable bool
}

type model struct {
	cfg     Config
	r       *logs.Renderer
	filter  logs.Filter
	items   []*item
	visible []*item
	cursor  int // índice en visible
	top     int // primera entrada pintada (índice en visible)
	skip    int // filas de visible[top] que quedan por encima de la pantalla
	follow  bool
	expand  bool // estado de las entradas nuevas
	unseen  int  // entradas nuevas mientras no se sigue el final
	notice  string
	noticeN int // para que solo borre el aviso el temporizador del último
	plain   *logs.Renderer
	copy    func(string) error
	width   int
	height  int

	searching bool        // escribiendo en la barra de búsqueda
	input     []rune      // lo escrito
	query     string      // búsqueda activa, en minúsculas
	shown     string      // la misma, tal cual se escribió
	saved     searchState // para volver atrás con Esc
	autoOpen  *item       // entrada desplegada por la búsqueda
	hitItem   *item       // última coincidencia a la que se ha saltado
	hits, hit int         // coincidencias visibles y posición de hitItem
}

func newModel(cfg Config) *model {
	return &model{
		cfg:    cfg,
		r:      logs.NewRenderer(cfg.Render),
		plain:  logs.NewRenderer(logs.Options{JSON: cfg.Render.JSON}),
		copy:   copyToClipboard,
		filter: cfg.Filter,
		follow: true,
	}
}

func (m *model) Init() tea.Cmd { return nil }

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.settle()
	case entriesMsg:
		m.append(msg)
	case tea.MouseMsg:
		m.mouse(msg)
	case copiedMsg:
		if msg.err != nil {
			return m, m.say("✘ No he podido copiar: " + msg.err.Error())
		}
		return m, m.say("✔ Copiada al portapapeles (" + plural(msg.lines, "línea", "líneas") + ")")
	case noticeDoneMsg:
		if int(msg) == m.noticeN {
			m.notice = ""
		}
	case tea.KeyMsg:
		if m.searching {
			return m, m.searchKey(msg)
		}
		if msg.Type == tea.KeyRunes && len(msg.Runes) > 1 && !msg.Paste {
			// Teclas pulsadas muy seguidas llegan juntas: una a una.
			var cmd tea.Cmd
			for _, r := range msg.Runes {
				if _, c := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}); c != nil {
					cmd = c
				}
			}
			return m, cmd
		}
		switch msg.String() {
		case "esc":
			if m.query != "" {
				m.clearSearch()
				return m, nil
			}
			return m, tea.Quit
		case "q", "ctrl+c":
			return m, tea.Quit
		case "down", "j":
			m.down()
		case "up", "k":
			m.up()
		case "pgdown", "ctrl+f":
			m.scroll(m.viewHeight() - 1)
		case "pgup", "ctrl+b":
			m.scroll(-(m.viewHeight() - 1))
		case "ctrl+d":
			m.scroll(m.viewHeight() / 2)
		case "ctrl+u":
			m.scroll(-m.viewHeight() / 2)
		case "home", "g":
			m.follow, m.cursor, m.top, m.skip = false, 0, 0, 0
		case "end", "G":
			m.follow = true
			m.settle()
		case "enter", " ", "tab", "o":
			m.toggle()
		case "e", "c":
			m.setAll(msg.String() == "e")
		case "l":
			m.cycleLevel()
		case "y":
			return m, m.copySelected()
		case "f", "/":
			m.startSearch()
		case "n":
			return m, m.next(-1)
		case "N":
			return m, m.next(1)
		}
	}
	return m, nil
}

// mouse: la rueda desplaza y un clic selecciona la entrada y la pliega o
// despliega.
func (m *model) mouse(msg tea.MouseMsg) {
	switch {
	case msg.Button == tea.MouseButtonWheelUp:
		m.scroll(-3)
	case msg.Button == tea.MouseButtonWheelDown:
		m.scroll(3)
	case msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress:
		if i, ok := m.entryAt(msg.Y); ok {
			m.follow = m.follow && i == len(m.visible)-1
			m.cursor = i
			m.toggle()
		}
	}
}

// entryAt devuelve la entrada pintada en la fila y de la pantalla.
func (m *model) entryAt(y int) (int, bool) {
	row := 0
	for i := m.top; i < len(m.visible) && row < m.viewHeight(); i++ {
		n := m.rowCount(i)
		if i == m.top {
			n -= m.skip
		}
		if y < row+n {
			return i, y >= 0
		}
		row += n
	}
	return 0, false
}

// copySelected copia la entrada seleccionada entera, sin colores y con el
// JSON ya formateado.
func (m *model) copySelected() tea.Cmd {
	it := m.at(m.cursor)
	if it == nil {
		return nil
	}
	b := m.plain.Render(it.e)
	text := strings.Join(append([]string{b.Head}, b.Body...), "\n")
	lines, copyFn := 1+len(b.Body), m.copy
	return func() tea.Msg { return copiedMsg{lines: lines, err: copyFn(text)} }
}

// copyToClipboard usa el portapapeles del sistema y, además, la secuencia
// OSC 52, que la terminal entiende aunque lray corra por SSH.
func copyToClipboard(text string) error {
	seq := osc52.New(text)
	switch {
	case os.Getenv("TMUX") != "":
		seq = seq.Tmux()
	case strings.HasPrefix(os.Getenv("TERM"), "screen"):
		seq = seq.Screen()
	}
	_, oscErr := seq.WriteTo(os.Stderr)
	if err := clipboard.WriteAll(text); err != nil && oscErr != nil {
		return err
	}
	return nil
}

// say enseña un aviso en la barra de estado durante unos segundos.
func (m *model) say(text string) tea.Cmd {
	m.notice = text
	m.noticeN++
	n := m.noticeN
	return tea.Tick(3*time.Second, func(time.Time) tea.Msg { return noticeDoneMsg(n) })
}

// ---- datos ----

func (m *model) append(entries []*logs.Entry) {
	for _, e := range entries {
		it := &item{e: e, open: m.expand}
		m.items = append(m.items, it)
		if m.filter.Allows(e.Level) {
			m.visible = append(m.visible, it)
			if !m.follow {
				m.unseen++
			}
			if m.matches(it, m.query) {
				m.hits++
			}
		}
	}
	if len(m.items) > maxItems {
		m.items = append([]*item(nil), m.items[len(m.items)-maxItems+maxItems/10:]...)
		m.refilter()
	}
	m.settle()
}

// refilter recalcula las entradas visibles conservando la seleccionada (o la
// más cercana) y la primera de la pantalla.
func (m *model) refilter() {
	sel, top := m.at(m.cursor), m.at(m.top)
	m.visible = m.visible[:0]
	m.cursor, m.top = -1, -1
	for _, it := range m.items {
		if !m.filter.Allows(it.e.Level) {
			if it == sel {
				sel = nil // la siguiente visible hereda la selección
			}
			if it == top {
				top = nil
			}
			continue
		}
		if (it == sel || sel == nil) && m.cursor < 0 {
			m.cursor = len(m.visible)
		}
		if (it == top || top == nil) && m.top < 0 {
			m.top = len(m.visible)
		}
		m.visible = append(m.visible, it)
	}
	if m.cursor < 0 {
		m.cursor = len(m.visible) - 1
	}
	if m.top < 0 || m.top > m.cursor {
		m.top, m.skip = m.cursor, 0
	}
	if m.cursor < 0 {
		m.cursor, m.top, m.skip = 0, 0, 0
	}
	m.recount()
}

func (m *model) at(i int) *item {
	if i < 0 || i >= len(m.visible) {
		return nil
	}
	return m.visible[i]
}

// ---- filas ----

func (m *model) viewHeight() int {
	if h := m.height - 2; h > 1 {
		return h
	}
	return 1
}

func (m *model) contentWidth() int {
	if w := m.width - 2; w > 10 {
		return w
	}
	return 10
}

// rows devuelve las filas ya ajustadas al ancho de una entrada.
func (m *model) rows(it *item) []string {
	if !it.ready {
		it.block, it.ready = m.r.Render(it.e), true
	}
	w := m.contentWidth()
	key := w * 2
	if it.open {
		key++
	}
	if it.rows != nil && it.rowsKey == key {
		return it.rows
	}
	lines := []string{it.block.Head}
	if !it.block.Collapsible() || it.open {
		lines = append(lines, it.block.Body...)
	}
	it.rows = wrap(lines, w)
	if it.block.Collapsible() && !it.open { // el resumen ocupa siempre una fila
		it.rows = append(it.rows, ansi.Truncate(strings.ReplaceAll(it.block.Summary, "\t", "    "), w, "…"))
	}
	it.rowsKey = key
	return it.rows
}

func (m *model) rowCount(i int) int { return len(m.rows(m.visible[i])) }

// wrap parte las líneas al ancho dado y arrastra el color de una fila a la
// siguiente, porque el margen izquierdo lo resetea.
func wrap(lines []string, w int) []string {
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		l = strings.ReplaceAll(l, "\t", "    ")
		state := ""
		for _, row := range strings.Split(ansi.Hardwrap(l, w, true), "\n") {
			next := sgrState(state, row)
			if state != "" {
				row = state + row
			}
			if next != "" {
				row += "\x1b[0m"
			}
			out = append(out, row)
			state = next
		}
	}
	return out
}

// sgrState devuelve los estilos SGR que siguen activos al final de row.
func sgrState(state, row string) string {
	for {
		i := strings.Index(row, "\x1b[")
		if i < 0 {
			return state
		}
		j := i + 2
		for j < len(row) && (row[j] == ';' || (row[j] >= '0' && row[j] <= '9')) {
			j++
		}
		if j < len(row) && row[j] == 'm' {
			if seq := row[i : j+1]; seq == "\x1b[0m" || seq == "\x1b[m" {
				state = ""
			} else {
				state += seq
			}
		}
		row = row[j:]
	}
}

// ---- desplazamiento ----

// bottom devuelve la posición (top, skip) que deja el final de la entrada
// idx justo en la última fila de la pantalla.
func (m *model) bottom(idx int) (int, int) {
	left := m.viewHeight()
	for i := idx; i >= 0; i-- {
		r := m.rowCount(i)
		if r >= left {
			return i, r - left
		}
		left -= r
	}
	return 0, 0
}

// settle recoloca la vista tras un cambio: pegada al final si se sigue el
// log y, si no, sin dejar hueco vacío debajo.
func (m *model) settle() {
	if len(m.visible) == 0 || m.width == 0 {
		return
	}
	last := len(m.visible) - 1
	if m.follow {
		m.cursor = last
		m.top, m.skip = m.bottom(last)
		m.unseen = 0
		return
	}
	m.cursor = min(max(m.cursor, 0), last)
	m.top = min(max(m.top, 0), last)
	if m.skip >= m.rowCount(m.top) {
		m.skip = 0
	}
	bt, bs := m.bottom(last)
	if m.top > bt || (m.top == bt && m.skip > bs) {
		m.top, m.skip = bt, bs
	}
}

// rowsUntilEnd cuenta las filas desde lo alto de la pantalla hasta el final
// de la entrada idx (que debe estar en o por debajo de top).
func (m *model) rowsUntilEnd(idx int) int {
	n := -m.skip
	for i := m.top; i <= idx; i++ {
		n += m.rowCount(i)
	}
	return n
}

// reveal desplaza lo justo para que se vea la entrada seleccionada.
func (m *model) reveal() {
	switch {
	case m.cursor < m.top:
		m.top, m.skip = m.cursor, 0
	case m.rowsUntilEnd(m.cursor) > m.viewHeight():
		if m.rowCount(m.cursor) > m.viewHeight() {
			m.top, m.skip = m.cursor, 0 // más alta que la pantalla: su principio
		} else {
			m.top, m.skip = m.bottom(m.cursor)
		}
	}
}

func (m *model) down() {
	if len(m.visible) == 0 {
		return
	}
	last := len(m.visible) - 1
	switch {
	case m.rowsUntilEnd(m.cursor) > m.viewHeight():
		m.scrollRows(1) // la seleccionada sigue por debajo: baja línea a línea
	case m.cursor < last:
		m.cursor++
		m.reveal()
	}
	if m.cursor == last && m.rowsUntilEnd(last) <= m.viewHeight() {
		m.follow = true
		m.settle()
	}
}

func (m *model) up() {
	if len(m.visible) == 0 {
		return
	}
	m.follow = false
	switch {
	case m.cursor == m.top && m.skip > 0:
		m.skip--
	case m.cursor > 0:
		m.cursor--
		if m.cursor < m.top {
			m.top, m.skip = m.cursor, max(0, m.rowCount(m.cursor)-m.viewHeight())
		}
	}
}

// scrollRows mueve la pantalla n filas sin tocar la selección.
func (m *model) scrollRows(n int) {
	bt, bs := m.bottom(len(m.visible) - 1)
	for ; n > 0; n-- {
		if m.top > bt || (m.top == bt && m.skip >= bs) {
			break
		}
		if m.skip+1 < m.rowCount(m.top) {
			m.skip++
		} else {
			m.top, m.skip = m.top+1, 0
		}
	}
	for ; n < 0; n++ {
		if m.skip > 0 {
			m.skip--
		} else if m.top > 0 {
			m.top--
			m.skip = m.rowCount(m.top) - 1
		} else {
			break
		}
	}
}

// scroll pasa página y deja la selección dentro de lo que se ve.
func (m *model) scroll(n int) {
	if len(m.visible) == 0 {
		return
	}
	top, skip := m.top, m.skip
	m.scrollRows(n)
	if n < 0 && (m.top != top || m.skip != skip) {
		m.follow = false
	}
	first, last := m.onScreen()
	m.cursor = min(max(m.cursor, first), last)
	if bt, bs := m.bottom(len(m.visible) - 1); n > 0 && m.top == bt && m.skip == bs {
		// Ha llegado al final: vuelve a seguir el log en vivo.
		m.follow = true
		m.settle()
	}
}

// onScreen devuelve la primera y la última entrada que asoman en pantalla.
func (m *model) onScreen() (int, int) {
	rows, last := -m.skip, m.top
	for i := m.top; i < len(m.visible) && rows < m.viewHeight(); i++ {
		last = i
		rows += m.rowCount(i)
	}
	return m.top, last
}

// ---- acciones ----

func (m *model) toggle() {
	it := m.at(m.cursor)
	if it == nil {
		return
	}
	if m.rows(it); !it.block.Collapsible() {
		return
	}
	it.open = !it.open
	if it == m.autoOpen {
		m.autoOpen = nil // ahora lo decide el usuario
	}
	if m.top == m.cursor {
		m.skip = 0
	}
	if !m.follow {
		m.reveal()
	}
	m.settle()
}

func (m *model) setAll(open bool) {
	m.expand = open
	m.autoOpen = nil
	for _, it := range m.items {
		it.open = open
	}
	m.skip = 0
	if !m.follow {
		m.top = min(m.top, m.cursor)
		m.reveal()
	}
	m.settle()
}

var levelCycle = []logs.Level{logs.LevelAll, logs.LevelDebug, logs.LevelInfo, logs.LevelWarn, logs.LevelError}

func (m *model) cycleLevel() {
	next := logs.LevelDebug
	if len(m.filter.Only) == 0 {
		for i, l := range levelCycle {
			if l == m.filter.Min {
				next = levelCycle[(i+1)%len(levelCycle)]
			}
		}
	}
	m.filter = logs.Filter{Min: next}
	m.refilter()
	if !m.follow {
		m.reveal()
	}
	m.settle()
}

// ---- vista ----

var (
	barSt    = lipgloss.NewStyle().Foreground(ui.Lamp).Bold(true)
	foldSt   = lipgloss.NewStyle().Foreground(ui.Muted)
	foldSel  = lipgloss.NewStyle().Foreground(ui.Lamp)
	liveSt   = lipgloss.NewStyle().Foreground(ui.Ok)
	pausedSt = lipgloss.NewStyle().Foreground(ui.Lamp)
)

func (m *model) View() string {
	if m.width == 0 {
		return ""
	}
	h := m.viewHeight()
	out := make([]string, 0, h+2)
	if len(m.visible) == 0 {
		msg := "Esperando líneas…"
		if len(m.items) > 0 {
			msg = "Nada que enseñar con el filtro " + m.filter.String() + " (pulsa l para cambiarlo)"
		}
		out = append(out, "  "+ui.MutedText(msg))
	}
	for i := m.top; i < len(m.visible) && len(out) < h; i++ {
		it := m.visible[i]
		rs := m.rows(it)
		start := 0
		if i == m.top {
			start = m.skip
		}
		for j := start; j < len(rs) && len(out) < h; j++ {
			out = append(out, m.gutter(it, i == m.cursor, j == 0)+highlight(rs[j], m.query))
		}
	}
	for len(out) < h {
		out = append(out, "")
	}
	out = append(out, m.status(), m.help())
	return strings.Join(out, "\n")
}

func (m *model) gutter(it *item, selected, first bool) string {
	bar := " "
	if selected {
		bar = barSt.Render("▌")
	}
	mark := " "
	if first && it.block.Collapsible() {
		mark = "▸"
		if it.open {
			mark = "▾"
		}
		if selected {
			mark = foldSel.Render(mark)
		} else {
			mark = foldSt.Render(mark)
		}
	}
	return bar + mark
}

func (m *model) status() string {
	left := ui.Badge("lray") + " " + ui.Bold(m.cfg.Title) + "  " +
		ui.MutedText(m.filter.String()+" · "+plural(len(m.visible), "entrada", "entradas"))
	if m.query != "" && !m.searching {
		left += "  " + pausedSt.Render("⌕ «"+m.shown+"» "+m.hitInfo())
	}
	right := liveSt.Render("● en vivo")
	switch {
	case m.notice != "":
		right = pausedSt.Render(m.notice)
	case !m.follow:
		right = pausedSt.Render("‖ en pausa")
		if m.unseen > 0 {
			right += pausedSt.Render(" · " + plural(m.unseen, "nueva", "nuevas") + " (G)")
		}
	}
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		left = ansi.Truncate(left, max(m.width-lipgloss.Width(right)-2, 0), "…")
		gap = max(m.width-lipgloss.Width(left)-lipgloss.Width(right), 1)
	}
	return left + strings.Repeat(" ", gap) + right
}

// hitInfo dice cuántas coincidencias hay y en cuál está la selección.
func (m *model) hitInfo() string {
	switch {
	case m.hits == 0:
		return "sin coincidencias"
	case m.hit > 0 && m.at(m.cursor) == m.hitItem:
		return strconv.Itoa(m.hit) + "/" + strconv.Itoa(m.hits)
	}
	return plural(m.hits, "coincidencia", "coincidencias")
}

func (m *model) help() string {
	if m.searching {
		info := "⏎ aceptar · Esc cancelar"
		if m.query != "" {
			info = m.hitInfo() + " · " + info
		}
		return ansi.Truncate(barSt.Render(" Buscar: ")+string(m.input)+barSt.Render("▏")+"  "+ui.MutedText(info), m.width, "…")
	}
	return ansi.Truncate(ui.MutedText(" ↑↓/rueda moverse · clic/⏎ plegar · e/c todo · y copiar · f buscar · n/N anterior/siguiente · l nivel · g/G inicio/final · q salir · Mayús+arrastrar selecciona texto  "+m.cfg.Detail), m.width, "…")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}
