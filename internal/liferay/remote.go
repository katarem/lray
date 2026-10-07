package liferay

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"regexp"
	"slices"
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
	Config    string // fichero de configuración leído (server.xml, standalone*.xml)
	Port      int    // puerto HTTP según su configuración y la línea de comandos (0 si no se sabe)
	Bind      string // IP de escucha si no es todas (-b / jboss.bind.address)
	PID       int    // proceso java del servidor (0 si no se ha encontrado)
	LogFile   string // log más reciente que encaja con el patrón
	Version   string
	Active    string // "1", "0" o "" si no se ha podido saber
	HTTP      int    // código HTTP de la sonda; 0 = sin respuesta, -1 = sin sonda
	URL       string // dirección sondeada que dio ese código
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

// Inspect averigua dónde está cada cosa y en qué estado está. Son dos
// viajes: el primero localiza el servidor, su configuración y su proceso; el
// segundo sondea HTTP en el puerto que de verdad usa (el de la configuración
// con lo que cambie su línea de comandos, y el registrado si es otro).
// withVersion busca además la versión en los logs si el portal no la dice
// en sus cabeceras (más lento con muchos logs).
func (r *Remote) Inspect(ctx context.Context, withVersion bool) (*RemoteInfo, error) {
	want := ""
	if withVersion {
		want = "1"
	}
	out, err := r.Exec.Run(ctx, inspectScript, r.Path, r.Log, r.Status, r.Since, want)
	if err != nil {
		return nil, err
	}
	info, err := parseInspect(out)
	if err != nil {
		return nil, err
	}
	if info.Active != "0" {
		var args []string
		for _, p := range []int{info.Port, r.Port} {
			if p > 0 && !slices.Contains(args, strconv.Itoa(p)) {
				args = append(args, strconv.Itoa(p))
			}
		}
		if len(args) > 0 {
			bind := info.Bind
			if strings.Contains(bind, ":") {
				bind = "[" + bind + "]" // IPv6 en una URL
			}
			out, err := r.Exec.Run(ctx, probeScript, append([]string{bind}, args...)...)
			if err != nil {
				return nil, err
			}
			parseProbe(out, info)
		}
	}
	info.State = remoteState(info.Active, info.HTTP)
	return info, nil
}

func parseInspect(out []byte) (*RemoteInfo, error) {
	info := &RemoteInfo{HTTP: -1}
	var conf bytes.Buffer
	var jvm string
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
		case "conf":
			info.Config = v
		case "jvm":
			jvm = v
		case "pid":
			info.PID, _ = strconv.Atoi(v)
		case "log":
			info.LogFile = v
		case "starting":
			if m := startingRe.FindStringSubmatch(v); m != nil {
				info.Version = prettyVersion(m[1])
			}
		case "active":
			info.Active = v
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
		props := jvmProps(jvm)
		info.Port = jbossPort(conf.Bytes(), props)
		info.Bind = jbossBind(jvm, props)
	}
	return info, nil
}

// parseProbe se queda con la mejor respuesta de la sonda: la primera que
// dice que el portal está listo o, si ninguna, la primera que respondió algo.
func parseProbe(out []byte, info *RemoteInfo) {
	info.HTTP, info.URL = -1, ""
	for _, line := range strings.Split(string(out), "\n") {
		k, v, _ := strings.Cut(strings.TrimSpace(line), "=")
		switch k {
		case "probe":
			u, c, _ := strings.Cut(v, " ")
			code, _ := strconv.Atoi(c)
			better := info.URL == "" ||
				!httpReady(info.HTTP) && httpReady(code) ||
				info.HTTP <= 0 && code > 0
			if better {
				info.HTTP, info.URL = code, u
			}
		case "portal":
			if m := portalHeaderRe.FindStringSubmatch(v); m != nil && strings.ContainsAny(m[1], "0123456789") {
				info.Version = prettyVersion(m[1]) // la del portal en marcha manda sobre la de los logs
			}
		}
	}
}

// portalHeaderRe saca el nombre de la cabecera Liferay-Portal
// ("Liferay Digital Experience Platform 7.4.13 Update 92 (Cavanaugh / ...)").
var portalHeaderRe = regexp.MustCompile(`^(Liferay [^(]+?)\s*(?:\(|$)`)

// httpReady dice si un código HTTP es de un portal ya desplegado. Al arrancar,
// JBoss responde 404 antes de que Liferay esté desplegado y 503 mientras se
// inicia; 502/504 vendrían de un proxy por el medio.
func httpReady(code int) bool {
	switch code {
	case 404, 502, 503, 504:
		return false
	}
	return code > 0
}

// AnsweredPort es el puerto en el que el portal respondió listo (0 si no).
func (i *RemoteInfo) AnsweredPort() int {
	if !httpReady(i.HTTP) {
		return 0
	}
	u, err := url.Parse(i.URL)
	if err != nil {
		return 0
	}
	p, _ := strconv.Atoi(u.Port())
	return p
}

// remoteState combina "¿está el proceso/servicio activo?" con la sonda HTTP.
func remoteState(active string, code int) State {
	switch {
	case active == "0":
		return Stopped
	case httpReady(code):
		return Running
	case active == "1" && code < 0:
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
	jvmPropRe     = regexp.MustCompile(`(?:^|\s)-D([\w.\-]+)=(\S*)`)
	jvmBindRe     = regexp.MustCompile(`(?:^|\s)-b[= ](\S+)`)
)

// jvmProps saca las -Dpropiedad=valor de una línea de comandos de java.
func jvmProps(jvm string) map[string]string {
	props := map[string]string{}
	for _, m := range jvmPropRe.FindAllStringSubmatch(jvm, -1) {
		props[m[1]] = m[2]
	}
	return props
}

// jbossBind es la IP en la que escucha JBoss si no es "todas" (-b o
// jboss.bind.address); "" si no se sabe o escucha en todas.
func jbossBind(jvm string, props map[string]string) string {
	b := props["jboss.bind.address"]
	if m := jvmBindRe.FindStringSubmatch(jvm); m != nil {
		b = m[1]
	}
	switch b {
	case "0.0.0.0", "::", "127.0.0.1", "localhost":
		return ""
	}
	return b
}

// jbossPort lee el puerto HTTP de un standalone.xml: el socket-binding "http"
// más el port-offset del grupo. Entiende ${propiedad:valor}: vale la
// propiedad si se pasó con -D al arrancar y, si no, el valor por defecto.
func jbossPort(data []byte, props map[string]string) int {
	tag := httpBindingRe.Find(data)
	if tag == nil {
		return 0
	}
	m := bindingPortRe.FindSubmatch(tag)
	if m == nil {
		return 0
	}
	port := expr(string(m[1]), props)
	if port <= 0 {
		return 0
	}
	if g := socketGroupRe.FindSubmatch(data); g != nil {
		port += expr(string(g[1]), props)
	}
	return port
}

// expr evalúa "8080" o "${jboss.http.port:8080}".
func expr(v string, props map[string]string) int {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "${") && strings.HasSuffix(v, "}") {
		name, def, _ := strings.Cut(v[2:len(v)-1], ":")
		if p, ok := props[name]; ok {
			def = p
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

// inspectScript: $1 ruta, $2 patrón de logs, $3 comando de estado, $4
// comando "desde", $5 "1" para buscar la versión en los logs. Sale siempre
// con 0 y cuenta los problemas con error=..., así no se confunden con fallos
// de ssh.
//
// El servidor de aplicaciones se busca primero dentro del liferay home y, si
// no está ahí (JBoss en /opt/jboss-eap-7.4 y Liferay en /opt/liferay, por
// ejemplo), por su proceso java: el de JBoss o Tomcat que mencione el home o,
// si solo hay uno, ese. De su línea de comandos salen sus carpetas reales.
const inspectScript = `
p=$1; log=$2; status=$3; since=$4; want=$5
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
real=
[ -n "$app" ] && real=$(cd "$app" && pwd -P)
found=$({ ps -e -ww -o pid=,args= 2>/dev/null || ps -eo pid=,args= 2>/dev/null; } |
	awk -v a="$app" -v b="$real" -v h="$home" '
	{ pid = $1; line = $0; sub(/^[ \t]*[0-9]+[ \t]+/, "", line); split(line, w, /[ \t]+/) }
	w[1] !~ /java$/ { next }
	a != "" { if (index(line, a) || index(line, b)) { print pid " " line; done = 1; exit } next }
	line ~ /jboss-modules\.jar|org\.jboss\.as\.standalone|org\.apache\.catalina\.startup\.Bootstrap/ {
		n++; any = pid " " line
		if (index(line, h)) mention = pid " " line
	}
	END { if (done || a != "") exit; if (mention != "") print mention; else if (n == 1) print any }')
jpid=; jvm=
if [ -n "$found" ]; then jpid=${found%% *}; jvm=${found#* }; fi
prop() { printf '%s\n' "$jvm" | sed -n "s/.*-D$1=\([^ ]*\).*/\1/p" | head -n 1; }
if [ -z "$app" ] && [ -n "$jvm" ]; then
	d=$(prop 'jboss\.home\.dir')
	if [ -n "$d" ]; then
		app=$d; kind=jboss
	else
		d=$(prop 'catalina\.base'); [ -n "$d" ] || d=$(prop 'catalina\.home')
		if [ -n "$d" ]; then app=$d; kind=tomcat; fi
	fi
fi
[ -n "$app" ] || [ -d "$home/osgi" ] || { echo error=notliferay; exit 0; }
echo "home=$home"
echo "kind=$kind"
echo "app=$app"
[ -n "$jvm" ] && { echo "pid=$jpid"; echo "jvm=$jvm"; }
jlog=; jpat=
case "$kind" in
	tomcat) conf="$app/conf/server.xml"; jlog="$app/logs"; jpat='catalina.out*' ;;
	jboss)
		base=$(prop 'jboss\.server\.base\.dir'); [ -n "$base" ] || base="$app/standalone"
		cdir=$(prop 'jboss\.server\.config\.dir'); [ -n "$cdir" ] || cdir="$base/configuration"
		jlog=$(prop 'jboss\.server\.log\.dir'); [ -n "$jlog" ] || jlog="$base/log"
		jpat='server.log*'
		cfg=standalone.xml
		case "$jvm" in
			*--server-config=*) cfg=${jvm##*--server-config=} ;;
			*" -c="*) cfg=${jvm##*" -c="} ;;
			*" -c "*) cfg=${jvm##*" -c "} ;;
		esac
		cfg=${cfg%% *}
		case "$cfg" in /*) conf=$cfg ;; *) conf="$cdir/$cfg" ;; esac
		;;
	*) conf= ;;
esac
if [ -n "$conf" ] && [ -r "$conf" ]; then echo "conf=$conf"; echo @@conf; cat "$conf"; echo; echo @@end; fi
[ -n "$log" ] || log=logs/liferay.*.log
case "$log" in /*) ;; *) log="$home/$log" ;; esac
ldir=$(dirname "$log"); lglob=$(basename "$log")
latest=$(cd "$ldir" 2>/dev/null && ls -t -- $lglob 2>/dev/null | head -n 1)
[ -n "$latest" ] && echo "log=$ldir/$latest"
if [ "$want" = 1 ]; then
	# La línea "Starting Liferay" está en el log del día en que arrancó, que
	# puede ser de hace semanas y los logs pesan mucho. Si se sabe cuándo
	# arrancó el proceso, se mira solo lo escrito desde entonces, del más viejo
	# al más nuevo, y grep para en la primera coincidencia. Si no, del más
	# nuevo al más viejo (máximo 60). Cada grep tiene un límite de tiempo.
	tm=; command -v timeout >/dev/null 2>&1 && tm="timeout 10"
	ref=
	if [ -n "$jpid" ]; then
		et=$(ps -o etimes= -p "$jpid" 2>/dev/null | tr -d ' ')
		case "$et" in
			'' | *[!0-9]*) ;;
			*) ref=$(mktemp 2>/dev/null) && touch -d "@$(($(date +%s) - et - 120))" "$ref" 2>/dev/null || { rm -f "$ref"; ref=; } ;;
		esac
	fi
	lst() {
		if [ -n "$ref" ]; then
			(cd "$1" 2>/dev/null && ls -tr -- $2 2>/dev/null) | while IFS= read -r f; do
				[ "$1/$f" -nt "$ref" ] && echo "$1/$f"
			done
		else
			(cd "$1" 2>/dev/null && ls -t -- $2 2>/dev/null | head -n 60) | while IFS= read -r f; do echo "$1/$f"; done
		fi
	}
	v=
	for f in $(lst "$ldir" "$lglob"; [ -n "$jlog" ] && lst "$jlog" "$jpat"); do
		if [ -n "$ref" ]; then
			v=$($tm grep -h -m 1 "Starting Liferay" "$f" 2>/dev/null)
		else
			v=$($tm grep -h "Starting Liferay" "$f" 2>/dev/null | tail -n 1)
		fi
		[ -n "$v" ] && break
	done
	[ -n "$ref" ] && rm -f "$ref"
	echo "starting=$v"
fi
if [ -n "$status" ]; then
	if sh -c "$status" >/dev/null 2>&1; then echo active=1; else echo active=0; fi
elif [ -n "$app" ]; then
	if [ -n "$jvm" ]; then echo active=1; else echo active=0; fi
fi
[ -n "$since" ] && echo "since=$(sh -c "$since" 2>/dev/null | head -n 1)"
exit 0
`

// probeScript: $1 IP de escucha ("" = solo 127.0.0.1 y el nombre de la
// máquina), luego los puertos. Pregunta sin proxy (en los servidores suele
// haber http_proxy y la petición acabaría en el proxy) y para en la primera
// respuesta de un portal listo. Saca probe=<url> <código> por intento y la
// cabecera Liferay-Portal, que trae la versión exacta.
const probeScript = `
bind=$1; shift
command -v curl >/dev/null 2>&1 || exit 0
hosts="127.0.0.1 $(hostname 2>/dev/null)"
[ -n "$bind" ] && hosts="$bind $hosts"
for port in "$@"; do
	for h in $hosts; do
		u="http://$h:$port/"
		out=$(curl -s -I --noproxy '*' --connect-timeout 2 -m 6 "$u" 2>/dev/null | tr -d '\r')
		code=$(printf '%s\n' "$out" | awk 'NR == 1 && /^HTTP/ { print $2 }')
		echo "probe=$u ${code:-000}"
		printf '%s\n' "$out" | awk 'tolower($0) ~ /^liferay-portal:/ { sub(/^[^:]*:[ \t]*/, ""); print "portal=" $0; exit }'
		case "${code:-000}" in 000|404|502|503|504) ;; *) exit 0 ;; esac
	done
done
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
