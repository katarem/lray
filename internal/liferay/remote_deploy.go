package liferay

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DeployTarget es la carpeta de autodeploy de un Liferay remoto: lo que se
// deja en ella, Liferay lo instala solo (JAR, WAR, client extensions...).
type DeployTarget struct {
	Dir      string // carpeta de autodeploy
	Disabled bool   // auto.deploy.enabled=false: Liferay no mira la carpeta
	Writable bool   // el usuario de ssh puede escribir en ella
	Owner    string // uid:gid de los ficheros de Liferay (osgi/modules), para lo que se mueva con sudo
	Staging  string // si no se puede escribir: carpeta temporal donde se suben antes de moverlos con sudo
}

// DeployTarget localiza la carpeta de autodeploy (deploy/ del liferay home,
// o auto.deploy.deploy.dir de portal-ext.properties) y averigua si se puede
// escribir en ella. Si no, prepara una carpeta temporal en la máquina; hay que
// borrarla después con Cleanup.
func (r *Remote) DeployTarget(ctx context.Context) (*DeployTarget, error) {
	out, err := r.Exec.Run(ctx, deployTargetScript, r.Path)
	if err != nil {
		return nil, err
	}
	t := &DeployTarget{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		k, v, _ := strings.Cut(sc.Text(), "=")
		switch k {
		case "error":
			return nil, errors.New(v)
		case "dir":
			t.Dir = v
		case "disabled":
			t.Disabled = v == "1"
		case "writable":
			t.Writable = v == "1"
		case "owner":
			t.Owner = v
		case "staging":
			t.Staging = v
		}
	}
	if t.Dir == "" || (!t.Writable && t.Staging == "") {
		return nil, fmt.Errorf("no he entendido la respuesta de la máquina: %q", strings.TrimSpace(string(out)))
	}
	return t, nil
}

// Upload sube el fichero local file. Si se puede escribir en la carpeta de
// autodeploy se escribe allí con otro nombre y se renombra al terminar, así
// Liferay nunca ve uno a medias; si no, va a la carpeta temporal y luego hay
// que moverlo con sudo (InstallScript).
func (r *Remote) Upload(ctx context.Context, t *DeployTarget, file string) error {
	name := filepath.Base(file)
	if !validDeployName(name) {
		return fmt.Errorf("nombre de fichero raro para desplegar: %q", name)
	}
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	dir := t.Dir
	if !t.Writable {
		dir = t.Staging
	}
	_, err = r.Exec.Send(ctx, f, uploadScript, dir, name)
	return err
}

// InstallScript es lo que hay que ejecutar con sudo para mover a la carpeta
// de autodeploy lo subido a la temporal, con el mismo dueño que el resto de
// ficheros de Liferay (el de osgi/modules), como si lo hubiera dejado él.
func (t *DeployTarget) InstallScript(names []string) (script string, args []string) {
	return installScript, append([]string{t.Dir, t.Owner, t.Staging}, names...)
}

// Cleanup borra la carpeta temporal, si se creó.
func (r *Remote) Cleanup(ctx context.Context, t *DeployTarget) error {
	if t == nil || t.Staging == "" {
		return nil
	}
	_, err := r.Exec.Run(ctx, `case "$1" in */lray-deploy.*) rm -rf "$1" ;; esac`, t.Staging)
	return err
}

func validDeployName(name string) bool {
	return name != "" && !strings.HasPrefix(name, ".") && !strings.ContainsAny(name, "/\\\n")
}

// deployTargetScript: $1 = liferay home. Lee auto.deploy.* de
// portal-ext.properties y portal-setup-wizard.properties (gana el último).
// El dueño sale de osgi/modules (si no, osgi, el home o la propia carpeta):
// deploy/ puede ser de root aunque Liferay corra con su propio usuario.
const deployTargetScript = `
home=$1
case "$home" in "~") home=$HOME ;; "~/"*) home="$HOME/${home#"~/"}" ;; esac
[ -d "$home" ] || { echo "error=no existe $home en la máquina"; exit 0; }
prop() {
	for f in "$home/portal-ext.properties" "$home/portal-setup-wizard.properties"; do
		[ -r "$f" ] || continue
		awk -v k="$1" '{ sub(/\r$/, "") }
			/^[ \t]*[#!]/ { next }
			{ i = index($0, "="); if (!i) next
			  key = substr($0, 1, i - 1); gsub(/^[ \t]+|[ \t]+$/, "", key)
			  if (key != k) next
			  v = substr($0, i + 1); gsub(/^[ \t]+|[ \t]+$/, "", v); print v }' "$f"
	done | tail -n 1
}
dir=$home/deploy
v=$(prop auto.deploy.deploy.dir)
case "$v" in
'') ;;
'${liferay.home}'*) dir=$home${v#'${liferay.home}'} ;;
*) dir=$v ;;
esac
echo "dir=$dir"
[ "$(prop auto.deploy.enabled)" = false ] && echo disabled=1
[ -d "$dir" ] || mkdir -p "$dir" 2>/dev/null || {
	echo "error=no existe $dir y no puedo crearla; arranca Liferay una vez para que la cree"
	exit 0
}
for o in "$home/osgi/modules" "$home/osgi" "$home" "$dir"; do
	[ -d "$o" ] && { echo "owner=$(ls -ldn "$o" | awk '{ print $3 ":" $4 }')"; break; }
done
if [ -w "$dir" ]; then
	echo writable=1
	exit 0
fi
t=$(mktemp -d "${TMPDIR:-/tmp}/lray-deploy.XXXXXX" 2>/dev/null) || {
	echo "error=no puedo escribir en $dir ni crear una carpeta temporal"
	exit 0
}
echo "staging=$t"
`

// uploadScript: $1 = carpeta, $2 = nombre; el fichero llega por la entrada.
const uploadScript = `
part="$1/.$2.lray-part"
{ cat > "$part" && chmod 644 "$part" && mv -f "$part" "$1/$2"; } || { rm -f "$part"; exit 1; }
`

// installScript (con sudo): $1 = carpeta de autodeploy, $2 = dueño,
// $3 = carpeta temporal, el resto = nombres.
const installScript = `
dir=$1; owner=$2; stage=$3; shift 3
for name; do
	part="$dir/.$name.lray-part"
	{ cp "$stage/$name" "$part" && chown "$owner" "$part" && chmod 644 "$part" && mv -f "$part" "$dir/$name"; } || { rm -f "$part"; exit 1; }
done
`
