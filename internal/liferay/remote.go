package liferay

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/katarem/lray/internal/logs"
)

// Exec ejecuta scripts de sh en otra máquina (lo cumple ssh.Conn; en los
// tests, un falso).
type Exec interface {
	Run(ctx context.Context, script string, args ...string) ([]byte, error)
	Stream(ctx context.Context, script string, args ...string) (io.ReadCloser, error)
}

// DefaultRemoteLog es el patrón de logs por defecto, relativo al liferay home.
const DefaultRemoteLog = "logs/liferay.*.log"

// ErrNotLiferayRemote: la ruta remota existe pero no tiene pinta de Liferay.
var ErrNotLiferayRemote = errors.New("no parece un liferay home (no hay servidor de aplicaciones ni carpeta osgi)")

// Remote es un Liferay en otra máquina. Todo lo que sabe hacer va en scripts
// de sh POSIX: no hace falta instalar nada allí.
type Remote struct {
	Exec   Exec
	Path   string // liferay home (o la carpeta del servidor de aplicaciones al añadirlo)
	Log    string // patrón de logs; "" = DefaultRemoteLog
	Port   int    // puerto HTTP; 0 = no sondear
	Status string // comando que sale con 0 si está encendido; "" = buscar el proceso java
	Since  string // comando opcional que dice desde cuándo está activo
}

// RemoteInfo es lo que se averigua de un Liferay remoto.
type RemoteInfo struct {
	Home      string // liferay home
	AppServer string // tomcat, jboss o "" si no se ha encontrado
	AppDir    string // carpeta del servidor de aplicaciones
	Port      int    // puerto HTTP según su configuración (0 si no se sabe)
	LogFile   string // log más reciente que encaja con el patrón
	Version   string
	Active    string // "1", "0" o "" si no se ha podido saber
	HTTP      int    // código HTTP de la sonda; 0 = sin respuesta, -1 = sin sonda
	Since     string // desde cuándo está activo (systemd)
	State     State
}

// LogPattern es el patrón absoluto de logs.
func (r *Remote) LogPattern() string {
	log := r.Log
	if log == "" {
		log = DefaultRemoteLog
	}
	if path.IsAbs(log) {
		return log
	}
	return path.Join(r.Path, log)
}

// Inspect averigua en un solo viaje dónde está cada cosa y en qué estado está.
// withVersion busca además la versión en los logs (más lento con logs grandes).
func (r *Remote) Inspect(ctx context.Context, withVersion bool) (*RemoteInfo, error) {
	want := ""
	if withVersion {
		want = "1"
	}
	out, err := r.Exec.Run(ctx, inspectScript, r.Path, r.Log, strconv.Itoa(r.Port), r.Status, r.Since, want)
	if err != nil {
		return nil, err
	}
	return parseInspect(out, r.Port)
}

func parseInspect(out []byte, port int) (*RemoteInfo, error) {
	info := &RemoteInfo{HTTP: -1}
	var conf bytes.Buffer
	inConf := false
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 64*1024), 16<<20)
	for sc.Scan() {
		line := sc.Text()
		if inConf {
			if line == "@@end" {
				inConf = false
				continue
			}
			conf.WriteString(line)
			conf.WriteByte('\n')
			continue
		}
		if line == "@@conf" {
			inConf = true
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch k {
		case "error":
			switch v {
			case "nodir":
				return nil, errors.New("esa ruta no existe en la máquina remota")
			case "noaccess":
				return nil, errors.New("no tengo permiso para entrar en esa ruta")
			default:
				return nil, ErrNotLiferayRemote
			}
		case "home":
			info.Home = v
		case "kind":
			info.AppServer = v
		case "app":
			info.AppDir = v
		case "log":
			info.LogFile = v
		case "starting":
			if m := startingRe.FindStringSubmatch(v); m != nil {
				info.Version = prettyVersion(m[1])
			}
		case "active":
			info.Active = v
		case "http":
			info.HTTP, _ = strconv.Atoi(v)
		case "since":
			_, after, found := strings.Cut(v, "=") // ActiveEnterTimestamp=...
			if !found {
				after = v
			}
			info.Since = strings.TrimSpace(after)
		}
	}
	if info.Home == "" {
		return nil, errors.New("respuesta inesperada de la máquina remota")
	}
	switch info.AppServer {
	case "tomcat":
		info.Port = tomcatPort(conf.Bytes())
	case "jboss":
		info.Port = jbossPort(conf.Bytes())
	}
	info.State = remoteState(info.Active, info.HTTP, port > 0)
	return info, nil
}

// remoteState combina "¿está el proceso/servicio activo?" con la sonda HTTP.
// Al arrancar, JBoss responde 404 antes de que Liferay esté desplegado, así
// que solo cuentan como listo las respuestas propias de un portal.
func remoteState(active string, code int, probed bool) State {
	ready := code >= 200 && code < 400 || code == 401 || code == 403
	switch {
	case active == "0":
		return Stopped
	case ready:
		return Running
	case active == "1" && (!probed || code < 0):
		return Running // no hay forma de sondear: nos fiamos del servicio
	case active == "1":
		return Starting
	}
	return Stopped
}

var (
	socketGroupRe = regexp.MustCompile(`<socket-binding-group\b[^>]*\bport-offset="([^"]*)"`)
	httpBindingRe = regexp.MustCompile(`<socket-binding\b[^>]*\bname="http"[^>]*>`)
	bindingPortRe = regexp.MustCompile(`\bport="([^"]*)"`)
)

// jbossPort lee el puerto HTTP de un standalone.xml: el socket-binding "http"
// más el port-offset del grupo. Entiende ${propiedad:valor} (se queda con el
// valor por defecto).
func jbossPort(data []byte) int {
	tag := httpBindingRe.Find(data)
	if tag == nil {
		return 0
	}
	m := bindingPortRe.FindSubmatch(tag)
	if m == nil {
		return 0
	}
	port := expr(string(m[1]))
	if port <= 0 {
		return 0
	}
	if g := socketGroupRe.FindSubmatch(data); g != nil {
		port += expr(string(g[1]))
	}
	return port
}

// expr evalúa "8080" o "${jboss.http.port:8080}".
func expr(v string) int {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "${") && strings.HasSuffix(v, "}") {
		_, def, ok := strings.Cut(v[2:len(v)-1], ":")
		if !ok {
			return 0
		}
		v = def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0
	}
	return n
}

// Follow sigue el log más reciente que encaja con el patrón, empezando por
// sus últimas lines líneas, y salta al siguiente cuando aparece uno nuevo
// (los liferay.AAAA-MM-DD.log cambian cada día).
func (r *Remote) Follow(ctx context.Context, lines int, onLine func(string), flush func()) error {
	st, err := r.Exec.Stream(ctx, followScript, r.LogPattern(), strconv.Itoa(lines))
	if err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() { _ = st.Close() })
	defer stop()
	err = logs.FollowReader(ctx, st, onLine, flush)
	cerr := st.Close()
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		return err
	}
	if cerr != nil {
		return fmt.Errorf("se ha cortado el log remoto: %w", cerr)
	}
	return nil
}

// FindServices busca servicios de systemd con pinta de servidor de
// aplicaciones, para sugerirlos al añadir un server.
func FindServices(ctx context.Context, e Exec) []string {
	out, err := e.Run(ctx, servicesScript)
	if err != nil {
		return nil
	}
	return strings.Fields(string(out))
}

// inspectScript: $1 ruta, $2 patrón de logs, $3 puerto, $4 comando de estado,
// $5 comando "desde", $6 "1" para buscar la versión. Sale siempre con 0 y
// cuenta los problemas con error=..., así no se confunden con fallos de ssh.
const inspectScript = `
p=$1; log=$2; port=$3; status=$4; since=$5; want=$6
case "$p" in "~") p=$HOME ;; "~/"*) p="$HOME/${p#"~/"}" ;; esac
[ -d "$p" ] || { echo error=nodir; exit 0; }
cd "$p" 2>/dev/null || { echo error=noaccess; exit 0; }
home=$(pwd)
appkind() {
	if [ -f "$1/bin/catalina.sh" ]; then echo tomcat
	elif [ -f "$1/bin/standalone.sh" ]; then echo jboss
	fi
}
app=
kind=$(appkind "$home")
if [ -n "$kind" ]; then
	app=$home; home=$(dirname "$home")
else
	for d in "$home"/*/; do
		[ -d "$d" ] || continue
		d=${d%/}
		kind=$(appkind "$d")
		if [ -n "$kind" ]; then app=$d; break; fi
	done
fi
[ -n "$app" ] || [ -d "$home/osgi" ] || { echo error=notliferay; exit 0; }
echo "home=$home"
echo "kind=$kind"
echo "app=$app"
case "$kind" in
	tomcat) conf="$app/conf/server.xml" ;;
	jboss) conf="$app/standalone/configuration/standalone.xml" ;;
	*) conf= ;;
esac
if [ -n "$conf" ] && [ -r "$conf" ]; then echo @@conf; cat "$conf"; echo; echo @@end; fi
[ -n "$log" ] || log=logs/liferay.*.log
case "$log" in /*) ;; *) log="$home/$log" ;; esac
ldir=$(dirname "$log"); lglob=$(basename "$log")
latest=$(cd "$ldir" 2>/dev/null && ls -t -- $lglob 2>/dev/null | head -n 1)
[ -n "$latest" ] && echo "log=$ldir/$latest"
if [ "$want" = 1 ] && [ -n "$latest" ]; then
	v=$(cd "$ldir" && ls -t -- $lglob 2>/dev/null | head -n 3 | while read -r f; do
		grep -h "Starting Liferay" "$f" 2>/dev/null | tail -n 1
	done | head -n 1)
	echo "starting=$v"
fi
if [ -n "$status" ]; then
	if sh -c "$status" >/dev/null 2>&1; then echo active=1; else echo active=0; fi
elif [ -n "$app" ]; then
	if ps -eo args= 2>/dev/null | awk -v a="$app" '$1 ~ /java$/ && index($0, a) { f = 1 } END { exit !f }'; then
		echo active=1
	else
		echo active=0
	fi
fi
[ -n "$since" ] && echo "since=$(sh -c "$since" 2>/dev/null | head -n 1)"
if [ "$port" -gt 0 ] 2>/dev/null && command -v curl >/dev/null 2>&1; then
	code=000
	for h in 127.0.0.1 "$(hostname)"; do
		code=$(curl -s -o /dev/null -I -m 4 -w '%{http_code}' "http://$h:$port/" 2>/dev/null)
		[ "$code" != 000 ] && break
	done
	echo "http=$code"
fi
exit 0
`

// followScript: $1 patrón absoluto de logs, $2 líneas iniciales. Cuando lray
// cierra la conexión, la entrada estándar llega a su fin: el vigilante avisa
// al script, que para su tail y termina (si no, tail se quedaría corriendo
// en la máquina remota).
const followScript = `
log=$1; n=$2
ldir=$(dirname "$log"); lglob=$(basename "$log")
cd "$ldir" 2>/dev/null || { echo "no existe la carpeta $ldir" >&2; exit 3; }
latest() { ls -t -- $lglob 2>/dev/null | head -n 1; }
cur=$(latest)
[ -n "$cur" ] || { echo "no hay ningún log que encaje con $log" >&2; exit 3; }
pid=
main=$$
trap 'kill "$pid" 2>/dev/null; exit 0' TERM HUP INT
exec 3<&0
{ cat <&3 >/dev/null; kill "$main" 2>/dev/null; } &
tail -n "$n" -F -- "$cur" 2>/dev/null &
pid=$!
while sleep 3 </dev/null >/dev/null 2>&1; do
	new=$(latest)
	if [ -n "$new" ] && [ "$new" != "$cur" ]; then
		kill "$pid" 2>/dev/null; wait "$pid" 2>/dev/null
		cur=$new
		tail -n +1 -F -- "$cur" 2>/dev/null &
		pid=$!
	fi
done
`

const servicesScript = `
command -v systemctl >/dev/null 2>&1 || exit 0
systemctl list-unit-files --type=service --no-legend --no-pager 2>/dev/null |
	awk '{ print $1 }' | grep -iE 'jboss|wildfly|tomcat|liferay' | sed 's/\.service$//'
`
