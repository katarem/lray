package ui

import (
	"errors"

	"github.com/charmbracelet/huh"
)

// ErrNeedsTTY: se necesita confirmación pero no hay terminal interactiva.
var ErrNeedsTTY = errors.New("necesito confirmación pero no hay terminal interactiva; usa --yes")

// Confirm pregunta sí/no.
func Confirm(title, description string, def bool) (bool, error) {
	if !IsTTY() {
		return false, ErrNeedsTTY
	}
	v := def
	err := huh.NewForm(huh.NewGroup(
		huh.NewConfirm().
			Title(title).
			Description(description).
			Affirmative("Sí").
			Negative("No").
			Value(&v),
	)).WithTheme(Theme()).Run()
	return v, err
}

// Input pide un texto libre.
func Input(title, description, placeholder string) (string, error) {
	if !IsTTY() {
		return "", nil
	}
	var v string
	err := huh.NewForm(huh.NewGroup(
		huh.NewInput().Title(title).Description(description).Placeholder(placeholder).Value(&v),
	)).WithTheme(Theme()).Run()
	return v, err
}
