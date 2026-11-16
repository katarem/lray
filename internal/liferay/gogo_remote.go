package liferay

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// gogoConfigScript: $1 = liferay home. Dice si la propiedad de la Gogo shell
// está en portal-ext.properties o portal-setup-wizard.properties (gana el
// último): "set=<valor>" (puede ir vacío = desactivada) o nada.
const gogoConfigScript = `
home=$1
case "$home" in "~") home=$HOME ;; "~/"*) home="$HOME/${home#"~/"}" ;; esac
[ -d "$home" ] || { echo "error=no existe $home en la máquina"; exit 0; }
for f in "$home/portal-ext.properties" "$home/portal-setup-wizard.properties"; do
	[ -r "$f" ] || continue
	awk -v k="$2" '{ sub(/\r$/, "") }
		/^[ \t]*[#!]/ { next }
		{ i = index($0, "="); if (!i) next
		  key = substr($0, 1, i - 1); gsub(/^[ \t]+|[ \t]+$/, "", key)
		  if (key != k) next
		  v = substr($0, i + 1); gsub(/^[ \t]+|[ \t]+$/, "", v); print "set=" v }' "$f"
done | tail -n 1
exit 0
`

// gogoRelayScript: $1 = máquina, $2 = puerto. Conecta con la Gogo shell desde
// la propia máquina remota (suele escuchar solo en localhost) y hace de
// puente con la entrada y la salida estándar. La primera línea dice cómo ha
// ido: lray-gogo=ok, refused o notool. Al cerrarse la entrada (lray ha
// terminado) se cierra la conexión, y con ella la sesión de Gogo.
const gogoRelayScript = `
if command -v bash >/dev/null 2>&1; then
	exec bash -c '
		{ exec 3<>"/dev/tcp/$1/$2"; } 2>/dev/null || { echo lray-gogo=refused; exit 0; }
		echo lray-gogo=ok
		cat <&3 &
		cat >&3
		kill $! 2>/dev/null
	' lray "$1" "$2"
fi
if command -v python3 >/dev/null 2>&1; then
	exec python3 -c '
import os, socket, sys, threading
try:
    s = socket.create_connection((sys.argv[1], int(sys.argv[2])), 8)
except OSError:
    print("lray-gogo=refused", flush=True)
    sys.exit(0)
s.settimeout(None)
print("lray-gogo=ok", flush=True)
def up():
    while True:
        d = os.read(0, 4096)
        if not d:
            os._exit(0)
        s.sendall(d)
threading.Thread(target=up, daemon=True).start()
while True:
    d = s.recv(4096)
    if not d:
        break
    os.write(1, d)
' "$1" "$2"
fi
echo lray-gogo=notool
`

// GogoConfig lee la propiedad de la Gogo shell del server remoto, como
// Layout.GogoConfig.
func (r *Remote) GogoConfig(ctx context.Context) (value string, set bool, err error) {
	out, err := r.Exec.Run(ctx, gogoConfigScript, r.Path, GogoProperty)
	if err != nil {
		return "", false, err
	}
	for _, l := range strings.Split(string(out), "\n") {
		l = strings.TrimRight(l, "\r")
		switch {
		case strings.HasPrefix(l, "error="):
			return "", false, errors.New(strings.TrimPrefix(l, "error="))
		case strings.HasPrefix(l, "set="):
			value, set = strings.TrimPrefix(l, "set="), true
		}
	}
	return value, set, nil
}

// ErrGogoNoTool: la máquina remota no tiene con qué abrir una conexión TCP.
var ErrGogoNoTool = errors.New("la máquina no tiene bash ni python3 para conectar con la Gogo shell")

// Gogo abre una conexión con la Gogo shell del server remoto, hecha desde la
// propia máquina.
func (r *Remote) Gogo(ctx context.Context, host string, port int) (io.ReadWriteCloser, error) {
	p, err := r.Exec.Pipe(ctx, gogoRelayScript, host, strconv.Itoa(port))
	if err != nil {
		return nil, err
	}
	br := bufio.NewReader(p)
	head, err := br.ReadString('\n')
	if err != nil {
		cerr := p.Close()
		if cerr != nil {
			return nil, cerr
		}
		return nil, fmt.Errorf("no he podido abrir el puente con la Gogo shell: %w", err)
	}
	switch strings.TrimSpace(head) {
	case "lray-gogo=ok":
		return &relay{Reader: br, ReadWriteCloser: p}, nil
	case "lray-gogo=refused":
		_ = p.Close()
		return nil, ErrGogoRefused
	case "lray-gogo=notool":
		_ = p.Close()
		return nil, ErrGogoNoTool
	default:
		_ = p.Close()
		return nil, fmt.Errorf("respuesta inesperada al conectar con la Gogo shell: %q", strings.TrimSpace(head))
	}
}

// relay lee de lo que ya ha leído la cabecera y escribe directamente.
type relay struct {
	io.Reader
	io.ReadWriteCloser
}

func (r *relay) Read(p []byte) (int, error) { return r.Reader.Read(p) }
