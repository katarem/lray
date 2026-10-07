// Package liferay sabe encontrar, inspeccionar, arrancar y parar bundles de Liferay.
package liferay

import (
	"bufio"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// ErrNotLiferay indica que la ruta no es ni un workspace ni un bundle.
var ErrNotLiferay = errors.New("no parece un workspace ni un bundle de Liferay")

// Layout describe dónde está cada pieza de un entorno Liferay.
type Layout struct {
	Workspace string // raíz del workspace ("" si es un bundle suelto)
	Home      string // liferay.home (bundles/)
	Tomcat    string // carpeta tomcat ("" si aún no hay bundle)

	port int
}

// Resolve acepta la raíz de un workspace, un liferay home o una carpeta tomcat.
func Resolve(path string) (*Layout, error) {
	abs, err := filepath.Abs(ExpandHome(path))
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("la ruta %s no existe", abs)
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("%s no es una carpeta", abs)
	}

	l := &Layout{}
	switch {
	case IsWorkspace(abs):
		l.Workspace = abs
		l.Home = workspaceHome(abs)
	case isTomcatDir(abs):
		l.Tomcat = abs
		l.Home = filepath.Dir(abs)
	default:
		l.Home = abs
		if parent := filepath.Dir(abs); IsWorkspace(parent) && workspaceHome(parent) == abs {
			l.Workspace = parent
		}
	}
	if l.Tomcat == "" {
		l.Tomcat = findTomcat(l.Home)
	}
	if l.Workspace == "" && l.Tomcat == "" {
		return nil, ErrNotLiferay
	}
	return l, nil
}

// Root es la ruta que conviene registrar: el workspace si existe, si no el home.
func (l *Layout) Root() string {
	if l.Workspace != "" {
		return l.Workspace
	}
	return l.Home
}

// HasBundle indica si ya hay un Tomcat con Liferay descargado.
func (l *Layout) HasBundle() bool { return l.Tomcat != "" }

// PIDFile es donde lray guarda el PID del proceso de Tomcat.
func (l *Layout) PIDFile() string { return filepath.Join(l.Home, ".lray.pid") }

// CatalinaOut es el log de consola de Tomcat (incluye los logs de Liferay).
func (l *Layout) CatalinaOut() string { return filepath.Join(l.Tomcat, "logs", "catalina.out") }

// LogFile elige el mejor fichero para seguir: catalina.out o, si no existe
// (server arrancado desde un IDE, por ejemplo), el liferay.*.log más reciente.
func (l *Layout) LogFile() string {
	if fileExists(l.CatalinaOut()) {
		return l.CatalinaOut()
	}
	if logs := liferayLogs(l.Home); len(logs) > 0 {
		return logs[0]
	}
	return l.CatalinaOut()
}

// Port devuelve el puerto HTTP leído del server.xml (8080 por defecto).
func (l *Layout) Port() int {
	if l.port != 0 {
		return l.port
	}
	l.port = DefaultPort
	if l.Tomcat == "" {
		return l.port
	}
	data, err := os.ReadFile(l.serverXML())
	if err != nil {
		return l.port
	}
	if p := tomcatPort(data); p > 0 {
		l.port = p
	}
	return l.port
}

// tomcatPort lee el primer conector HTTP (ni AJP ni SSL) de un server.xml.
func tomcatPort(data []byte) int {
	var srv struct {
		Services []struct {
			Connectors []struct {
				Port       string `xml:"port,attr"`
				Protocol   string `xml:"protocol,attr"`
				SSLEnabled string `xml:"SSLEnabled,attr"`
			} `xml:"Connector"`
		} `xml:"Service"`
	}
	if xml.Unmarshal(data, &srv) != nil {
		return 0
	}
	for _, s := range srv.Services {
		for _, c := range s.Connectors {
			if strings.Contains(strings.ToUpper(c.Protocol), "AJP") || strings.EqualFold(c.SSLEnabled, "true") {
				continue
			}
			if p, err := strconv.Atoi(c.Port); err == nil && p > 0 {
				return p
			}
		}
	}
	return 0
}

// IsWorkspace comprueba si dir es la raíz de un Liferay Workspace (Gradle).
func IsWorkspace(dir string) bool {
	for _, f := range []string{"settings.gradle", "settings.gradle.kts"} {
		data, err := os.ReadFile(filepath.Join(dir, f))
		if err == nil && (bytes.Contains(data, []byte("com.liferay.workspace")) ||
			bytes.Contains(data, []byte("com.liferay.gradle.plugins.workspace"))) {
			return true
		}
	}
	return false
}

// FindWorkspace sube desde dir hasta encontrar la raíz de un workspace.
func FindWorkspace(dir string) (string, bool) {
	dir, _ = filepath.Abs(dir)
	for {
		if IsWorkspace(dir) {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

func workspaceHome(ws string) string {
	h := ReadProperties(filepath.Join(ws, "gradle.properties"))["liferay.workspace.home.dir"]
	if h == "" {
		h = "bundles"
	}
	if !filepath.IsAbs(h) {
		h = filepath.Join(ws, h)
	}
	return filepath.Clean(h)
}

func isTomcatDir(dir string) bool {
	return fileExists(filepath.Join(dir, "bin", "catalina.sh")) ||
		fileExists(filepath.Join(dir, "bin", "catalina.bat"))
}

func findTomcat(home string) string {
	entries, err := os.ReadDir(home)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "tomcat") {
			p := filepath.Join(home, e.Name())
			if isTomcatDir(p) {
				return p
			}
		}
	}
	return ""
}

// liferayLogs devuelve los liferay.*.log del home, el más reciente primero.
func liferayLogs(home string) []string {
	matches, _ := filepath.Glob(filepath.Join(home, "logs", "liferay.*.log"))
	sort.Slice(matches, func(i, j int) bool { return modTime(matches[i]) > modTime(matches[j]) })
	return matches
}

// ---------------------------------------------------------------- versión

var startingRe = regexp.MustCompile(`(?m)Starting (Liferay [^(\r\n]+?)\s*(?:\(|\r?$)`)

// DetectVersion intenta averiguar la versión: primero por el gradle.properties
// del workspace y, si no, buscando el "Starting Liferay ..." de los logs.
func DetectVersion(l *Layout) string {
	if l.Workspace != "" {
		p := ReadProperties(filepath.Join(l.Workspace, "gradle.properties"))
		for _, k := range []string{"liferay.workspace.product", "liferay.workspace.target.platform.version"} {
			if v := p[k]; v != "" {
				return v
			}
		}
		if u := p["liferay.workspace.bundle.url"]; u != "" {
			name := filepath.Base(u)
			for _, ext := range []string{".tar.gz", ".zip", ".7z"} {
				name = strings.TrimSuffix(name, ext)
			}
			return name
		}
	}
	if l.Home == "" {
		return ""
	}
	candidates := liferayLogs(l.Home)
	if len(candidates) > 3 {
		candidates = candidates[:3]
	}
	if l.Tomcat != "" {
		candidates = append(candidates, l.CatalinaOut())
	}
	for _, f := range candidates {
		m := startingRe.FindAllSubmatch(readTail(f, 4<<20), -1)
		if len(m) > 0 {
			return prettyVersion(string(m[len(m)-1][1]))
		}
	}
	return ""
}

func prettyVersion(v string) string {
	r := strings.NewReplacer(
		"Liferay Digital Experience Platform", "DXP",
		"Liferay Community Edition Portal", "Portal CE",
		"Liferay Portal Community Edition", "Portal CE",
		"Liferay DXP", "DXP",
		"Liferay Portal", "Portal",
	)
	return strings.TrimSpace(r.Replace(v))
}

// ---------------------------------------------------------------- utilidades

// ReadProperties lee un .properties sencillo (con continuaciones de línea).
func ReadProperties(path string) map[string]string {
	m := map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		return m
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	var buf strings.Builder
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if buf.Len() == 0 && (line == "" || line[0] == '#' || line[0] == '!') {
			continue
		}
		if strings.HasSuffix(line, `\`) {
			buf.WriteString(strings.TrimSuffix(line, `\`))
			continue
		}
		buf.WriteString(line)
		line = buf.String()
		buf.Reset()
		if i := strings.IndexAny(line, "=:"); i > 0 {
			m[strings.TrimSpace(line[:i])] = strings.TrimSpace(line[i+1:])
		}
	}
	return m
}

// ExpandHome sustituye ~ por la carpeta del usuario.
func ExpandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[1:])
		}
	}
	return p
}

// FileSize devuelve el tamaño de un fichero (0 si no existe).
func FileSize(path string) int64 {
	st, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return st.Size()
}

func readTail(path string, max int64) []byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil
	}
	if st.Size() > max {
		_, _ = f.Seek(st.Size()-max, io.SeekStart)
	}
	data, _ := io.ReadAll(f)
	return data
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func modTime(p string) int64 {
	st, err := os.Stat(p)
	if err != nil {
		return 0
	}
	return st.ModTime().UnixNano()
}
