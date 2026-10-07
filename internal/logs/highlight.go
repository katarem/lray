package logs

import (
	"fmt"
	"regexp"
	"strings"
)

// Level es la severidad de una línea de log.
type Level int

const (
	LevelAll Level = iota
	LevelTrace
	LevelDebug
	LevelInfo
	LevelWarn
	LevelError
)

// ParseLevel convierte "warn", "error"... en un Level. "" significa todo.
func ParseLevel(s string) (Level, error) {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "", "ALL":
		return LevelAll, nil
	case "TRACE", "FINEST", "FINER":
		return LevelTrace, nil
	case "DEBUG", "FINE":
		return LevelDebug, nil
	case "INFO", "CONFIG":
		return LevelInfo, nil
	case "WARN", "WARNING":
		return LevelWarn, nil
	case "ERROR", "SEVERE", "FATAL":
		return LevelError, nil
	}
	return LevelAll, fmt.Errorf("nivel desconocido %q (usa trace, debug, info, warn o error)", s)
}

// Formatos soportados (fecha opcional para cubrir Liferay 7.0):
//
//	Liferay: 2025-06-01 10:00:00.123 INFO  [main][StartupHelperUtil:72] mensaje
//	Tomcat:  01-Jun-2025 10:00:00.123 INFO [main] org.apache.catalina... mensaje
var (
	lineRe  = regexp.MustCompile(`^((?:\d{2,4}-(?:\d{2}|[A-Za-z]{3})-\d{2,4}[ T])?\d{2}:\d{2}:\d{2}(?:[.,]\d{1,3})?)\s+([A-Z]+)\s+(.*)$`)
	whereRe = regexp.MustCompile(`^((?:\[[^\]]*\])+)\s*(.*)$`)
)

// Message quita fecha, nivel y [hilo][clase] y deja solo el texto.
func Message(line string) string {
	m := lineRe.FindStringSubmatch(line)
	if m == nil {
		return strings.TrimSpace(line)
	}
	if w := whereRe.FindStringSubmatch(m[3]); w != nil {
		return strings.TrimSpace(w[2])
	}
	return strings.TrimSpace(m[3])
}

// Códigos ANSI a mano: es el camino caliente y así no hay asignaciones extra.
const (
	reset   = "\x1b[0m"
	dim     = "\x1b[2m"
	bold    = "\x1b[1m"
	red     = "\x1b[31m"
	green   = "\x1b[32m"
	yellow  = "\x1b[33m"
	cyan    = "\x1b[36m"
	gray    = "\x1b[90m"
	dimRed  = "\x1b[2;31m"
	boldRed = "\x1b[1;31m"
	boldGrn = "\x1b[1;32m"
)

var badges = map[Level]string{
	LevelError: "\x1b[1;97;41m ERROR \x1b[0m",
	LevelWarn:  "\x1b[1;30;43m WARN  \x1b[0m",
	LevelInfo:  "\x1b[1;97;44m INFO  \x1b[0m",
	LevelDebug: "\x1b[1;97;45m DEBUG \x1b[0m",
	LevelTrace: "\x1b[1;97;100m TRACE \x1b[0m",
}

var msgColor = map[Level]string{
	LevelError: red,
	LevelWarn:  yellow,
	LevelInfo:  "",
	LevelDebug: gray,
	LevelTrace: gray,
}

// Highlighter colorea líneas y filtra por nivel mínimo. Recuerda el nivel de
// la última línea para tratar igual sus continuaciones (stack traces).
type Highlighter struct {
	color   bool
	min     Level
	last    Level
	visible bool
	sb      strings.Builder
}

// NewHighlighter crea un resaltador; con color=false solo filtra.
func NewHighlighter(color bool, min Level) *Highlighter {
	return &Highlighter{color: color, min: min, last: LevelInfo, visible: true}
}

// Format devuelve la línea lista para imprimir y si debe mostrarse.
func (h *Highlighter) Format(line string) (string, bool) {
	m := lineRe.FindStringSubmatch(line)
	if m == nil {
		return h.continuation(line), h.visible
	}
	lvl, err := ParseLevel(m[2])
	if err != nil {
		return h.continuation(line), h.visible
	}
	h.last = lvl
	h.visible = lvl >= h.min
	if !h.visible || !h.color {
		return line, h.visible
	}

	where, msg := "", m[3]
	if w := whereRe.FindStringSubmatch(m[3]); w != nil {
		where, msg = w[1], w[2]
	}

	sb := &h.sb
	sb.Reset()
	sb.WriteString(gray)
	sb.WriteString(m[1])
	sb.WriteString(reset)
	sb.WriteByte(' ')
	sb.WriteString(badges[lvl])
	sb.WriteByte(' ')
	if where != "" {
		sb.WriteString(cyan)
		sb.WriteString(dim)
		sb.WriteString(where)
		sb.WriteString(reset)
		sb.WriteByte(' ')
	}
	switch {
	case strings.Contains(msg, "Server startup in"):
		sb.WriteString(boldGrn)
	case strings.HasPrefix(msg, "STARTED "):
		sb.WriteString(green)
	default:
		sb.WriteString(msgColor[lvl])
	}
	sb.WriteString(msg)
	sb.WriteString(reset)
	return sb.String(), true
}

func (h *Highlighter) continuation(line string) string {
	if !h.visible || !h.color {
		return line
	}
	t := strings.TrimSpace(line)
	switch {
	case strings.HasPrefix(t, "Caused by:"):
		return boldRed + line + reset
	case strings.HasPrefix(t, "at ") || strings.HasPrefix(t, "... "):
		if h.last >= LevelWarn {
			return dimRed + line + reset
		}
		return gray + line + reset
	case h.last == LevelError:
		return red + line + reset
	case h.last == LevelWarn:
		return yellow + line + reset
	}
	return line
}
