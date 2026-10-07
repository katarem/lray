package logs

import (
	"regexp"
	"strings"
)

// Entry es una línea de log junto con las que dependen de ella: el stack
// trace de una excepción o un JSON repartido en varias líneas.
type Entry struct {
	Raw   string   // primera línea tal cual
	Time  string   // vacío si la línea no tiene fecha y nivel
	Level Level    // si la línea no lo trae, el de la entrada anterior
	Where string   // [hilo][Clase:línea]
	Msg   string   // texto sin fecha, nivel ni [hilo][clase]
	Extra []string // líneas de continuación
}

// Parsed indica si la primera línea tiene el formato de log (fecha y nivel).
func (e *Entry) Parsed() bool { return e.Time != "" }

// Lines devuelve el número de líneas originales de la entrada.
func (e *Entry) Lines() int { return 1 + len(e.Extra) }

func parseHead(line string) (Entry, bool) {
	m := lineRe.FindStringSubmatch(line)
	if m == nil {
		return Entry{}, false
	}
	lvl, err := ParseLevel(m[2])
	if err != nil {
		return Entry{}, false
	}
	e := Entry{Raw: line, Time: m[1], Level: lvl, Msg: m[3]}
	if w := whereRe.FindStringSubmatch(m[3]); w != nil {
		e.Where, e.Msg = w[1], w[2]
	}
	return e, true
}

// exceptionRe reconoce la primera línea de una excepción Java
// ("java.lang.NullPointerException: ...", "com.foo.Bar$BazError").
var exceptionRe = regexp.MustCompile(`^(?:[a-zA-Z_$][\w$]*\.)+[A-Z][\w$]*(?:Exception|Error|Throwable)(?::|$)`)

// isContinuation decide si una línea sin fecha ni nivel pertenece a la
// entrada anterior. Lo que no encaja (un System.out.println, por ejemplo) va
// como entrada propia para no esconderlo al plegar.
func isContinuation(line string) bool {
	if line == "" {
		return false
	}
	switch line[0] {
	case ' ', '\t', '{', '}', ']':
		return true
	case '[':
		t := strings.TrimSpace(line)
		return t == "[" || (len(t) > 1 && strings.ContainsRune(`{["`, rune(t[1])))
	}
	return strings.HasPrefix(line, "Caused by:") ||
		strings.HasPrefix(line, "Suppressed:") ||
		strings.HasPrefix(line, "... ") ||
		exceptionRe.MatchString(line)
}

// Grouper junta las líneas en entradas. Una entrada se cierra cuando llega
// la siguiente o cuando se llama a Flush (fin de una ráfaga de lectura).
type Grouper struct {
	cur  *Entry
	last Level
}

// NewGrouper crea un agrupador vacío.
func NewGrouper() *Grouper { return &Grouper{last: LevelInfo} }

// Add procesa una línea y devuelve la entrada anterior si esta la cierra.
func (g *Grouper) Add(line string) *Entry {
	if e, ok := parseHead(line); ok {
		done := g.cur
		g.cur, g.last = &e, e.Level
		return done
	}
	if g.cur != nil && isContinuation(line) {
		g.cur.Extra = append(g.cur.Extra, line)
		return nil
	}
	done := g.cur
	g.cur = &Entry{Raw: line, Level: g.last, Msg: line}
	return done
}

// Flush devuelve la entrada en curso (o nil) y empieza de cero.
func (g *Grouper) Flush() *Entry {
	done := g.cur
	g.cur = nil
	return done
}
