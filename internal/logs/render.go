package logs

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Options configura cómo se pinta cada entrada.
type Options struct {
	Color bool
	JSON  JSONMode
}

// Block es una entrada lista para pintar.
type Block struct {
	Head    string   // primera línea
	Body    []string // lo que se puede plegar: JSON formateado, stack trace…
	Summary string   // sustituye a Body al plegar; vacío si no hay nada que plegar
}

// Collapsible indica si la entrada tiene cuerpo suficiente para plegarla.
func (b Block) Collapsible() bool { return b.Summary != "" }

// Renderer convierte entradas en bloques coloreados. Reutiliza su buffer, así
// que no es seguro usarlo desde varias goroutines.
type Renderer struct {
	opts Options
	sb   strings.Builder
}

// NewRenderer crea un renderer; con Color=false solo reorganiza el texto.
func NewRenderer(o Options) *Renderer { return &Renderer{opts: o} }

// Render pinta una entrada.
func (r *Renderer) Render(e *Entry) Block {
	msg, extra := e.Msg, e.Extra
	var body []string
	preview := ""

	if r.opts.JSON != JSONRaw {
		prefix, js, ok := findJSON(msg)
		if !ok && len(extra) > 0 && endsLikeJSON(extra[len(extra)-1]) {
			// JSON que empieza en la primera línea (o justo debajo) y sigue en las demás.
			joined := msg + "\n" + strings.Join(extra, "\n")
			if p, j, found := findJSON(joined); found && len(strings.TrimRight(p, " \t\r\n")) <= len(msg) {
				prefix, js, ok, extra = p, j, true, nil
			}
		}
		if ok {
			var lines []string
			msg, lines = r.splitJSON(prefix, js, "")
			for _, l := range lines {
				body = append(body, r.jsonLine(l))
			}
			if len(lines) > 0 {
				preview = compactJSON(js)
			}
		}
	}

	for _, l := range extra {
		if r.opts.JSON != JSONRaw && endsLikeJSON(l) {
			if prefix, js, ok := findJSON(l); ok {
				ws := l[:len(l)-len(strings.TrimLeft(l, " \t"))]
				head, lines := r.splitJSON(prefix, js, ws)
				if strings.TrimSpace(head) != "" {
					body = append(body, r.cont(head, e.Level))
				}
				for _, jl := range lines {
					body = append(body, r.jsonLine(jl))
				}
				continue
			}
		}
		body = append(body, r.cont(l, e.Level))
	}

	b := Block{Head: r.head(e, msg), Body: body}
	if len(body) >= 2 {
		b.Summary = r.summary(e, preview, len(body))
	}
	return b
}

// splitJSON aplica el modo de JSON: devuelve el texto que queda en la línea y,
// en modo pretty, las líneas indentadas que van debajo.
func (r *Renderer) splitJSON(prefix, js, indent string) (string, []string) {
	p := strings.TrimRight(prefix, " \t\r\n")
	sep := ""
	if p != prefix && p != "" {
		sep = " "
	}
	if r.opts.JSON == JSONPretty {
		if p == "" { // todo es JSON: la llave de apertura se queda en la línea
			if lines := prettyJSON(js, indent); lines != nil {
				return strings.TrimLeft(lines[0], " \t"), lines[1:]
			}
		} else if lines := prettyJSON(js, indent+"  "); lines != nil {
			return p, lines
		}
	}
	return p + sep + compactJSON(js), nil
}

func endsLikeJSON(s string) bool {
	s = strings.TrimRight(s, " \t\r")
	return s != "" && (s[len(s)-1] == '}' || s[len(s)-1] == ']')
}

func (r *Renderer) head(e *Entry, msg string) string {
	if !e.Parsed() {
		return r.cont(msg, e.Level)
	}
	if !r.opts.Color {
		if msg == e.Msg {
			return e.Raw
		}
		return e.Raw[:len(e.Raw)-len(e.Msg)] + msg
	}
	sb := &r.sb
	sb.Reset()
	sb.WriteString(gray)
	sb.WriteString(e.Time)
	sb.WriteString(reset)
	sb.WriteByte(' ')
	sb.WriteString(badges[e.Level])
	sb.WriteByte(' ')
	if e.Where != "" {
		sb.WriteString(cyan)
		sb.WriteString(dim)
		sb.WriteString(e.Where)
		sb.WriteString(reset)
		sb.WriteByte(' ')
	}
	switch {
	case strings.Contains(msg, "Server startup in"):
		sb.WriteString(boldGrn)
	case strings.HasPrefix(msg, "STARTED "):
		sb.WriteString(green)
	default:
		sb.WriteString(msgColor[e.Level])
	}
	sb.WriteString(msg)
	sb.WriteString(reset)
	return sb.String()
}

// cont colorea una línea de continuación según el nivel de su entrada.
func (r *Renderer) cont(line string, lvl Level) string {
	if !r.opts.Color {
		return line
	}
	t := strings.TrimSpace(line)
	switch {
	case strings.HasPrefix(t, "Caused by:"):
		return boldRed + line + reset
	case strings.HasPrefix(t, "at ") || strings.HasPrefix(t, "... "):
		if lvl >= LevelWarn {
			return dimRed + line + reset
		}
		return gray + line + reset
	case lvl == LevelError:
		return red + line + reset
	case lvl == LevelWarn:
		return yellow + line + reset
	}
	return line
}

func (r *Renderer) jsonLine(line string) string {
	if !r.opts.Color {
		return line
	}
	sb := &r.sb
	sb.Reset()
	colorJSON(sb, line)
	return sb.String()
}

// summary resume el cuerpo plegado: el JSON en una línea o la excepción con
// su causa raíz ("Caused by:" más profunda).
func (r *Renderer) summary(e *Entry, preview string, n int) string {
	text := clip(preview, 100)
	if text == "" && len(e.Extra) > 0 {
		text = clip(strings.TrimSpace(e.Extra[0]), 100)
		for i := len(e.Extra) - 1; i > 0; i-- {
			if t := strings.TrimSpace(e.Extra[i]); strings.HasPrefix(t, "Caused by:") {
				text += "  ⤷ " + clip(strings.TrimSpace(strings.TrimPrefix(t, "Caused by:")), 100)
				break
			}
		}
	}
	count := fmt.Sprintf(" · %d líneas", n)
	if !r.opts.Color {
		return "  ▸ " + text + count
	}
	color := msgColor[e.Level]
	if preview != "" {
		color = ""
	}
	return gray + "  ▸ " + reset + color + text + reset + gray + count + reset
}

// clip recorta s a n caracteres como mucho.
func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	i, count := 0, 0
	for i = range s {
		if count == n-1 {
			break
		}
		count++
	}
	return s[:i] + "…"
}
