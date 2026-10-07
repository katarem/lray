// Package config guarda la lista de servers registrados en un JSON.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
)

// Server es un entorno Liferay registrado con un nombre.
type Server struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Version  string `json:"version,omitempty"`
	JavaHome string `json:"javaHome,omitempty"`
	Port     int    `json:"port,omitempty"` // puerto HTTP asignado al añadirlo (se aplica tras initBundle)
}

// Registry es la lista de servers persistida en disco.
type Registry struct {
	Servers []Server `json:"servers"`
	file    string
}

var nameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

// ValidName comprueba que el nombre sea cómodo de teclear en la terminal.
func ValidName(name string) error {
	if !nameRe.MatchString(name) {
		return fmt.Errorf("nombre no válido %q: usa letras, números, '.', '_' o '-'", name)
	}
	return nil
}

// Dir devuelve la carpeta de configuración (LRAY_HOME o ~/.config/lray).
func Dir() (string, error) {
	if d := os.Getenv("LRAY_HOME"); d != "" {
		return d, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "lray"), nil
}

// Load lee el registro; si no existe devuelve uno vacío.
func Load() (*Registry, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	r := &Registry{file: filepath.Join(dir, "servers.json")}
	data, err := os.ReadFile(r.file)
	if errors.Is(err, fs.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, r); err != nil {
		return nil, fmt.Errorf("no puedo leer %s: %w", r.file, err)
	}
	return r, nil
}

// Save escribe el registro de forma atómica.
func (r *Registry) Save() error {
	sort.Slice(r.Servers, func(i, j int) bool { return r.Servers[i].Name < r.Servers[j].Name })
	if err := os.MkdirAll(filepath.Dir(r.file), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	tmp := r.file + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, r.file)
}

// Get busca un server por nombre.
func (r *Registry) Get(name string) (*Server, bool) {
	for i := range r.Servers {
		if r.Servers[i].Name == name {
			return &r.Servers[i], true
		}
	}
	return nil, false
}

// Put añade o reemplaza un server.
func (r *Registry) Put(s Server) {
	if old, ok := r.Get(s.Name); ok {
		*old = s
		return
	}
	r.Servers = append(r.Servers, s)
}

// Remove quita un server; devuelve false si no existía.
func (r *Registry) Remove(name string) bool {
	for i := range r.Servers {
		if r.Servers[i].Name == name {
			r.Servers = append(r.Servers[:i], r.Servers[i+1:]...)
			return true
		}
	}
	return false
}
