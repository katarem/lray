package config

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// Snapshot es el último estado conocido de un server remoto: lray server list
// lo enseña sin conectarse.
type Snapshot struct {
	State     string    `json:"state"`           // running, starting o stopped
	CheckedAt time.Time `json:"checkedAt"`       // última comprobación con éxito
	FailedAt  time.Time `json:"failedAt"`        // último intento sin conexión (posterior a CheckedAt)
	Error     string    `json:"error,omitempty"` // por qué falló ese intento
}

// Reachable indica si la última comprobación llegó al server.
func (s Snapshot) Reachable() bool { return s.FailedAt.IsZero() || s.FailedAt.Before(s.CheckedAt) }

// States guarda los Snapshot por nombre de server (state.json, junto a
// servers.json).
type States struct {
	Servers map[string]Snapshot `json:"servers"`
	file    string
}

// LoadStates lee los últimos estados conocidos; si no hay, devuelve vacío.
func LoadStates() (*States, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	st := &States{Servers: map[string]Snapshot{}, file: filepath.Join(dir, "state.json")}
	data, err := os.ReadFile(st.file)
	if errors.Is(err, fs.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return nil, err
	}
	if json.Unmarshal(data, st) != nil || st.Servers == nil {
		st.Servers = map[string]Snapshot{} // es solo una caché: si está rota se empieza de cero
	}
	return st, nil
}

// Seen apunta un estado comprobado ahora mismo.
func (st *States) Seen(name, state string) {
	st.Servers[name] = Snapshot{State: state, CheckedAt: time.Now()}
}

// Failed apunta que no se pudo comprobar, conservando el último estado bueno.
func (st *States) Failed(name string, err error) {
	s := st.Servers[name]
	s.FailedAt, s.Error = time.Now(), err.Error()
	st.Servers[name] = s
}

// Save escribe los estados de forma atómica.
func (st *States) Save() error {
	if err := os.MkdirAll(filepath.Dir(st.file), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp := st.file + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, st.file)
}

// RecordState carga, apunta y guarda en un paso (para comandos de un server).
func RecordState(name, state string, err error) {
	st, lerr := LoadStates()
	if lerr != nil {
		return
	}
	if err != nil {
		st.Failed(name, err)
	} else {
		st.Seen(name, state)
	}
	_ = st.Save()
}

// ForgetState borra el estado guardado de un server.
func ForgetState(name string) {
	if st, err := LoadStates(); err == nil {
		if _, ok := st.Servers[name]; ok {
			delete(st.Servers, name)
			_ = st.Save()
		}
	}
}
