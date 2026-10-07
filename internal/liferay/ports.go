package liferay

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
)

// DefaultPort es el puerto HTTP de un Tomcat de Liferay recién descargado.
const DefaultPort = 8080

// defaultPorts son los puertos que abre un Tomcat de Liferay sin tocar:
// el de apagado (8005) y el conector HTTP.
var defaultPorts = []int{8005, DefaultPort}

// Ports devuelve todos los puertos en los que escuchará Tomcat: el de apagado
// y el de cada conector del server.xml. Sin bundle, los de por defecto.
func (l *Layout) Ports() []int {
	if l.Tomcat == "" {
		return append([]int(nil), defaultPorts...)
	}
	data, err := os.ReadFile(l.serverXML())
	if err != nil {
		return append([]int(nil), defaultPorts...)
	}
	var srv struct {
		Port     string `xml:"port,attr"`
		Services []struct {
			Connectors []struct {
				Port string `xml:"port,attr"`
			} `xml:"Connector"`
		} `xml:"Service"`
	}
	if xml.Unmarshal(data, &srv) != nil {
		return append([]int(nil), defaultPorts...)
	}
	var out []int
	add := func(s string) {
		if p, err := strconv.Atoi(s); err == nil && p > 0 {
			out = append(out, p)
		}
	}
	add(srv.Port)
	for _, s := range srv.Services {
		for _, c := range s.Connectors {
			add(c.Port)
		}
	}
	if len(out) == 0 {
		return append([]int(nil), defaultPorts...)
	}
	return out
}

// ShiftPorts suma delta a cada puerto.
func ShiftPorts(ports []int, delta int) []int {
	out := make([]int, len(ports))
	for i, p := range ports {
		out[i] = p + delta
	}
	return out
}

var portAttrRe = regexp.MustCompile(`(\s(?:port|redirectPort)\s*=\s*")(\d+)(")`)

// SetPort cambia el puerto HTTP del server.xml a port y desplaza el resto
// (apagado, AJP, redirect...) la misma cantidad para que dos bundles con
// puertos distintos no choquen en ninguno. Respeta el formato del fichero.
func (l *Layout) SetPort(port int) error {
	if !l.HasBundle() {
		return ErrNoBundle
	}
	if port < 1 || port > 65535 {
		return fmt.Errorf("puerto no válido: %d", port)
	}
	delta := port - l.Port()
	if delta == 0 {
		return nil
	}
	for _, p := range l.Ports() {
		if q := p + delta; q < 1 || q > 65535 {
			return fmt.Errorf("no puedo mover el puerto %d a %d: se sale del rango", p, q)
		}
	}
	path := l.serverXML()
	st, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("no encuentro el server.xml: %w", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	out := portAttrRe.ReplaceAllFunc(data, func(m []byte) []byte {
		sub := portAttrRe.FindSubmatch(m)
		p, err := strconv.Atoi(string(sub[2]))
		if err != nil || p <= 0 {
			return m
		}
		return []byte(string(sub[1]) + strconv.Itoa(p+delta) + string(sub[3]))
	})
	tmp := path + ".lray.tmp"
	if err := os.WriteFile(tmp, out, st.Mode().Perm()); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	l.port = 0
	return nil
}

func (l *Layout) serverXML() string { return filepath.Join(l.Tomcat, "conf", "server.xml") }
