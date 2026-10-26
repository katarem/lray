package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/katarem/lray/internal/config"
	"github.com/katarem/lray/internal/ssh"
	"github.com/katarem/lray/internal/ui"
)

// remoteDeploy compila en local y sube lo nuevo a la carpeta de autodeploy
// del remoto. Gradle despliega en una carpeta propia del server
// (deployStage), que hace de copia de lo que ya se ha subido: solo se sube lo
// que Gradle deja distinto en ella. Con clean, todo lo que Gradle escriba
// (aunque salga idéntico), por si alguien ha desplegado otra versión encima.
func remoteDeploy(ctx context.Context, s *config.Server, ws, cwd string, clean, yes bool) error {
	if !yes && !ui.IsTTY() {
		return ui.ErrNeedsTTY
	}
	conn, r := remoteOf(s)
	if err := connect(ctx, s, conn); err != nil {
		config.RecordState(s.Name, "", err)
		return err
	}
	t, err := r.DeployTarget(ctx)
	if err != nil {
		return fmt.Errorf("no puedo desplegar en «%s»: %w", s.Name, remoteErr(s, err))
	}
	defer func() { _ = r.Cleanup(context.Background(), t) }()
	if t.Disabled {
		return fmt.Errorf("«%s» tiene el autodeploy desactivado (auto.deploy.enabled=false): lo que deje en %s no se instalaría", s.Name, t.Dir)
	}

	stage, err := deployStage(s.Name)
	if err != nil {
		return err
	}
	before, err := snapshotDir(stage)
	if err != nil {
		return err
	}
	var tasks []string
	if clean {
		tasks = append(tasks, "clean")
	}
	tasks = append(tasks, "deploy")
	var since time.Time
	if clean {
		since = time.Now().Add(-time.Second) // margen por la resolución de mtime
	}
	res, buildErr := gradleDeploy(fmt.Sprintf("Compilando %s para «%s»", scopeOf(ws, cwd), s.Name), "Compilado",
		ws, cwd, s.JavaHome, stage, tasks)
	after, err := snapshotDir(stage)
	if err != nil {
		return err
	}
	changed := changedFiles(before, after, since)
	// Lo que no llegue a subirse se borra de la copia: si no, Gradle lo daría
	// por desplegado la próxima vez y no se volvería a subir.
	uploaded := map[string]bool{}
	defer func() {
		for _, rel := range changed {
			if !uploaded[rel] {
				_ = os.Remove(filepath.Join(stage, rel))
			}
		}
	}()
	if errors.Is(buildErr, ui.ErrInterrupted) {
		return buildErr
	}
	if buildErr != nil {
		printGradleFailure(res.tail)
		return errors.New("la compilación ha fallado; arriba tienes los errores")
	}

	var unchanged []string
	for _, p := range res.unchanged {
		unchanged = append(unchanged, ui.MutedText("= "+p+" (sin cambios)"))
	}
	if len(changed) == 0 {
		body := append(unchanged, "",
			"Lo compilado ya estaba subido a "+s.Host+".",
			ui.MutedText("Si alguien ha desplegado otra versión encima, vuelve a subirlo con ")+ui.Code("lray server deploy "+s.Name+" --clean"))
		ui.Say(ui.Thinking, fmt.Sprintf("Nada nuevo para «%s»", s.Name), body...)
		return nil
	}
	names := make([]string, len(changed))
	seen := map[string]string{}
	for i, rel := range changed {
		names[i] = filepath.Base(rel)
		if other, ok := seen[names[i]]; ok {
			return fmt.Errorf("%s y %s se llaman igual y en %s uno pisaría al otro", other, rel, t.Dir)
		}
		seen[names[i]] = rel
	}

	if !yes {
		desc := "Irán a " + s.Host + ":" + t.Dir + " y Liferay los instalará solo:\n  " + strings.Join(names, "\n  ")
		if !t.Writable {
			desc += "\nNo puedes escribir ahí: los subiré a una carpeta temporal y los moveré con sudo, con el dueño de los ficheros de Liferay (" + t.Owner + ")."
		}
		ok, err := ui.Confirm(fmt.Sprintf("¿Subo %s a «%s»?", countFiles(len(names)), s.Name), desc, true)
		if err != nil {
			return err
		}
		if !ok {
			ui.Say(ui.Happy, fmt.Sprintf("No subo nada a «%s».", s.Name))
			return nil
		}
	}

	err = ui.RunTask(fmt.Sprintf("Subiendo a «%s»", s.Name), fmt.Sprintf("Subido a «%s»", s.Name),
		func(ctx context.Context, update func(string)) error {
			for _, rel := range changed {
				update(filepath.Base(rel))
				if err := r.Upload(ctx, t, filepath.Join(stage, rel)); err != nil {
					return fmt.Errorf("no he podido subir %s: %w", filepath.Base(rel), remoteErr(s, err))
				}
				if t.Writable {
					uploaded[rel] = true
				}
			}
			return nil
		})
	if err != nil {
		return err
	}
	if !t.Writable {
		fmt.Println(ui.MutedText("  "+s.Host+" $ ") + "sudo: muevo " + countFiles(len(names)) + " a " + t.Dir)
		script, args := t.InstallScript(names)
		if err := remoteSudo(ctx, s, conn, script, args...); err != nil {
			return fmt.Errorf("no he podido moverlos a %s: %w", t.Dir, err)
		}
		for _, rel := range changed {
			uploaded[rel] = true
		}
	}

	var body []string
	for _, n := range names {
		body = append(body, ui.OkText("↑ ")+n)
	}
	body = append(append(body, unchanged...), "",
		"Mira cómo se instalan con "+ui.Code("lray server logs "+s.Name))
	ui.Say(ui.Party, fmt.Sprintf("Listo en «%s»", s.Name), body...)
	return nil
}

// remoteSudo ejecuta script con sudo en la máquina. Con terminal va por
// ssh -t, así sudo te pide la contraseña a ti; sin ella, sudo -n.
func remoteSudo(ctx context.Context, s *config.Server, conn *ssh.Conn, script string, args ...string) error {
	if ui.IsTTY() {
		return remoteErr(s, conn.Interactive(ctx, "sudo "+ssh.Remote(script, args...)))
	}
	_, err := conn.Run(ctx, `exec sudo -n sh -c "$@"`, append([]string{script, "lray"}, args...)...)
	return remoteErr(s, err)
}

func countFiles(n int) string {
	if n == 1 {
		return "1 fichero"
	}
	return fmt.Sprintf("%d ficheros", n)
}

// deployStage es la carpeta local donde Gradle despliega lo que va a un
// server remoto: <caché del usuario>/lray/deploy/<nombre>.
func deployStage(name string) (string, error) {
	dir, err := deployStagePath(name)
	if err != nil {
		return "", err
	}
	return dir, os.MkdirAll(dir, 0o755)
}

func deployStagePath(name string) (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "lray", "deploy", name), nil
}

// forgetDeployStage borra la copia de lo subido a un server (ya no vale).
func forgetDeployStage(name string) {
	if dir, err := deployStagePath(name); err == nil {
		_ = os.RemoveAll(dir)
	}
}

// renameDeployStage lleva la copia de lo subido al nombre nuevo del server.
func renameDeployStage(old, name string) {
	from, err1 := deployStagePath(old)
	to, err2 := deployStagePath(name)
	if err1 == nil && err2 == nil {
		_ = os.RemoveAll(to)
		_ = os.Rename(from, to)
	}
}

// fileStamp es el contenido (sha256) y la fecha de un fichero.
type fileStamp struct {
	sum string
	mod time.Time
}

// snapshotDir resume cada fichero bajo dir (por ruta relativa). Una carpeta
// que no existe está vacía.
func snapshotDir(dir string) (map[string]fileStamp, error) {
	out := map[string]fileStamp{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		h := sha256.New()
		if _, err := io.Copy(h, f); err != nil {
			return err
		}
		fi, err := f.Stat()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		out[rel] = fileStamp{sum: hex.EncodeToString(h.Sum(nil)), mod: fi.ModTime()}
		return nil
	})
	return out, err
}

// changedFiles son los ficheros nuevos o distintos en after, más los
// escritos desde since (si no es cero), ordenados.
func changedFiles(before, after map[string]fileStamp, since time.Time) []string {
	var out []string
	for rel, st := range after {
		if before[rel].sum != st.sum || (!since.IsZero() && !st.mod.Before(since)) {
			out = append(out, rel)
		}
	}
	sort.Strings(out)
	return out
}
