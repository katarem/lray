//go:build !windows

package liferay

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDeployTargetUpload(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	r := &Remote{Exec: localExec{}, Path: home}

	tg, err := r.DeployTarget(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, "deploy"); tg.Dir != want || !tg.Writable || tg.Disabled || tg.Staging != "" {
		t.Fatalf("target = %+v, quería %s escribible", tg, want)
	}
	if fi, err := os.Stat(tg.Dir); err != nil || !fi.IsDir() {
		t.Fatalf("no ha creado deploy/: %v", err)
	}

	src := filepath.Join(t.TempDir(), "foo.web-1.0.0.jar")
	writeFile(t, src, "contenido del jar")
	if err := r.Upload(ctx, tg, src); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(tg.Dir, "foo.web-1.0.0.jar"))
	if err != nil || string(got) != "contenido del jar" {
		t.Fatalf("subido = %q, %v", got, err)
	}
	entries, _ := os.ReadDir(tg.Dir)
	if len(entries) != 1 {
		t.Errorf("deploy/ tiene %d ficheros, quería solo el JAR (sin .lray-part)", len(entries))
	}
}

func TestDeployTargetProperties(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, "portal-ext.properties"),
		"# auto.deploy.deploy.dir=/no\nauto.deploy.deploy.dir=/tmp/no\nauto.deploy.enabled = false\r\n")
	writeFile(t, filepath.Join(home, "portal-setup-wizard.properties"),
		"auto.deploy.deploy.dir=${liferay.home}/otro-deploy\n")
	r := &Remote{Exec: localExec{}, Path: home}

	tg, err := r.DeployTarget(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, "otro-deploy"); tg.Dir != want {
		t.Errorf("Dir = %q, quería %q", tg.Dir, want)
	}
	if !tg.Disabled {
		t.Error("auto.deploy.enabled = false no se ha visto")
	}
}

func TestDeployTargetMissingHome(t *testing.T) {
	r := &Remote{Exec: localExec{}, Path: filepath.Join(t.TempDir(), "no-existe")}
	if _, err := r.DeployTarget(context.Background()); err == nil || !strings.Contains(err.Error(), "no existe") {
		t.Fatalf("err = %v", err)
	}
}

// Sin permiso en deploy/ se sube a una temporal y se mueve con el script de
// sudo (aquí, sin sudo: se devuelve el permiso antes de ejecutarlo).
func TestDeployTargetStaging(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root puede escribir en cualquier carpeta")
	}
	ctx := context.Background()
	home := t.TempDir()
	dir := filepath.Join(home, "deploy")
	if err := os.Mkdir(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	r := &Remote{Exec: localExec{}, Path: home}

	tg, err := r.DeployTarget(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if tg.Writable || tg.Staging == "" {
		t.Fatalf("target = %+v, quería carpeta temporal", tg)
	}
	if want := fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()); tg.Owner != want {
		t.Errorf("Owner = %q, quería %q", tg.Owner, want)
	}
	defer func() {
		if err := r.Cleanup(ctx, tg); err != nil {
			t.Error(err)
		}
		if _, err := os.Stat(tg.Staging); !os.IsNotExist(err) {
			t.Errorf("la carpeta temporal sigue ahí: %v", err)
		}
	}()

	src := filepath.Join(t.TempDir(), "tema.war")
	writeFile(t, src, "war")
	if err := r.Upload(ctx, tg, src); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(tg.Staging, "tema.war")); err != nil {
		t.Fatalf("no está en la temporal: %v", err)
	}

	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	script, args := tg.InstallScript([]string{"tema.war"})
	if out, err := r.Exec.Run(ctx, script, args...); err != nil {
		t.Fatalf("install: %v %s", err, out)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "tema.war")); err != nil || string(got) != "war" {
		t.Fatalf("instalado = %q, %v", got, err)
	}
}

func TestUploadRejectsHiddenNames(t *testing.T) {
	r := &Remote{Exec: localExec{}, Path: t.TempDir()}
	tg := &DeployTarget{Dir: "/tmp", Writable: true}
	if err := r.Upload(context.Background(), tg, "/x/.oculto.jar"); err == nil {
		t.Fatal("quería error con un nombre que empieza por punto")
	}
}

// El dueño de lo que se sube con sudo es el de osgi/modules, no el de deploy/.
func TestDeployTargetOwnerFromModules(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("hace falta root para cambiar el dueño de una carpeta")
	}
	home := t.TempDir()
	modules := filepath.Join(home, "osgi", "modules")
	if err := os.MkdirAll(modules, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(modules, 65534, 65534); err != nil {
		t.Fatal(err)
	}
	r := &Remote{Exec: localExec{}, Path: home}
	tg, err := r.DeployTarget(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if tg.Owner != "65534:65534" {
		t.Errorf("Owner = %q, quería el de osgi/modules (65534:65534)", tg.Owner)
	}
}
