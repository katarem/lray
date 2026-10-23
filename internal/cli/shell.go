package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/katarem/lray/internal/config"
	"github.com/katarem/lray/internal/ssh"
	"github.com/katarem/lray/internal/ui"
)

func newShell() *cobra.Command {
	var f folderFlags
	cmd := &cobra.Command{
		Use:   "shell <nombre>",
		Short: "Abre una terminal en la carpeta del server, en tu máquina o en la remota por SSH",
		Long: `Abre tu shell en la carpeta del server para hacer cambios a mano. Al
salir (exit o Ctrl+D) vuelves a donde estabas. La carpeta se elige igual que
en lray server open: la que enseña list o, con --home, --logs o --deploy, la
del bundle, la de sus logs o la deploy/.

En los locales, la shell lleva el JAVA_HOME del server (si tiene uno) y su
bin/ delante en el PATH, y LRAY_SERVER con su nombre. En los remotos entra
por SSH reutilizando la sesión compartida (si la máquina pide contraseña,
solo la primera vez) y abre allí la shell de tu usuario; para tocar archivos
de otro usuario, usa sudo dentro.

Necesita una terminal.`,
		Example: `  lray server shell turismo
  lray server shell pre --logs`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeServers,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !ui.IsTTY() {
				return errors.New("la shell necesita una terminal interactiva")
			}
			_, s, err := findServer(args[0])
			if err != nil {
				return err
			}
			// Ctrl+C es para lo que corra dentro de la shell, no para lray.
			sig := make(chan os.Signal, 1)
			signal.Notify(sig, os.Interrupt)
			defer signal.Stop(sig)

			if s.IsRemote() {
				dir, err := f.remote(s)
				if err != nil {
					return err
				}
				return remoteShell(cmd.Context(), s, dir)
			}
			dir, err := f.local(s)
			if err != nil {
				return err
			}
			return localShell(s, dir)
		},
	}
	f.register(cmd)
	return cmd
}

func localShell(s *config.Server, dir string) error {
	sh := userShell()
	cmd := exec.Command(sh[0], sh[1:]...)
	cmd.Dir = dir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Env = append(os.Environ(), "LRAY_SERVER="+s.Name)
	if s.JavaHome != "" {
		cmd.Env = append(cmd.Env, "JAVA_HOME="+s.JavaHome, "JRE_HOME="+s.JavaHome,
			"PATH="+filepath.Join(s.JavaHome, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	fmt.Println(ui.MutedText("  Shell en ") + ui.ShortPath(dir) + ui.MutedText(" · exit para volver"))
	err := cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return nil // el código de la última orden que escribiste, no un fallo de lray
	}
	return err
}

// userShell es la shell del usuario: $SHELL en Unix; PowerShell (o cmd) en Windows.
func userShell() []string {
	if runtime.GOOS == "windows" {
		for _, sh := range []string{"pwsh", "powershell"} {
			if p, err := exec.LookPath(sh); err == nil {
				return []string{p, "-NoLogo"}
			}
		}
		if c := os.Getenv("COMSPEC"); c != "" {
			return []string{c}
		}
		return []string{"cmd"}
	}
	if sh := os.Getenv("SHELL"); sh != "" {
		return []string{sh}
	}
	return []string{"/bin/sh"}
}

// shellScript entra en la carpeta ($1) y abre la shell de login del usuario.
// Si no puede entrar, sale con noDirCode.
const shellScript = `d=$1
case $d in
"~") d=$HOME ;;
"~/"*) d=$HOME/${d#"~/"} ;;
esac
cd -- "$d" 2>/dev/null || exit 97
exec "${SHELL:-/bin/sh}" -l`

const noDirCode = 97

func remoteShell(ctx context.Context, s *config.Server, dir string) error {
	conn, _ := remoteOf(s)
	if err := connect(ctx, s, conn); err != nil {
		return err
	}
	fmt.Println(ui.MutedText("  Shell en ") + s.Host + ":" + dir + ui.MutedText(" · exit para volver"))
	err := conn.Interactive(ctx, ssh.Remote(shellScript, dir))
	var ee *ssh.ExitError
	if errors.As(err, &ee) {
		if ee.Code == noDirCode {
			return fmt.Errorf("no he podido entrar en %s en %s: ¿existe y tienes permiso?", dir, s.Host)
		}
		return nil // como en local: el código de la última orden, no un fallo
	}
	return remoteErr(s, err)
}
