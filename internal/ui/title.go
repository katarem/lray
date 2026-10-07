package ui

import (
	"fmt"
	"os"
	"strings"
)

// SetTitle cambia el título de la ventana o pestaña de la terminal (para
// distinguir de un vistazo qué server tiene cada una) y devuelve con qué
// dejarlo como estaba. El título anterior se guarda en la pila de títulos de
// xterm (CSI 22/23 t), que entienden xterm, VTE (GNOME Terminal, Tilix...),
// Konsole, kitty o WezTerm; donde no, el prompt de la shell suele volver a
// poner el suyo. Sin terminal o con LRAY_NO_TITLE no hace nada.
func SetTitle(title string) (restore func()) {
	if !IsTTY() || os.Getenv("LRAY_NO_TITLE") != "" {
		return func() {}
	}
	fmt.Fprint(os.Stdout, "\x1b[22;0t\x1b]0;"+cleanTitle(title)+"\x07")
	return func() { fmt.Fprint(os.Stdout, "\x1b[23;0t") }
}

// cleanTitle quita los caracteres de control: un nombre raro no debe poder
// cerrar la secuencia y escribir en la terminal.
func cleanTitle(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return -1
		}
		return r
	}, s)
}
