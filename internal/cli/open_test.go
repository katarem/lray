package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/katarem/lray/internal/config"
)

func TestFolderLocal(t *testing.T) {
	home := fakeBundle(t)
	s := &config.Server{Name: "uno", Path: home}

	cases := []struct {
		f    folderFlags
		want string
	}{
		{folderFlags{}, home},
		{folderFlags{home: true}, home},
		{folderFlags{deploy: true}, filepath.Join(home, "deploy")},
	}
	for _, c := range cases {
		got, err := c.f.local(s)
		if err != nil || got != c.want {
			t.Errorf("%+v: %q, %v; quería %q", c.f, got, err, c.want)
		}
	}
	if !dirExists(filepath.Join(home, "deploy")) {
		t.Error("no ha creado deploy/")
	}

	if _, err := (folderFlags{logs: true}).local(s); err == nil {
		t.Error("sin logs debería fallar")
	}
	tomcatLogs := filepath.Join(home, "tomcat-9.0.1", "logs")
	if err := os.MkdirAll(tomcatLogs, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, _ := (folderFlags{logs: true}).local(s); got != tomcatLogs {
		t.Errorf("logs = %q, quería los de Tomcat si no hay otros", got)
	}
	if err := os.MkdirAll(filepath.Join(home, "logs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, _ := (folderFlags{logs: true}).local(s); got != filepath.Join(home, "logs") {
		t.Errorf("logs = %q, quería los de Liferay", got)
	}
}

func TestFolderRemote(t *testing.T) {
	s := &config.Server{Name: "pre", Path: "/opt/liferay", Host: "yo@pre", Remote: &config.Remote{}}
	cases := []struct {
		f    folderFlags
		log  string
		want string
	}{
		{folderFlags{}, "", "/opt/liferay"},
		{folderFlags{home: true}, "", "/opt/liferay"},
		{folderFlags{deploy: true}, "", "/opt/liferay/deploy"},
		{folderFlags{logs: true}, "", "/opt/liferay/logs"},
		{folderFlags{logs: true}, "/var/log/liferay/liferay.*.log", "/var/log/liferay"},
	}
	for _, c := range cases {
		s.Remote.Log = c.log
		if got, err := c.f.remote(s); err != nil || got != c.want {
			t.Errorf("%+v log=%q: %q, %v; quería %q", c.f, c.log, got, err, c.want)
		}
	}
	s.Remote.Log = "/var/log/*/liferay.log"
	if _, err := (folderFlags{logs: true}).remote(s); err == nil {
		t.Error("un patrón con comodines en la carpeta debería fallar")
	}
}

func TestSftpURL(t *testing.T) {
	s := &config.Server{Host: "yo@pre", Remote: &config.Remote{}}
	if got := sftpURL(s, "/opt/liferay"); got != "sftp://yo@pre/opt/liferay" {
		t.Errorf("sin puerto: %q", got)
	}
	s.Remote.SSHPort = 2222
	if got := sftpURL(s, "~/liferay"); got != "sftp://yo@pre:2222/~/liferay" {
		t.Errorf("con puerto: %q", got)
	}
}

func TestShellScript(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("necesita sh")
	}
	dir, home := t.TempDir(), t.TempDir()
	fake := filepath.Join(t.TempDir(), "fake-shell")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\necho \"$1 $(pwd)\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(arg string) (string, int) {
		cmd := exec.Command("sh", "-c", shellScript, "lray", arg)
		cmd.Env = append(os.Environ(), "SHELL="+fake, "HOME="+home)
		out, err := cmd.Output()
		if ee, ok := err.(*exec.ExitError); ok {
			return "", ee.ExitCode()
		}
		return strings.TrimSpace(string(out)), 0
	}
	real := func(p string) string { r, _ := filepath.EvalSymlinks(p); return r }

	if out, code := run(dir); code != 0 || out != "-l "+real(dir) {
		t.Errorf("carpeta: %q (código %d)", out, code)
	}
	if out, code := run("~"); code != 0 || out != "-l "+real(home) {
		t.Errorf("~: %q (código %d)", out, code)
	}
	if _, code := run(filepath.Join(dir, "no-existe")); code != noDirCode {
		t.Errorf("carpeta que no existe: código %d, quería %d", code, noDirCode)
	}
}
