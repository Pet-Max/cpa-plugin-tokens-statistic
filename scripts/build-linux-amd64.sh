#!/usr/bin/env bash
set -euo pipefail

# Build the Linux amd64 c-shared plugin (tokens-statistic).
#
# Defaults to zig cc for cross-compiling from macOS/Windows; set CC to
# override (for example a native gcc when building on Linux).
ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
DIST_DIR="$ROOT_DIR/dist"
VERSION=${VERSION:-v0.1.0}
PLUGIN_ID=${PLUGIN_ID:-tokens-statistic}

export PATH="/usr/local/go/bin:$PATH"
export GOPATH="${GOPATH:-/tmp/gopath}"
export GOPROXY="${GOPROXY:-https://goproxy.cn,direct}"
export CGO_ENABLED=1
export GOOS=linux
export GOARCH=amd64
if [ -z "${CC:-}" ]; then
  if command -v zig >/dev/null 2>&1; then
    CC="zig cc -target x86_64-linux-gnu"
  elif command -v x86_64-linux-gnu-gcc >/dev/null 2>&1; then
    CC="x86_64-linux-gnu-gcc"
  fi
fi
if [ -n "${CC:-}" ]; then
  export CC
  echo "using CC=$CC"
fi

mkdir -p "$GOPATH"
mkdir -p "$DIST_DIR"
cd "$ROOT_DIR"
go build -buildmode=c-shared -buildvcs=false \
  -ldflags="-s -w -X github.com/Pet-Max/cpa-plugin-tokens-statistic/internal/plugin.version=${VERSION}" \
  -o "$DIST_DIR/${PLUGIN_ID}-${VERSION}.so" .
echo "built $DIST_DIR/${PLUGIN_ID}-${VERSION}.so"

# Package the store-conformant release zip: tokens-statistic_<version without v>_<goos>_<goarch>.zip
# with the library at the zip root, plus a checksums.txt for every zip in the release dir.
RELEASE_VERSION=${VERSION#v}
RELEASE_DIR="$ROOT_DIR/dist/release/$VERSION"
mkdir -p "$RELEASE_DIR"
package_zip() { # $1 = zip path, $2 = library path (stored at zip root)
  if command -v zip >/dev/null 2>&1; then
    zip -j -q "$1" "$2"
  elif command -v python >/dev/null 2>&1; then
    python - "$1" "$2" <<'PY'
import os, sys, zipfile
zip_path, file_path = sys.argv[1], sys.argv[2]
with zipfile.ZipFile(zip_path, "w", zipfile.ZIP_DEFLATED) as z:
    z.write(file_path, os.path.basename(file_path))
PY
  else
    echo "packaging requires zip or python" >&2
    exit 1
  fi
}
package_zip "$RELEASE_DIR/${PLUGIN_ID}_${RELEASE_VERSION}_linux_amd64.zip" "$DIST_DIR/${PLUGIN_ID}-${VERSION}.so"
( cd "$RELEASE_DIR" && sha256sum -t -- *.zip > checksums.txt )
echo "release package: $RELEASE_DIR/${PLUGIN_ID}_${RELEASE_VERSION}_linux_amd64.zip"
