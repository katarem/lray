package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// ErrInterrupted se devuelve cuando el usuario pulsa Ctrl+C durante una tarea.
var ErrInterrupted = errors.New("interrumpido")

type (
	detailMsg string
	doneMsg   struct{ err error }
)

type taskModel struct {
	spin        spinner.Model
	title       string
	detail      string
	start       time.Time
	width       int
	cancel      context.CancelFunc
	done        bool
	interrupted bool
	err         error
}

func (m taskModel) Init() tea.Cmd { return m.spin.Tick }

func (m taskModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			m.interrupted = true
			m.cancel()
			return m, tea.Quit
		}
	case tea.WindowSizeMsg:
		m.width = msg.Width
	case detailMsg:
		m.detail = string(msg)
	case doneMsg:
		m.done, m.err = true, msg.err
		return m, tea.Quit
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m taskModel) View() string {
	if m.done || m.interrupted {
		return ""
	}
	s := m.spin.View() + " " + Bold(m.title) + "  " + MutedText(elapsed(m.start))
	if m.detail != "" {
		w := m.width - 4
		if w < 20 {
			w = 76
		}
		s += "\n  " + MutedText(ansi.Truncate(strings.ReplaceAll(m.detail, "\t", " "), w, "…"))
	}
	return s
}

func elapsed(start time.Time) string {
	d := time.Since(start).Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}

// RunTask ejecuta fn mostrando un spinner con el título y la última línea de
// progreso que fn vaya reportando. Ctrl+C cancela el contexto de fn.
func RunTask(title, doneTitle string, fn func(ctx context.Context, update func(string)) error) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	start := time.Now()

	if !IsTTY() {
		fmt.Println("… " + title)
		err := fn(ctx, func(string) {})
		report(doneTitle, title, start, err)
		return err
	}

	m := taskModel{
		spin:   spinner.New(spinner.WithSpinner(spinner.MiniDot), spinner.WithStyle(lipgloss.NewStyle().Foreground(Lamp))),
		title:  title,
		start:  start,
		cancel: cancel,
	}
	p := tea.NewProgram(m)
	go func() {
		err := fn(ctx, func(s string) { p.Send(detailMsg(s)) })
		p.Send(doneMsg{err})
	}()
	final, err := p.Run()
	if err != nil {
		return err
	}
	fm := final.(taskModel)
	if fm.interrupted {
		return ErrInterrupted
	}
	report(doneTitle, title, start, fm.err)
	return fm.err
}

func report(doneTitle, title string, start time.Time, err error) {
	if err != nil {
		Fail(title)
		return
	}
	Success(doneTitle + "  " + MutedText(elapsed(start)))
}
