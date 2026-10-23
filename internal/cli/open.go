package cli

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/katarem/lray/internal/config"
	"github.com/katarem/lray/internal/liferay"
	"github.com/katarem/lray/internal/ui"
)

func newOpen() *cobra.Command {
	var f folderFlags
	var print bool
	cmd := &cobra.Command{
		Use:   "open <nombre>",
		Short: "Abre la carpeta del server en el explorador de archivos",
		Long: `Abre en el explorador de archivos la carpeta del server: la que enseña
lray server list (la raíz del workspace o el liferay home) o, con --home,
--logs o --deploy, la del bundle, la de sus logs o la deploy/.

Con -p no abre nada y solo escribe la ruta, por ejemplo para
cd "$(lray server open turismo -p)".

Las carpetas de un server remoto no se pueden abrir en tu explorador: lray
te enseña la ruta y la dirección sftp:// para abrirla con WinSCP, FileZilla
o Nautilus; para entrar en ella, lray server shell. Con -p escribe
máquina:ruta, que vale tal cual para scp y rsync (si el server usa otro
puerto SSH, pásaselo a scp con -P).`,
		Example: `  lray server open turismo
  lray server open turismo --logs
  cd "$(lray server open turismo --home -p)"
  scp modulo.jar "$(lray server open pre --deploy -p)"`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeServers,
		RunE: func(_ *cobra.Command, args []string) error {
			_, s, err := findServer(args[0])
			if err != nil {
				return err
			}
			if s.IsRemote() {
				dir, err := f.remote(s)
				if err != nil {
					return err
				}
				if print {
					fmt.Println(s.Host + ":" + dir)
					return nil
				}
				ui.Say(ui.Thinking, "No puedo abrir en tu explorador una carpeta de otra máquina",
					ui.MutedText("Ruta    ")+s.Host+":"+dir,
					ui.MutedText("SFTP    ")+sftpURL(s, dir),
					ui.MutedText("Entrar  ")+ui.Code("lray server shell "+s.Name+f.suffix()))
				return errShown
			}
			dir, err := f.local(s)
			if err != nil {
				return err
			}
			if print {
				fmt.Println(dir)
				return nil
			}
			if err := openFolder(dir); err != nil {
				return fmt.Errorf("no he podido abrir %s: %w", dir, err)
			}
			ui.Say(ui.Happy, "Abierta en el explorador", ui.ShortPath(dir))
			return nil
		},
	}
	f.register(cmd)
	cmd.Flags().BoolVarP(&print, "print", "p", false, "No abrir nada: solo escribir la ruta")
	return cmd
}

// folderFlags eligen qué carpeta del server usan open y shell.
type folderFlags struct{ home, logs, deploy bool }

func (f *folderFlags) register(cmd *cobra.Command) {
	fl := cmd.Flags()
	fl.BoolVar(&f.home, "home", false, "La carpeta del bundle (liferay home) en vez de la raíz del workspace")
	fl.BoolVar(&f.logs, "logs", false, "La carpeta de sus logs")
	fl.BoolVar(&f.deploy, "deploy", false, "La carpeta deploy/, donde se dejan los JAR para instalarlos")
	cmd.MarkFlagsMutuallyExclusive("home", "logs", "deploy")
}

// suffix repite la opción elegida, para sugerir otro comando con la misma carpeta.
func (f folderFlags) suffix() string {
	switch {
	case f.home:
		return " --home"
	case f.logs:
		return " --logs"
	case f.deploy:
		return " --deploy"
	}
	return ""
}

// local resuelve la carpeta pedida de un server local. La deploy/ la crea si
// aún no existe (Liferay instala lo que haya en ella al arrancar).
func (f folderFlags) local(s *config.Server) (string, error) {
	l, err := liferay.Resolve(s.Path)
	if err != nil {
		return "", fmt.Errorf("la ruta de «%s» (%s) ya no es válida: %w", s.Name, ui.ShortPath(s.Path), err)
	}
	noBundle := fmt.Errorf("«%s» aún no tiene bundle: %s te ofrecerá descargarlo", s.Name, ui.Code("lray server start "+s.Name))
	switch {
	case f.home:
		if !dirExists(l.Home) {
			return "", noBundle
		}
		return l.Home, nil
	case f.logs:
		dirs := []string{filepath.Join(l.Home, "logs")}
		if l.HasBundle() {
			dirs = append(dirs, filepath.Join(l.Tomcat, "logs"))
		}
		for _, d := range dirs {
			if dirExists(d) {
				return d, nil
			}
		}
		return "", fmt.Errorf("«%s» aún no tiene logs: aparecen al arrancarlo", s.Name)
	case f.deploy:
		if !l.HasBundle() {
			return "", noBundle
		}
		d := filepath.Join(l.Home, "deploy")
		if err := os.MkdirAll(d, 0o755); err != nil {
			return "", err
		}
		return d, nil
	}
	return l.Root(), nil
}

// remote calcula la carpeta pedida de un server remoto, sin conectarse: el
// liferay home es la ruta registrada y los logs salen de su patrón.
func (f folderFlags) remote(s *config.Server) (string, error) {
	switch {
	case f.logs:
		dir := path.Dir(logPattern(s))
		if strings.ContainsAny(dir, "*?[") {
			return "", fmt.Errorf("la carpeta del patrón de logs (%s) tiene comodines; cámbialo con lray server set %s --log", logPattern(s), s.Name)
		}
		if !path.IsAbs(dir) {
			dir = path.Join(s.Path, dir)
		}
		return dir, nil
	case f.deploy:
		return path.Join(s.Path, "deploy"), nil
	}
	return s.Path, nil
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// sftpURL es la dirección de la carpeta remota para un cliente SFTP.
func sftpURL(s *config.Server, dir string) string {
	host := s.Host
	if p := sshPortOf(s); p != 0 {
		host += ":" + strconv.Itoa(p)
	}
	if !strings.HasPrefix(dir, "/") {
		dir = "/" + dir
	}
	return "sftp://" + host + dir
}

// openFolder abre dir con el explorador de archivos del sistema.
func openFolder(dir string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", dir)
	case "windows":
		cmd = exec.Command("explorer", dir)
	default:
		if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
			return errors.New("en esta sesión no hay escritorio (ni DISPLAY ni WAYLAND_DISPLAY)")
		}
		cmd = exec.Command("xdg-open", dir)
	}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	var ee *exec.ExitError
	switch {
	case err == nil:
		return nil
	case errors.Is(err, exec.ErrNotFound):
		return fmt.Errorf("no encuentro %s para abrir carpetas", cmd.Args[0])
	case runtime.GOOS == "windows" && errors.As(err, &ee):
		return nil // explorer termina con código 1 aunque haya abierto la carpeta
	}
	if l := strings.TrimSpace(out.String()); l != "" {
		return fmt.Errorf("%s: %s", cmd.Args[0], l)
	}
	return err
}
