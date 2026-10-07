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
	"strings"
)

// Server es un entorno Liferay registrado con un nombre.
type Server struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Version  string `json:"version,omitempty"`
	JavaHome string `json:"javaHome,omitempty"`
	Port     int    `json:"port,omitempty"` // puerto HTTP asignado al añadirlo (se aplica tras initBundle)

	// Host es el destino SSH (usuario@máquina o un alias de ~/.ssh/config).
	// Vacío = server local; si no, Path es la ruta en la máquina remota.
	Host   string  `json:"host,omitempty"`
	Remote *Remote `json:"remote,omitempty"`
}

// IsRemote indica si el server vive en otra máquina (por SSH).
func (s *Server) IsRemote() bool { return s.Host != "" }

// Location es la ruta tal y como se enseña: host:ruta en los remotos.
func (s *Server) Location() string {
	if s.IsRemote() {
		return s.Host + ":" + s.Path
	}
	return s.Path
}

// Remote son los datos que solo tienen los servers remotos.
type Remote struct {
	SSHPort int     `json:"sshPort,omitempty"`
	Log     string  `json:"log,omitempty"` // patrón de logs, relativo al liferay home si no es absoluto
	Control Control `json:"control"`
}

// Tipos de gestión de un server remoto.
const (
	ControlNone    = ""        // lray no lo arranca ni lo para
	ControlSystemd = "systemd" // systemctl start|stop|is-active <service>
	ControlService = "service" // service <service> start|stop|status (init.d)
	ControlCustom  = "custom"  // comandos propios
)

// Control dice cómo se arranca, se para y se consulta un server remoto.
type Control struct {
	Kind    string `json:"kind,omitempty"`
	Service string `json:"service,omitempty"`
	Start   string `json:"start,omitempty"` // solo con kind custom
	Stop    string `json:"stop,omitempty"`
	Status  string `json:"status,omitempty"` // éxito (código 0) = encendido
	Sudo    bool   `json:"sudo,omitempty"`   // anteponer sudo a start y stop
}

// StartCommand es el comando de arranque, con sudo si hace falta; "" si
// lray no sabe arrancarlo.
func (c Control) StartCommand() string { return c.withSudo(c.action("start", c.Start)) }

// StopCommand es el comando de parada, con sudo si hace falta.
func (c Control) StopCommand() string { return c.withSudo(c.action("stop", c.Stop)) }

// StatusCommand comprueba si está encendido (sin sudo: no puede preguntar
// la contraseña). "" = buscar el proceso.
func (c Control) StatusCommand() string {
	switch c.Kind {
	case ControlSystemd:
		return "systemctl is-active --quiet " + ShellQuote(c.Service)
	case ControlService:
		return "service " + ShellQuote(c.Service) + " status"
	case ControlCustom:
		return c.Status
	}
	return ""
}

// SinceCommand imprime desde cuándo está activo (solo systemd).
func (c Control) SinceCommand() string {
	if c.Kind == ControlSystemd {
		return "systemctl show -p ActiveEnterTimestamp " + ShellQuote(c.Service)
	}
	return ""
}

// Describe resume la gestión para enseñarla.
func (c Control) Describe() string {
	switch c.Kind {
	case ControlSystemd:
		return "systemd · " + c.Service + sudoNote(c.Sudo)
	case ControlService:
		return "service · " + c.Service + sudoNote(c.Sudo)
	case ControlCustom:
		return "comandos propios" + sudoNote(c.Sudo)
	}
	return "sin gestionar (solo estado y logs)"
}

func sudoNote(sudo bool) string {
	if sudo {
		return " (con sudo)"
	}
	return ""
}

func (c Control) action(verb, custom string) string {
	switch c.Kind {
	case ControlSystemd:
		return "systemctl " + verb + " " + ShellQuote(c.Service)
	case ControlService:
		return "service " + ShellQuote(c.Service) + " " + verb
	case ControlCustom:
		return custom
	}
	return ""
}

func (c Control) withSudo(cmd string) string {
	if cmd == "" || !c.Sudo {
		return cmd
	}
	return "sudo " + cmd
}

// ShellQuote protege s para pasarlo a sh como una sola palabra.
func ShellQuote(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789@%+=:,./_-") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
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
