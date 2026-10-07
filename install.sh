#!/bin/sh
# Instalador de lray para Linux y macOS.
#   curl -fsSL https://raw.githubusercontent.com/tu-usuario/lray/main/install.sh | sh
# Variables opcionales: LRAY_REPO, LRAY_VERSION (p. ej. v0.2.0), LRAY_BIN_DIR
set -eu

REPO="${LRAY_REPO:-tu-usuario/lray}"
VERSION="${LRAY_VERSION:-latest}"
BIN_DIR="${LRAY_BIN_DIR:-$HOME/.local/bin}"

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in
  linux|darwin) ;;
  *) echo "Sistema no soportado: $os. En Windows descarga el .zip de la release." >&2; exit 1 ;;
esac

arch=$(uname -m)
case "$arch" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) echo "Arquitectura no soportada: $arch" >&2; exit 1 ;;
esac

if [ "$VERSION" = latest ]; then
  base="https://github.com/$REPO/releases/latest/download"
else
  base="https://github.com/$REPO/releases/download/$VERSION"
fi
asset="lray_${os}_${arch}.tar.gz"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "Descargando $asset ($VERSION)..."
curl -fsSL -o "$tmp/$asset" "$base/$asset"
curl -fsSL -o "$tmp/checksums.txt" "$base/checksums.txt"

expected=$(grep " $asset\$" "$tmp/checksums.txt" | cut -d' ' -f1)
if command -v sha256sum >/dev/null 2>&1; then
  actual=$(sha256sum "$tmp/$asset" | cut -d' ' -f1)
else
  actual=$(shasum -a 256 "$tmp/$asset" | cut -d' ' -f1)
fi
if [ -z "$expected" ] || [ "$expected" != "$actual" ]; then
  echo "El checksum no coincide; abortando." >&2
  exit 1
fi

tar -xzf "$tmp/$asset" -C "$tmp"
mkdir -p "$BIN_DIR"
install -m 0755 "$tmp/lray" "$BIN_DIR/lray"
echo "lray instalado en $BIN_DIR/lray"

case ":$PATH:" in
  *":$BIN_DIR:"*) ;;
  *) echo "Aviso: $BIN_DIR no está en tu PATH. Añádelo en tu ~/.zshrc o ~/.bashrc:"
     echo "  export PATH=\"$BIN_DIR:\$PATH\"" ;;
esac
