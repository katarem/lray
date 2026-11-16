package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katarem/lray/internal/config"
	"github.com/katarem/lray/internal/liferay"
)

// bundleWith es fakeBundle con el portal-ext.properties dado.
func bundleWith(t *testing.T, portalExt string) string {
	t.Helper()
	home := fakeBundle(t)
	if portalExt != "" {
		if err := os.WriteFile(filepath.Join(home, "portal-ext.properties"), []byte(portalExt), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

func TestResolveGogoLocal(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		ext, flag, addr string
		set, off        bool
	}{
		{"include-and-override=portal-developer.properties\n", "", "localhost:11311", false, false},
		{"module.framework.properties.osgi.console=0.0.0.0:12311\n", "", "localhost:12311", true, false},
		{"module.framework.properties.osgi.console=\n", "", "", false, true},
		{"module.framework.properties.osgi.console=\n", "127.0.0.1:13311", "127.0.0.1:13311", true, false},
		{"", "14311", "localhost:14311", true, false},
	} {
		s := &config.Server{Name: "x", Path: bundleWith(t, tc.ext)}
		g, err := resolveGogo(ctx, s, tc.flag)
		if err != nil {
			t.Fatalf("%q/%q: %v", tc.ext, tc.flag, err)
		}
		if g.off != tc.off || (!tc.off && (g.addr() != tc.addr || g.set != tc.set)) {
			t.Errorf("%q/%q: addr=%s set=%v off=%v", tc.ext, tc.flag, g.addr(), g.set, g.off)
		}
	}
}

func TestGogoErrExplains(t *testing.T) {
	s := &config.Server{Name: "x"}
	off := gogoErr(s, gogoTarget{off: true}, nil)
	if !strings.Contains(off.Error(), "desactivada") || !strings.Contains(off.Error(), liferay.GogoProperty) {
		t.Errorf("desactivada: %v", off)
	}
	notSet := gogoErr(s, gogoTarget{host: "localhost", port: 11311}, liferay.ErrGogoRefused)
	if !strings.Contains(notSet.Error(), "no la tiene activada") {
		t.Errorf("sin configurar: %v", notSet)
	}
	set := gogoErr(s, gogoTarget{host: "localhost", port: 11311, set: true}, liferay.ErrGogoRefused)
	if !strings.Contains(set.Error(), "¿Está arrancado Liferay?") {
		t.Errorf("configurada: %v", set)
	}
	other := errors.New("otra cosa")
	if gogoErr(s, gogoTarget{}, other) != other {
		t.Error("los demás errores pasan tal cual")
	}
}
