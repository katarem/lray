#!/bin/sh
# Lee VERSION y dice si hay que publicarla. Escribe en $GITHUB_OUTPUT (o en
# la salida, fuera de Actions) tag=vX.Y.Z y new=true|false:
#   - new=false si el tag ya existe (no se ha subido la versión)
#   - new=true si no existe y es mayor que la última versión publicada
# Falla si VERSION no es X.Y.Z o si es una versión nueva pero menor que la
# última (se publicaría como «latest» una versión vieja).
# Necesita los tags del repo (checkout con fetch-depth: 0).
set -eu

v=$(tr -d ' \t\r\n' < VERSION)
if ! printf '%s\n' "$v" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$'; then
	echo "::error file=VERSION::VERSION debe ser X.Y.Z (sin v delante), no «$v»"
	exit 1
fi
tag="v$v"
out=${GITHUB_OUTPUT:-/dev/stdout}
echo "tag=$tag" >> "$out"

if git rev-parse -q --verify "refs/tags/$tag" > /dev/null; then
	echo "$tag ya existe: no hay versión nueva que publicar"
	echo "new=false" >> "$out"
	exit 0
fi

latest=$(git tag -l 'v*' | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' | sort -V | tail -n 1 || true)
if [ -n "$latest" ] && [ "$(printf '%s\n%s\n' "$latest" "$tag" | sort -V | tail -n 1)" != "$tag" ]; then
	echo "::error file=VERSION::VERSION ($v) es menor que la última versión publicada ($latest)"
	exit 1
fi
echo "Versión nueva: $tag (la última es ${latest:-ninguna}); se publicará al llegar a main"
echo "new=true" >> "$out"
