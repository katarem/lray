// Package scaffold crea workspaces de Liferay sin blade.
package scaffold

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Release es una entrada del releases.json oficial de Liferay.
type Release struct {
	Product             string   `json:"product"`
	ProductGroupVersion string   `json:"productGroupVersion"`
	ProductVersion      string   `json:"productVersion"`
	Promoted            string   `json:"promoted"`
	ReleaseKey          string   `json:"releaseKey"`
	Tags                []string `json:"tags"`
	URL                 string   `json:"url"`
}

// IsPromoted indica si Liferay la destaca (la última de cada rama).
func (r Release) IsPromoted() bool { return r.Promoted == "true" }

// HasTag comprueba una etiqueta (recommended, supported, jakarta...).
func (r Release) HasTag(tag string) bool {
	for _, t := range r.Tags {
		if t == tag {
			return true
		}
	}
	return false
}

// Label es el texto que se enseña en el selector.
func (r Release) Label() string {
	label := r.ProductVersion
	if label == "" {
		label = r.ReleaseKey
	}
	if r.HasTag("recommended") {
		label += "  ★ recomendada"
	}
	return label
}

var releaseURLs = []string{
	"https://releases-cdn.liferay.com/releases.json",
	"https://releases.liferay.com/releases.json",
	// Espejo comunitario por si el CDN de Liferay está caído (pasa a menudo).
	"https://raw.githubusercontent.com/lgdd/liferay-product-info/main/releases.json",
}

const cacheTTL = 24 * time.Hour

// LoadReleases descarga (o lee de la caché de 24 h) la lista de versiones.
func LoadReleases(ctx context.Context) ([]Release, error) {
	cache := cacheFile()
	if st, err := os.Stat(cache); err == nil && time.Since(st.ModTime()) < cacheTTL {
		if rs, err := parseFile(cache); err == nil {
			return rs, nil
		}
	}

	client := &http.Client{Timeout: 20 * time.Second}
	var lastErr error
	for _, u := range releaseURLs {
		data, err := fetch(ctx, client, u)
		if err != nil {
			lastErr = err
			continue
		}
		var rs []Release
		if err := json.Unmarshal(data, &rs); err != nil || len(rs) == 0 {
			lastErr = fmt.Errorf("respuesta inesperada de %s", u)
			continue
		}
		_ = os.MkdirAll(filepath.Dir(cache), 0o755)
		_ = os.WriteFile(cache, data, 0o644)
		return rs, nil
	}
	if rs, err := parseFile(cache); err == nil { // caché caducada mejor que nada
		return rs, nil
	}
	if lastErr == nil {
		lastErr = errors.New("sin conexión")
	}
	return nil, lastErr
}

func fetch(ctx context.Context, c *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s respondió %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

func parseFile(p string) ([]Release, error) {
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var rs []Release
	return rs, json.Unmarshal(data, &rs)
}

func cacheFile() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "lray", "releases.json")
}

// ForProduct filtra por producto ("dxp" o "portal") y ordena: primero las
// destacadas, y dentro de cada grupo de la más nueva a la más vieja.
func ForProduct(all []Release, product string) []Release {
	var out []Release
	for _, r := range all {
		if r.Product == product {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.IsPromoted() != b.IsPromoted() {
			return a.IsPromoted()
		}
		ga, gb := groupKey(a.ProductGroupVersion), groupKey(b.ProductGroupVersion)
		if ga != gb {
			return ga > gb
		}
		return patch(a.ReleaseKey) > patch(b.ReleaseKey)
	})
	return out
}

// "2025.q2" -> 20252 ; "7.4" -> 20224 (las 7.x van antes que las trimestrales)
func groupKey(g string) int {
	parts := strings.SplitN(strings.ToLower(g), ".", 2)
	if len(parts) != 2 {
		return 0
	}
	major, _ := strconv.Atoi(parts[0])
	minor, _ := strconv.Atoi(strings.TrimPrefix(parts[1], "q"))
	if major < 100 {
		return 20150 + minor*10 // 7.0..7.4 -> 20150..20190
	}
	return major*10 + minor
}

var patchRe = regexp.MustCompile(`(\d+)(?:-lts)?$`)

func patch(key string) int {
	m := patchRe.FindStringSubmatch(key)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}
