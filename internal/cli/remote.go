package cli

import (
	"context"
	"errors"
	"fmt"
	neturl "net/url"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/katarem/lray/internal/config"
	"github.com/katarem/lray/internal/liferay"
	"github.com/katarem/lray/internal/logs"
	"github.com/katarem/lray/internal/ssh"
	"github.com/katarem/lray/internal/ui"
)

// remoteOf prepara la conexión y el acceso a Liferay de un server remoto.
func remoteOf(s *config.Server) (*ssh.Conn, *liferay.Remote) {
	rm := s.Remote
	if rm == nil {
		rm = &config.Remote{}
	}
	conn := ssh.New(s.Host, rm.SSHPort)
	return conn, &liferay.Remote{
		Exec:   conn,
		Path:   s.Path,
		Log:    rm.Log,
		Port:   s.Port,
		Status: rm.Control.StatusCommand(),
		Since:  rm.Control.SinceCommand(),
	}
}

// connect abre la sesión SSH del server o reutiliza la que ya hay. Si la
// máquina pide contraseña, ssh la pregunta en la terminal una sola vez.
func connect(ctx context.Context, s *config.Server, conn *ssh.Conn) error {
	if conn.Connected() {
		return nil
	}
	fmt.Println(ui.MutedText("  Conectando con ") + ui.Code(s.Host) + ui.MutedText("…"))
	err := conn.Connect(ctx, ui.IsTTY(), func() {
		fmt.Println(ui.MutedText(fmt.Sprintf("  La sesión se queda abierta %s sin usarse; no te volveré a pedir la contraseña hasta entonces.", ssh.Persist())))
	})
	return remoteErr(s, err)
}

// remoteErr explica los fallos de conexión en cristiano.
func remoteErr(s *config.Server, err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ssh.ErrUnreachable):
		return fmt.Errorf("no llego a «%s» (%s). ¿Tienes la VPN conectada?", s.Name, s.Host)
	case errors.Is(err, ssh.ErrNeedsLogin):
		return fmt.Errorf("«%s» pide contraseña y aquí no puedo preguntártela; abre la sesión antes con lray server connect %s", s.Name, s.Name)
	case errors.Is(err, ssh.ErrAuth) && runtime.GOOS == "windows":
		return fmt.Errorf("%s no ha aceptado el acceso: en Windows lray no puede reutilizar la sesión, así que necesita una clave SSH (ssh-keygen + copiar la .pub al authorized_keys del servidor): %w", s.Host, err)
	case errors.Is(err, ssh.ErrAuth):
		return fmt.Errorf("%s no ha aceptado tu usuario o contraseña: %w", s.Host, err)
	}
	return err
}

// inspectRemote conecta y averigua el estado del server, y lo apunta como
// último estado conocido (también si falla).
func inspectRemote(ctx context.Context, s *config.Server, withVersion bool) (*ssh.Conn, *liferay.Remote, *liferay.RemoteInfo, error) {
	conn, r := remoteOf(s)
	if err := connect(ctx, s, conn); err != nil {
		config.RecordState(s.Name, "", err)
		return nil, nil, nil, err
	}
	info, err := r.Inspect(ctx, withVersion)
	if err != nil {
		err = remoteErr(s, err)
		config.RecordState(s.Name, "", err)
		return nil, nil, nil, err
	}
	config.RecordState(s.Name, stateName(info.State), nil)
	return conn, r, info, nil
}

// remoteFollow adapta el log remoto a una fuente del visor.
func remoteFollow(s *config.Server, r *liferay.Remote, lines int) logs.Source {
	return func(ctx context.Context, onLine func(string), flush func()) error {
		return remoteErr(s, r.Follow(ctx, lines, onLine, flush))
	}
}

// runRemoteControl lanza el comando de arranque o parada. Con terminal va por
// ssh -t, así sudo te pide la contraseña a ti; sin ella, sudo -n (falla en
// vez de quedarse esperando).
func runRemoteControl(ctx context.Context, s *config.Server, conn *ssh.Conn, command string) error {
	fmt.Println(ui.MutedText("  "+s.Host+" $ ") + command)
	if ui.IsTTY() {
		return remoteErr(s, conn.Interactive(ctx, command))
	}
	if rest, ok := strings.CutPrefix(command, "sudo "); ok {
		command = "sudo -n " + rest
	}
	_, err := conn.Run(ctx, command)
	return remoteErr(s, err)
}

// waitRemote sondea el server hasta que llega a want (o se agota el tiempo),
// enseñando mientras tanto lo que va saliendo en su log.
func waitRemote(ctx context.Context, s *config.Server, r *liferay.Remote, want liferay.State, timeout time.Duration, update func(string)) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if want == liferay.Running {
		go func() {
			_ = r.Follow(ctx, 0, func(line string) {
				if msg := logs.Message(line); msg != "" {
					update(msg)
				}
			}, func() {})
		}()
	}
	tick := time.NewTicker(3 * time.Second)
	defer tick.Stop()
	for {
		info, err := r.Inspect(ctx, false)
		if err == nil {
			config.RecordState(s.Name, stateName(info.State), nil)
			if info.State == want {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return fmt.Errorf("pasados %s sigue sin estar %s; revisa sus logs con lray server logs %s", timeout, stateWord(want), s.Name)
			}
			return ctx.Err()
		case <-tick.C:
		}
	}
}

func stateWord(st liferay.State) string {
	if st == liferay.Running {
		return "encendido"
	}
	return "apagado"
}

// parseRemoteSpec reconoce usuario@máquina:/ruta y ssh://usuario@máquina:puerto/ruta.
// Una sola letra antes de ":" es una unidad de Windows, no una máquina.
func parseRemoteSpec(arg string) (dest string, port int, path string, ok bool, err error) {
	if strings.HasPrefix(arg, "ssh://") {
		u, err := neturl.Parse(arg)
		if err != nil || u.Hostname() == "" {
			return "", 0, "", true, fmt.Errorf("no entiendo %q; usa ssh://usuario@máquina/ruta", arg)
		}
		dest = u.Hostname()
		if u.User != nil {
			dest = u.User.Username() + "@" + dest
		}
		if p := u.Port(); p != "" {
			port, _ = strconv.Atoi(p)
		}
		path = u.Path
		if path == "" {
			path = "~"
		}
		return dest, port, path, true, ssh.ValidDest(dest)
	}
	host, path, found := strings.Cut(arg, ":")
	if !found || len(host) < 2 || strings.ContainsAny(host, `/\`) {
		return "", 0, "", false, nil
	}
	if _, err := os.Stat(arg); err == nil {
		return "", 0, "", false, nil // es una ruta local con ":" en el nombre
	}
	if path == "" {
		path = "~"
	}
	return host, 0, path, true, ssh.ValidDest(host)
}

func stateName(st liferay.State) string {
	switch st {
	case liferay.Running:
		return "running"
	case liferay.Starting:
		return "starting"
	}
	return "stopped"
}

func parseState(s string) liferay.State {
	switch s {
	case "running":
		return liferay.Running
	case "starting":
		return liferay.Starting
	}
	return liferay.Stopped
}

// snapshotLabel pinta el último estado conocido de un remoto con su antigüedad.
func snapshotLabel(snap config.Snapshot, ok bool) string {
	if !ok || (snap.CheckedAt.IsZero() && snap.FailedAt.IsZero()) {
		return ui.MutedText("? sin comprobar")
	}
	if !snap.Reachable() {
		last := "nunca comprobado"
		if !snap.CheckedAt.IsZero() {
			last = plainState(parseState(snap.State)) + " " + ago(snap.CheckedAt)
		}
		return ui.BadText("◌ sin conexión") + ui.MutedText(" · "+last)
	}
	return stateLabel(parseState(snap.State)) + ui.MutedText(" · "+ago(snap.CheckedAt))
}

func plainState(st liferay.State) string {
	switch st {
	case liferay.Running:
		return "encendido"
	case liferay.Starting:
		return "arrancando"
	}
	return "apagado"
}

// ago dice cuánto hace de t en pocas palabras.
func ago(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "ahora mismo"
	case d < time.Hour:
		return fmt.Sprintf("hace %d min", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("hace %d h", int(d.Hours()))
	}
	return fmt.Sprintf("hace %d días", int(d.Hours()/24))
}
