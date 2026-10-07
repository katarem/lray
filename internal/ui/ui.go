// Package ui reúne la parte visual: la mascota, colores, spinners y preguntas.
package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
)

// Paleta: el azul del mar de noche y el ámbar de la luz de un faro
// (Liferay = "life ray": la mascota es un faro pequeñito).
var (
	Sea   = lipgloss.AdaptiveColor{Light: "#1F4FD1", Dark: "#6E9BFF"}
	Lamp  = lipgloss.AdaptiveColor{Light: "#B7791F", Dark: "#F6C454"}
	Ok    = lipgloss.AdaptiveColor{Light: "#1E8E5A", Dark: "#5FD39A"}
	Bad   = lipgloss.AdaptiveColor{Light: "#C53030", Dark: "#FF7A7A"}
	Muted = lipgloss.AdaptiveColor{Light: "#6B7280", Dark: "#8B93A7"}
	Ink   = lipgloss.AdaptiveColor{Light: "#111827", Dark: "#F3F4F6"}

	bold   = lipgloss.NewStyle().Bold(true)
	muted  = lipgloss.NewStyle().Foreground(Muted)
	code   = lipgloss.NewStyle().Foreground(Lamp)
	okSt   = lipgloss.NewStyle().Foreground(Ok)
	badSt  = lipgloss.NewStyle().Foreground(Bad)
	warnSt = lipgloss.NewStyle().Foreground(Lamp)
)

// Bold, Muted y Code devuelven texto con estilo.
func Bold(s string) string     { return bold.Render(s) }
func MutedText(s string) string { return muted.Render(s) }
func Code(s string) string     { return code.Render(s) }
func OkText(s string) string   { return okSt.Render(s) }
func BadText(s string) string  { return badSt.Render(s) }
func WarnText(s string) string { return warnSt.Render(s) }

// Success, Warn y Fail imprimen una línea de estado.
func Success(msg string) { fmt.Println(okSt.Render("✔ ") + msg) }
func Warn(msg string)    { fmt.Println(warnSt.Render("▲ ") + msg) }
func Fail(msg string)    { fmt.Println(badSt.Render("✘ ") + msg) }

// Badge es la etiqueta "lray" estilo cabecera.
func Badge(text string) string {
	return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#0B1220")).
		Background(Lamp).Padding(0, 1).Render(text)
}

// Header imprime la cabecera de una vista de larga duración (logs).
func Header(title, detail string) {
	fmt.Printf("\n%s %s %s\n\n", Badge("lray"), Bold(title), MutedText(detail))
}

// ShortPath sustituye la carpeta del usuario por ~.
func ShortPath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if rel, err := filepath.Rel(home, p); err == nil && !strings.HasPrefix(rel, "..") {
			if rel == "." {
				return "~"
			}
			return filepath.Join("~", rel)
		}
	}
	return p
}

// IsTTY indica si la salida estándar es una terminal interactiva.
func IsTTY() bool {
	st, err := os.Stdout.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

// Theme es el tema de los formularios interactivos.
func Theme() *huh.Theme {
	t := huh.ThemeBase()
	t.Focused.Base = t.Focused.Base.BorderForeground(Lamp)
	t.Focused.Title = t.Focused.Title.Foreground(Sea).Bold(true)
	t.Focused.NoteTitle = t.Focused.NoteTitle.Foreground(Sea).Bold(true)
	t.Focused.Description = t.Focused.Description.Foreground(Muted)
	t.Focused.SelectSelector = t.Focused.SelectSelector.Foreground(Lamp)
	t.Focused.SelectedOption = t.Focused.SelectedOption.Foreground(Lamp)
	t.Focused.SelectedPrefix = t.Focused.SelectedPrefix.Foreground(Lamp)
	t.Focused.FocusedButton = t.Focused.FocusedButton.Foreground(lipgloss.Color("#0B1220")).Background(Lamp).Bold(true)
	t.Focused.BlurredButton = t.Focused.BlurredButton.Foreground(Ink).Background(lipgloss.AdaptiveColor{Light: "#E5E7EB", Dark: "#2A3142"})
	t.Focused.TextInput.Cursor = t.Focused.TextInput.Cursor.Foreground(Lamp)
	t.Focused.TextInput.Prompt = t.Focused.TextInput.Prompt.Foreground(Lamp)
	t.Blurred = t.Focused
	t.Blurred.Base = t.Blurred.Base.BorderStyle(lipgloss.HiddenBorder())
	return t
}
