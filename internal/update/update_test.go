package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestNewer(t *testing.T) {
	tests := []struct {
		current, latest string
		want            bool
	}{
		{"0.1.0", "v0.2.0", true},
		{"v0.2.0", "v0.2.0", false},
		{"0.2.0", "v0.2.0", false},
		{"v0.10.0", "v0.9.9", false},
		{"v1.2.3", "v1.2.4", true},
		{"v1.2.3", "v2.0.0", true},
		{"v1.0.0-rc1", "v1.0.0", true},
		{"v1.0.0", "v1.0.0-rc2", false},
		{"v0.2.0-3-gabc1234", "v0.2.0", false}, // build de make con commits encima
		{"v0.2.0-3-gabc1234-dirty", "v0.2.1", true},
		{"dev", "v9.9.9", false},
		{"v0.1.0", "", false},
		{"v0.1.0", "latest", false},
	}
	for _, tt := range tests {
		if got := Newer(tt.current, tt.latest); got != tt.want {
			t.Errorf("Newer(%q, %q) = %v; quiero %v", tt.current, tt.latest, got, tt.want)
		}
	}
}

func TestIsRelease(t *testing.T) {
	for v, want := range map[string]bool{"v1.0.0": true, "1.0.0": true, "dev": false, "abc1234": false, "": false} {
		if got := IsRelease(v); got != want {
			t.Errorf("IsRelease(%q) = %v; quiero %v", v, got, want)
		}
	}
}

func TestCheckManaged(t *testing.T) {
	if !errors.Is(CheckManaged("/opt/homebrew/Cellar/lray/0.2.0/bin/lray"), ErrManaged) {
		t.Error("un binario de Homebrew debería estar gestionado")
	}
	if CheckManaged("/home/ana/.local/bin/lray") != nil {
		t.Error("un binario de ~/.local/bin no debería estar gestionado")
	}
}

func TestChecksumFor(t *testing.T) {
	sums := []byte("aaa  lray_linux_amd64.tar.gz\nbbb  lray_windows_amd64.zip\n")
	got, err := checksumFor(sums, "lray_windows_amd64.zip")
	if err != nil || got != "bbb" {
		t.Fatalf("checksumFor = %q, %v", got, err)
	}
	if _, err := checksumFor(sums, "lray_plan9_amd64.tar.gz"); err == nil {
		t.Fatal("debería fallar con un asset que no está")
	}
}

// fakeGitHub sirve una release v9.9.9 con el binario indicado para este sistema.
func fakeGitHub(t *testing.T, binary []byte, corrupt bool) {
	t.Helper()
	archive := buildArchive(t, binary)
	sum := sha256.Sum256(archive)
	if corrupt {
		sum[0] ^= 0xff
	}
	sums := fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), Asset())

	mux := http.NewServeMux()
	mux.HandleFunc("/"+DefaultRepo+"/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/"+DefaultRepo+"/releases/tag/v9.9.9", http.StatusFound)
	})
	mux.HandleFunc("/"+DefaultRepo+"/releases/download/v9.9.9/checksums.txt", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(sums))
	})
	mux.HandleFunc("/"+DefaultRepo+"/releases/download/v9.9.9/"+Asset(), func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	old := githubURL
	githubURL = srv.URL
	t.Cleanup(func() { githubURL = old })
	t.Setenv("LRAY_REPO", "")
}

func buildArchive(t *testing.T, binary []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	if runtime.GOOS == "windows" {
		zw := zip.NewWriter(&buf)
		w, err := zw.Create("lray.exe")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write(binary)
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, data := range map[string][]byte{"README.md": []byte("léeme"), "lray": binary} {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		_, _ = tw.Write(data)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestLatest(t *testing.T) {
	fakeGitHub(t, []byte("nuevo"), false)
	tag, err := Latest(context.Background())
	if err != nil || tag != "v9.9.9" {
		t.Fatalf("Latest = %q, %v", tag, err)
	}
}

func TestLatestWithoutReleases(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/"+DefaultRepo+"/releases", http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	old := githubURL
	githubURL = srv.URL
	t.Cleanup(func() { githubURL = old })

	if _, err := Latest(context.Background()); !errors.Is(err, ErrNoRelease) {
		t.Fatalf("quiero ErrNoRelease y tengo %v", err)
	}
}

func TestInstall(t *testing.T) {
	fakeGitHub(t, []byte("binario nuevo"), false)
	exe := filepath.Join(t.TempDir(), "lray")
	if err := os.WriteFile(exe, []byte("binario viejo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Install(context.Background(), "v9.9.9", exe, func(string) {}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	got, _ := os.ReadFile(exe)
	if string(got) != "binario nuevo" {
		t.Fatalf("el binario no se ha sustituido: %q", got)
	}
	entries, _ := os.ReadDir(filepath.Dir(exe))
	if len(entries) != 1 && runtime.GOOS != "windows" {
		t.Fatalf("quedan temporales en la carpeta: %v", entries)
	}
}

func TestInstallBadChecksum(t *testing.T) {
	fakeGitHub(t, []byte("binario nuevo"), true)
	exe := filepath.Join(t.TempDir(), "lray")
	if err := os.WriteFile(exe, []byte("binario viejo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Install(context.Background(), "v9.9.9", exe, func(string) {}); err == nil {
		t.Fatal("con el checksum mal debería fallar")
	}
	got, _ := os.ReadFile(exe)
	if string(got) != "binario viejo" {
		t.Fatal("con el checksum mal no debería tocar el binario")
	}
}
