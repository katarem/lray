package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Mood cambia la cara y la luz de Faro.
type Mood int

const (
	Happy Mood = iota
	Thinking
	Worried
	Sad
	Sleepy
	Party
)

type look struct {
	face        string
	lamp        string
	beams       bool
	lampColor   lipgloss.TerminalColor
	bodyColor   lipgloss.TerminalColor
}

var looks = map[Mood]look{
	Happy:    {"ᵔᴗᵔ", "✦", true, Lamp, Sea},
	Thinking: {"•_•", "·", false, Muted, Sea},
	Worried:  {"°o°", "!", false, Lamp, Lamp},
	Sad:      {";_;", "○", false, Muted, Bad},
	Sleepy:   {"-ᴗ-", "○", false, Muted, Muted},
	Party:    {"^ᴗ^", "✺", true, Lamp, Sea},
}

// Faro dibuja la mascota (7 columnas × 4 filas).
//
//	   ▲
//	╶─[✦]─╴
//	 │ᵔᴗᵔ│
//	 ╘═══╛
func faro(m Mood) string {
	lk := looks[m]
	body := lipgloss.NewStyle().Foreground(lk.bodyColor)
	lamp := lipgloss.NewStyle().Foreground(lk.lampColor).Bold(true)
	face := lipgloss.NewStyle().Foreground(Ink).Bold(true)
	beam := lipgloss.NewStyle().Foreground(Lamp)

	left, right := "  ", "  "
	if lk.beams {
		left, right = beam.Render("╶─"), beam.Render("─╴")
	}
	lines := []string{
		"   " + body.Render("▲") + "   ",
		left + body.Render("[") + lamp.Render(lk.lamp) + body.Render("]") + right,
		" " + body.Render("│") + face.Render(lk.face) + body.Render("│") + " ",
		" " + body.Render("╘═══╛") + " ",
	}
	return strings.Join(lines, "\n")
}

// Say hace hablar a Faro: un título en negrita y líneas opcionales debajo.
func Say(m Mood, title string, body ...string) {
	content := Bold(title)
	if len(body) > 0 {
		content += "\n" + strings.Join(body, "\n")
	}
	bubble := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(Muted).
		Padding(0, 1)
	if lipgloss.Width(content) > 74 {
		bubble = bubble.Width(78)
	}
	tail := lipgloss.NewStyle().Foreground(Muted).Render("◂")
	out := lipgloss.JoinHorizontal(lipgloss.Center, faro(m), " ", tail, bubble.Render(content))
	fmt.Println()
	fmt.Println(lipgloss.NewStyle().MarginLeft(1).Render(out))
	fmt.Println()
}
