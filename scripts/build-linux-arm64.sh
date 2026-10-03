#!/usr/bin/env bash
set -euo pipefail

# Build the Linux arm64 c-shared plugin (tokens-statistic) and the store release zip.
# Uses zig cc for cross-compiling when available, otherwise aarch64-linux-gnu-gcc.
ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
DIST_DIR="$ROOT_DIR/dist"
VERSION=${VERSION:-v0.1.0}
PLUGIN_ID=${PLUGIN_ID:-tokens-statistic}
RELEASE_ARTIFACT="$DIST_DIR/${PLUGIN_ID}-${VERSION}-linux-arm64.so"

# Optional: set CLASH_PROXY_URL (or the standard *_PROXY variables) to route module
# downloads through a local proxy. By default the ambient environment applies.
if [[ -n "${CLASH_PROXY_URL:-}" ]]; then
  export HTTP_PROXY="$CLASH_PROXY_URL" HTTPS_PROXY="$CLASH_PROXY_URL" ALL_PROXY="$CLASH_PROXY_URL"
  export http_proxy="$CLASH_PROXY_URL" https_proxy="$CLASH_PROXY_URL" all_proxy="$CLASH_PROXY_URL"
  echo "using proxy $CLASH_PROXY_URL"
fi

command -v go >/dev/null 2>&1 || { printf 'Required command not found: go\n' >&2; exit 1; }
if command -v zig >/dev/null 2>&1; then
  CC="zig cc -target aarch64-linux-gnu"
elif command -v aarch64-linux-gnu-gcc >/dev/null 2>&1; then
  CC="aarch64-linux-gnu-gcc"
else
  printf 'Required cross compiler not found: zig (or aarch64-linux-gnu-gcc)\n' >&2
  exit 1
fi
export CC
echo "using CC=$CC"

mkdir -p "$DIST_DIR"
cd "$ROOT_DIR"
go mod download
CGO_ENABLED=1 \
GOOS=linux \
GOARCH=arm64 \
go build \
  -buildmode=c-shared \
  -trimpath \
  -buildvcs=false \
  -ldflags="-s -w -X github.com/Pet-Max/cpa-plugin-tokens-statistic/internal/plugin.version=${VERSION}" \
  -o "$RELEASE_ARTIFACT" \
  .
printf 'Built release artifact: %s\n' "$RELEASE_ARTIFACT"

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
package_zip "$RELEASE_DIR/${PLUGIN_ID}_${RELEASE_VERSION}_linux_arm64.zip" "$RELEASE_ARTIFACT"
( cd "$RELEASE_DIR" && sha256sum -t -- *.zip > checksums.txt )
echo "release package: $RELEASE_DIR/${PLUGIN_ID}_${RELEASE_VERSION}_linux_arm64.zip"
