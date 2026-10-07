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

func (l Level) String() string {
	switch l {
	case LevelTrace:
		return "trace"
	case LevelDebug:
		return "debug"
	case LevelInfo:
		return "info"
	case LevelWarn:
		return "warn"
	case LevelError:
		return "error"
	}
	return "todo"
}

// Filter decide qué niveles se ven: desde un mínimo o solo los de una lista.
type Filter struct {
	Min  Level
	Only []Level // si no está vacía manda sobre Min
}

// ParseFilter combina --level (mínimo) y --only (lista separada por comas).
func ParseFilter(min, only string) (Filter, error) {
	lvl, err := ParseLevel(min)
	if err != nil {
		return Filter{}, err
	}
	f := Filter{Min: lvl}
	for _, part := range strings.Split(only, ",") {
		if strings.TrimSpace(part) == "" {
			continue
		}
		l, err := ParseLevel(part)
		if err != nil {
			return Filter{}, err
		}
		if l != LevelAll && !f.has(l) {
			f.Only = append(f.Only, l)
		}
	}
	return f, nil
}

func (f Filter) has(l Level) bool {
	for _, o := range f.Only {
		if o == l {
			return true
		}
	}
	return false
}

// Allows indica si una entrada de ese nivel debe mostrarse.
func (f Filter) Allows(l Level) bool {
	if len(f.Only) > 0 {
		return f.has(l)
	}
	return l >= f.Min
}

// String describe el filtro para la barra de estado: "≥ warn", "solo error, debug".
func (f Filter) String() string {
	if len(f.Only) > 0 {
		names := make([]string, len(f.Only))
		for i, l := range f.Only {
			names[i] = l.String()
		}
		return "solo " + strings.Join(names, ", ")
	}
	if f.Min <= LevelTrace {
		return "todo"
	}
	return "≥ " + f.Min.String()
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
	magenta = "\x1b[35m"
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
