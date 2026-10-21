// Package shellcomp instala el autocompletado de lray en las shells del
// usuario sin que tenga que hacer nada: guarda el script de cada shell en una
// carpeta de lray y lo carga desde su fichero de arranque (~/.bashrc,
// ~/.zshrc, el perfil de PowerShell) con un bloque marcado, que se actualiza
// en su sitio en vez de duplicarse. Fish no necesita bloque: carga solo lo que
// hay en ~/.config/fish/completions.
//
// Los scripts no llevan los nombres de los servers: le preguntan a lray al
// pulsar Tab (lray __complete ...), así que siempre están al día.
package shellcomp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Generators escriben el script de cada shell.
type Generators struct {
	Bash, Zsh, Fish, PowerShell func(io.Writer) error
}

// Result dice qué se ha instalado.
type Result struct {
	Shells []string // shells con autocompletado
	RCs    []string // ficheros de arranque modificados ahora (abre otra terminal para notarlo)
}

const (
	begin = "# >>> lray: autocompletado (lo mantiene lray, no lo edites) >>>"
	end   = "# <<< lray: autocompletado <<<"
)

// Install escribe (o refresca) el autocompletado de cada shell que use el
// usuario y apunta la versión de lray que lo hizo. No toca nada que ya esté
// igual. Los fallos de una shell no impiden instalar las demás.
func Install(version string, gen Generators) (Result, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Result{}, err
	}
	var res Result
	var errs []error
	for _, sh := range shells(home) {
		changed, err := sh.install(gen)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", sh.name, err))
			continue
		}
		res.Shells = append(res.Shells, sh.name)
		if changed != "" {
			res.RCs = append(res.RCs, changed)
		}
	}
	// Se apunta aunque alguna falle: reintentarlo en cada comando no lo
	// arreglaría y lo haría más lento. Se vuelve a probar con la siguiente
	// versión o al añadir o quitar un server.
	writeStamp(stamp{Version: version, At: time.Now(), Shells: res.Shells})
	return res, errors.Join(errs...)
}

// Stale indica si esta versión de lray aún no ha instalado el autocompletado.
func Stale(version string) bool {
	return readStamp().Version != version
}

// Status es lo último que se instaló (para el informe de depuración).
func Status() string {
	st := readStamp()
	if st.Version == "" {
		return "sin instalar"
	}
	return fmt.Sprintf("instalado por lray %s el %s en %s", st.Version, st.At.Format("2006-01-02 15:04"), strings.Join(st.Shells, ", "))
}

// ------------------------------------------------------------------ shells

type shell struct {
	name   string
	script string // dónde va el script
	rc     string // fichero de arranque que lo carga; "" = la shell lo carga sola
	block  string // líneas que van en rc entre las marcas
	gen    func(Generators) func(io.Writer) error
}

// shells decide dónde instalar: en las shells que tienen fichero de arranque
// y en la que tienes por defecto ($SHELL), aunque aún no lo tenga.
func shells(home string) []shell {
	dir := dataDir(home)
	login := filepath.Base(os.Getenv("SHELL"))
	var out []shell

	if rc := bashRC(home, login == "bash"); rc != "" {
		script := filepath.Join(dir, "lray.bash")
		out = append(out, shell{
			name: "bash", script: script, rc: rc,
			block: fmt.Sprintf("[ -f %[1]s ] && . %[1]s", shQuote(script)),
			gen:   func(g Generators) func(io.Writer) error { return g.Bash },
		})
	}

	zdot := os.Getenv("ZDOTDIR")
	if zdot == "" {
		zdot = home
	}
	if rc := filepath.Join(zdot, ".zshrc"); exists(rc) || login == "zsh" {
		script := filepath.Join(dir, "lray.zsh")
		out = append(out, shell{
			name: "zsh", script: script, rc: rc,
			// El script se registra con compdef, que solo existe tras compinit.
			block: fmt.Sprintf("if [ -f %[1]s ]; then\n  (( $+functions[compdef] )) || { autoload -Uz compinit && compinit; }\n  source %[1]s\nfi", shQuote(script)),
			gen:   func(g Generators) func(io.Writer) error { return g.Zsh },
		})
	}

	fishDir := filepath.Join(configHome(home), "fish")
	if exists(fishDir) || login == "fish" {
		out = append(out, shell{
			name: "fish", script: filepath.Join(fishDir, "completions", "lray.fish"),
			gen: func(g Generators) func(io.Writer) error { return g.Fish },
		})
	}

	if runtime.GOOS == "windows" {
		if rc := powershellProfile(); rc != "" {
			script := filepath.Join(dir, "lray.ps1")
			out = append(out, shell{
				name: "powershell", script: script, rc: rc,
				block: fmt.Sprintf("if (Test-Path '%[1]s') { . '%[1]s' }", strings.ReplaceAll(script, "'", "''")),
				gen:   func(g Generators) func(io.Writer) error { return g.PowerShell },
			})
		}
	}
	return out
}

// install escribe el script y, si hace falta, el bloque en el fichero de
// arranque. Devuelve el fichero de arranque si lo ha modificado.
func (s shell) install(g Generators) (string, error) {
	gen := s.gen(g)
	if gen == nil {
		return "", errors.New("no sé generar su script")
	}
	var buf bytes.Buffer
	if err := gen(&buf); err != nil {
		return "", err
	}
	if err := writeIfChanged(s.script, buf.Bytes()); err != nil {
		return "", err
	}
	if s.rc == "" {
		return "", nil
	}
	changed, err := ensureBlock(s.rc, s.block)
	if err != nil || !changed {
		return "", err
	}
	return s.rc, nil
}

// bashRC elige el fichero de arranque de bash: ~/.bashrc, salvo en macOS,
// donde las terminales abren shells de login y leen ~/.bash_profile. Sin
// ninguno, solo se crea si bash es tu shell.
func bashRC(home string, isLogin bool) string {
	names := []string{".bashrc"}
	if runtime.GOOS == "darwin" {
		names = []string{".bash_profile", ".bashrc"}
	}
	for _, n := range names {
		if p := filepath.Join(home, n); exists(p) {
			return p
		}
	}
	if isLogin {
		return filepath.Join(home, names[0])
	}
	return ""
}

// powershellProfile pregunta a PowerShell por su perfil del usuario (cambia
// con la versión y con OneDrive, así que no se adivina).
func powershellProfile() string {
	for _, exe := range []string{"pwsh", "powershell"} {
		if _, err := exec.LookPath(exe); err != nil {
			continue
		}
		out, err := exec.Command(exe, "-NoProfile", "-NonInteractive", "-Command", "$PROFILE.CurrentUserAllHosts").Output()
		if p := strings.TrimSpace(string(out)); err == nil && p != "" {
			return p
		}
	}
	return ""
}

// dataDir es la carpeta de los scripts: $XDG_DATA_HOME/lray/completion
// (~/.local/share/...) o %LOCALAPPDATA%\lray\completion en Windows.
func dataDir(home string) string {
	if runtime.GOOS == "windows" {
		if d := os.Getenv("LOCALAPPDATA"); d != "" {
			return filepath.Join(d, "lray", "completion")
		}
	}
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "lray", "completion")
}

func configHome(home string) string {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return d
	}
	return filepath.Join(home, ".config")
}

// ------------------------------------------------------------ ficheros

// ensureBlock deja en rc el bloque de lray con body, sustituyendo el que
// hubiera. Si rc es un enlace (dotfiles), escribe en el fichero enlazado.
func ensureBlock(rc, body string) (bool, error) {
	if real, err := filepath.EvalSymlinks(rc); err == nil {
		rc = real
	}
	old, err := os.ReadFile(rc)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	nl := "\n"
	if bytes.Contains(old, []byte("\r\n")) {
		nl = "\r\n"
	}
	block := strings.ReplaceAll(begin+"\n"+body+"\n"+end, "\n", nl)
	text := disableLegacy(string(old))
	var updated string
	if i := strings.Index(text, begin); i >= 0 {
		j := strings.Index(text[i:], end)
		if j < 0 {
			return false, fmt.Errorf("%s tiene el bloque de lray a medias; borra desde «%s» y vuelvo a ponerlo", rc, begin)
		}
		updated = text[:i] + block + text[i+j+len(end):]
	} else {
		sep := ""
		if text != "" && !strings.HasSuffix(text, "\n") {
			sep = nl
		}
		if text != "" {
			sep += nl
		}
		updated = text + sep + block + nl
	}
	if updated == string(old) {
		return false, nil
	}
	return true, writeFile(rc, []byte(updated))
}

// legacyNote encabeza las líneas antiguas que cargaban el autocompletado con
// el comando completion, que ya no existe.
const legacyNote = "# (desactivado por lray: el autocompletado ya se carga solo, más abajo) "

// disableLegacy comenta las líneas que cargaban el autocompletado con
// «lray completion ...» (source <(lray completion zsh), eval "$(...)"):
// ese comando ya no existe y, al abrir la terminal, darían error.
func disableLegacy(text string) string {
	lines := strings.SplitAfter(text, "\n")
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if strings.Contains(t, "lray completion") && !strings.HasPrefix(t, "#") {
			lines[i] = legacyNote + l
		}
	}
	return strings.Join(lines, "")
}

func writeIfChanged(path string, data []byte) error {
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, data) {
		return nil
	}
	return writeFile(path, data)
}

// writeFile escribe de forma atómica conservando los permisos que tuviera.
func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	mode := fs.FileMode(0o644)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	tmp := path + ".lray-tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// ------------------------------------------------------------------ marca

type stamp struct {
	Version string    `json:"version"`
	At      time.Time `json:"at"`
	Shells  []string  `json:"shells"`
}

func stampFile() string {
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	return filepath.Join(base, "lray", "completion.json")
}

func readStamp() stamp {
	var st stamp
	if data, err := os.ReadFile(stampFile()); err == nil {
		_ = json.Unmarshal(data, &st)
	}
	return st
}

func writeStamp(st stamp) {
	data, err := json.MarshalIndent(st, "", "  ")
	if err == nil {
		_ = writeFile(stampFile(), data)
	}
}
