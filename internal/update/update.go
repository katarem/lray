// Package update comprueba si hay una versión nueva de lray en GitHub y
// sustituye el binario por la de la última release.
package update

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// DefaultRepo es el repo de GitHub del que salen las releases.
const DefaultRepo = "katarem/lray"

// githubURL se cambia en los tests por un servidor local.
var githubURL = "https://github.com"

// CheckInterval es cada cuánto se consulta GitHub como mucho para avisar.
const CheckInterval = 24 * time.Hour

// ErrNoRelease: el repo todavía no tiene ninguna release publicada.
var ErrNoRelease = errors.New("todavía no hay ninguna release publicada")

// ErrManaged: el binario lo gestiona otro instalador (Homebrew).
var ErrManaged = errors.New("lray está instalado con Homebrew")

// Repo es el repo de las releases (LRAY_REPO, igual que install.sh).
func Repo() string {
	if r := strings.TrimSpace(os.Getenv("LRAY_REPO")); r != "" {
		return r
	}
	return DefaultRepo
}

// ReleaseURL es la página de la release con sus notas.
func ReleaseURL(tag string) string {
	return fmt.Sprintf("%s/%s/releases/tag/%s", githubURL, Repo(), tag)
}

// ------------------------------------------------------------ versiones

var semverRe = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.-]+))?`)

// gitDescribeRe reconoce lo que añade git describe a un build hecho con make
// (v0.2.0-3-gabc1234-dirty): commits posteriores a la versión, no anteriores.
var gitDescribeRe = regexp.MustCompile(`^\d+-g[0-9a-f]+(?:-dirty)?$|^dirty$`)

type semver struct {
	nums [3]int
	pre  string
}

func parse(v string) (semver, bool) {
	m := semverRe.FindStringSubmatch(strings.TrimSpace(v))
	if m == nil {
		return semver{}, false
	}
	var s semver
	for i := 0; i < 3; i++ {
		s.nums[i], _ = strconv.Atoi(m[i+1])
	}
	s.pre = m[4]
	if gitDescribeRe.MatchString(s.pre) {
		s.pre = ""
	}
	return s, true
}

// Newer indica si latest es posterior a current. Una versión que no se
// entiende (dev, por ejemplo) nunca se considera desactualizada.
func Newer(current, latest string) bool {
	c, ok1 := parse(current)
	l, ok2 := parse(latest)
	if !ok1 || !ok2 {
		return false
	}
	for i := 0; i < 3; i++ {
		if c.nums[i] != l.nums[i] {
			return l.nums[i] > c.nums[i]
		}
	}
	// Misma versión: una release final va después de su prerelease.
	return c.pre != "" && l.pre == ""
}

// IsRelease indica si la versión es de una release (no dev ni un commit suelto).
func IsRelease(v string) bool {
	_, ok := parse(v)
	return ok
}

// ------------------------------------------------------------ consulta

var client = &http.Client{Timeout: 30 * time.Second}

// Latest consulta el tag de la última release. Usa la redirección de
// github.com/<repo>/releases/latest, que no gasta cupo de la API.
func Latest(ctx context.Context) (string, error) {
	u := fmt.Sprintf("%s/%s/releases/latest", githubURL, Repo())
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, u, nil)
	if err != nil {
		return "", err
	}
	noRedirect := *client
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := noRedirect.Do(req)
	if err != nil {
		return "", fmt.Errorf("no puedo consultar GitHub: %w", err)
	}
	resp.Body.Close()
	loc := resp.Header.Get("Location")
	if resp.StatusCode == http.StatusNotFound || (loc != "" && !strings.Contains(loc, "/releases/tag/")) {
		return "", ErrNoRelease
	}
	if loc == "" {
		return "", fmt.Errorf("GitHub respondió %s al buscar la última versión", resp.Status)
	}
	tag := path.Base(loc)
	if !IsRelease(tag) {
		return "", fmt.Errorf("la última release tiene un tag que no entiendo: %s", tag)
	}
	return tag, nil
}

type cache struct {
	Checked time.Time `json:"checked"`
	Latest  string    `json:"latest,omitempty"`
}

func cacheFile() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "lray", "update.json")
}

// Cached devuelve la última versión conocida, consultando GitHub como mucho
// una vez cada CheckInterval. Si la consulta falla devuelve lo que hubiera.
func Cached(ctx context.Context) string {
	var c cache
	if data, err := os.ReadFile(cacheFile()); err == nil {
		_ = json.Unmarshal(data, &c)
	}
	if time.Since(c.Checked) < CheckInterval {
		return c.Latest
	}
	// Se apunta el intento antes de consultar: sin conexión (o si lray
	// termina antes de que GitHub conteste) no se reintenta hasta mañana.
	writeCache(cache{Checked: time.Now(), Latest: c.Latest})
	if tag, err := Latest(ctx); err == nil {
		c.Latest = tag
		Remember(tag)
	}
	return c.Latest
}

// Remember guarda en la caché una versión recién consultada.
func Remember(tag string) {
	writeCache(cache{Checked: time.Now(), Latest: tag})
}

func writeCache(c cache) {
	data, err := json.Marshal(c)
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(cacheFile()), 0o755)
	_ = os.WriteFile(cacheFile(), data, 0o644)
}

// ------------------------------------------------------------ instalación

// Executable es la ruta real del binario en ejecución (sin enlaces).
func Executable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

// CheckManaged devuelve ErrManaged si el binario es de Homebrew, que debe
// actualizarse con brew para no desincronizar su instalación.
func CheckManaged(exe string) error {
	p := filepath.ToSlash(exe)
	if strings.Contains(p, "/Cellar/") || strings.Contains(p, "/homebrew/") || strings.Contains(p, "/linuxbrew/") {
		return ErrManaged
	}
	return nil
}

// Asset es el nombre del archivo de la release para este sistema
// (el mismo name_template que .goreleaser.yaml).
func Asset() string {
	ext := "tar.gz"
	if runtime.GOOS == "windows" {
		ext = "zip"
	}
	return fmt.Sprintf("lray_%s_%s.%s", runtime.GOOS, runtime.GOARCH, ext)
}

// Install descarga la release tag, verifica su checksum y sustituye el
// binario exe. progress recibe mensajes cortos de lo que va haciendo.
func Install(ctx context.Context, tag, exe string, progress func(string)) error {
	if err := CheckManaged(exe); err != nil {
		return err
	}
	dir := filepath.Dir(exe)
	if err := checkWritable(dir); err != nil {
		return err
	}

	base := fmt.Sprintf("%s/%s/releases/download/%s/", githubURL, Repo(), tag)
	asset := Asset()

	progress("Descargando checksums.txt")
	sums, err := download(ctx, base+"checksums.txt", 1<<20)
	if err != nil {
		return err
	}
	want, err := checksumFor(sums, asset)
	if err != nil {
		return err
	}

	progress("Descargando " + asset)
	archive, err := download(ctx, base+asset, 200<<20)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(archive)
	if got := hex.EncodeToString(sum[:]); !strings.EqualFold(got, want) {
		return fmt.Errorf("el checksum de %s no coincide; no toco nada", asset)
	}

	progress("Extrayendo el binario")
	bin, err := extract(archive)
	if err != nil {
		return err
	}

	progress("Sustituyendo " + exe)
	tmp, err := os.CreateTemp(dir, ".lray-update-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no hace nada si ya se ha renombrado
	if _, err := tmp.Write(bin); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return err
	}
	return replace(exe, tmpName)
}

// replace pone el binario nuevo en su sitio. En Windows no se puede
// sobrescribir un .exe en marcha, pero sí renombrarlo: el viejo queda como
// .old y se borra en la siguiente ejecución (CleanupOld).
func replace(exe, tmp string) error {
	if runtime.GOOS != "windows" {
		return os.Rename(tmp, exe)
	}
	old := exe + ".old"
	_ = os.Remove(old)
	if err := os.Rename(exe, old); err != nil {
		return err
	}
	if err := os.Rename(tmp, exe); err != nil {
		_ = os.Rename(old, exe)
		return err
	}
	return nil
}

// CleanupOld borra el binario que dejó una actualización en Windows.
func CleanupOld() {
	if runtime.GOOS != "windows" {
		return
	}
	if exe, err := Executable(); err == nil {
		_ = os.Remove(exe + ".old")
	}
}

func checkWritable(dir string) error {
	f, err := os.CreateTemp(dir, ".lray-write-test-*")
	if err != nil {
		return fmt.Errorf("no tengo permiso para escribir en %s; vuelve a instalarlo con install.sh o ejecútalo con permisos de administrador", dir)
	}
	name := f.Name()
	f.Close()
	return os.Remove(name)
}

func download(ctx context.Context, url string, max int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	dl := *client
	dl.Timeout = 10 * time.Minute
	resp, err := dl.Do(req)
	if err != nil {
		return nil, fmt.Errorf("no puedo descargar %s: %w", path.Base(url), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("la release no tiene %s; puede que tu sistema no esté soportado", path.Base(url))
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s respondió %s", path.Base(url), resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("%s es demasiado grande", path.Base(url))
	}
	return data, nil
}

// checksumFor busca el sha256 de asset en un checksums.txt de GoReleaser.
func checksumFor(sums []byte, asset string) (string, error) {
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == asset {
			return f[0], nil
		}
	}
	return "", fmt.Errorf("checksums.txt no incluye %s", asset)
}

// extract saca el binario lray (o lray.exe) del archivo de la release.
func extract(archive []byte) ([]byte, error) {
	want := "lray"
	if runtime.GOOS == "windows" {
		want = "lray.exe"
	}
	const max = 200 << 20
	if runtime.GOOS == "windows" {
		zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if err != nil {
			return nil, err
		}
		for _, f := range zr.File {
			if path.Base(f.Name) != want || f.FileInfo().IsDir() {
				continue
			}
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(io.LimitReader(rc, max))
		}
		return nil, fmt.Errorf("el archivo no contiene %s", want)
	}

	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("el archivo no contiene %s", want)
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag == tar.TypeReg && path.Base(h.Name) == want {
			return io.ReadAll(io.LimitReader(tr, max))
		}
	}
}
