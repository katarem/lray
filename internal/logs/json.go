package logs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// JSONMode dice qué hacer con el JSON que aparece en los logs.
type JSONMode int

const (
	JSONPretty  JSONMode = iota // indentado y coloreado, en líneas aparte
	JSONCompact                 // en una sola línea
	JSONRaw                     // tal cual viene
)

// ParseJSONMode convierte "pretty", "compact" o "raw" en un JSONMode.
func ParseJSONMode(s string) (JSONMode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "pretty":
		return JSONPretty, nil
	case "compact":
		return JSONCompact, nil
	case "raw", "off":
		return JSONRaw, nil
	}
	return JSONPretty, fmt.Errorf("formato de JSON desconocido %q (usa pretty, compact o raw)", s)
}

// findJSON busca un objeto (o una lista de objetos/listas) que ocupe el final
// de s y devuelve el texto anterior y el JSON. Descarta cosas como "[1234]"
// o "{}" para no tocar mensajes normales que llevan corchetes.
func findJSON(s string) (prefix, js string, ok bool) {
	end := strings.TrimRight(s, " \t\r\n")
	if len(end) < 2 {
		return "", "", false
	}
	closer := end[len(end)-1]
	if closer != '}' && closer != ']' {
		return "", "", false
	}
	opener := byte('{')
	if closer == ']' {
		opener = '['
	}
	for i, tries := 0, 0; i < len(end) && tries < 8; i++ {
		if end[i] != opener {
			continue
		}
		tries++
		cand := end[i:]
		if worthFormatting(cand) && json.Valid([]byte(cand)) {
			return end[:i], cand, true
		}
	}
	return "", "", false
}

func worthFormatting(js string) bool {
	rest := strings.TrimLeft(js[1:], " \t\r\n")
	if rest == "" {
		return false
	}
	if js[0] == '{' {
		return rest[0] == '"'
	}
	return rest[0] == '{' || rest[0] == '['
}

// prettyJSON indenta js. Devuelve nil si cabe en una línea sin perder nada
// (un objeto con una sola clave simple, por ejemplo).
func prettyJSON(js, indent string) []string {
	var buf bytes.Buffer
	if err := json.Indent(&buf, []byte(js), "", "  "); err != nil {
		return nil
	}
	lines := strings.Split(buf.String(), "\n")
	if len(lines) <= 3 {
		return nil
	}
	for i, l := range lines {
		lines[i] = indent + l
	}
	return lines
}

func compactJSON(js string) string {
	var buf bytes.Buffer
	if err := json.Compact(&buf, []byte(js)); err != nil {
		return js
	}
	return buf.String()
}

// colorJSON colorea una línea de JSON bien formado: claves, cadenas,
// números y literales con colores distintos y la puntuación atenuada.
func colorJSON(sb *strings.Builder, line string) {
	for i := 0; i < len(line); {
		c := line[i]
		switch {
		case c == '"':
			j := i + 1
			for j < len(line) && line[j] != '"' {
				if line[j] == '\\' {
					j++
				}
				j++
			}
			if j < len(line) {
				j++
			}
			k := j
			for k < len(line) && line[k] == ' ' {
				k++
			}
			if k < len(line) && line[k] == ':' {
				sb.WriteString(cyan)
			} else {
				sb.WriteString(green)
			}
			sb.WriteString(line[i:j])
			sb.WriteString(reset)
			i = j
		case c == '-' || (c >= '0' && c <= '9'):
			j := i
			for j < len(line) && strings.IndexByte("+-.eE0123456789", line[j]) >= 0 {
				j++
			}
			sb.WriteString(yellow)
			sb.WriteString(line[i:j])
			sb.WriteString(reset)
			i = j
		case c == 't' || c == 'f' || c == 'n':
			j := i
			for j < len(line) && line[j] >= 'a' && line[j] <= 'z' {
				j++
			}
			sb.WriteString(magenta)
			sb.WriteString(line[i:j])
			sb.WriteString(reset)
			i = j
		case strings.IndexByte("{}[],:", c) >= 0:
			sb.WriteString(gray)
			sb.WriteByte(c)
			sb.WriteString(reset)
			i++
		default:
			sb.WriteByte(c)
			i++
		}
	}
}
