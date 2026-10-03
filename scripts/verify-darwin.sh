#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
ARTIFACT=${1:-"$ROOT_DIR/tokens-statistic.dylib"}
TARGET_ARCH=${2:-}

if [[ -z "$TARGET_ARCH" || ( "$TARGET_ARCH" != "arm64" && "$TARGET_ARCH" != "amd64" ) ]]; then
  printf 'usage: %s <plugin-library> <arm64|amd64>\n' "${BASH_SOURCE[0]}" >&2
  exit 2
fi

for command in go file nm clang; do
  command -v "$command" >/dev/null || {
    printf 'Required command not found: %s\n' "$command" >&2
    exit 1
  }
done

[[ "$(uname -s)" == "Darwin" ]] || {
  printf 'This verification must run on macOS.\n' >&2
  exit 1
}
[[ -f "$ARTIFACT" ]] || {
  printf 'Artifact not found: %s\n' "$ARTIFACT" >&2
  exit 1
}

HOST_ARCH=$(go env GOHOSTARCH)

cd "$ROOT_DIR"
if [[ -n "$(gofmt -l -- *.go)" ]]; then
  printf 'Go source is not formatted.\n' >&2
  gofmt -l -- *.go >&2
  exit 1
fi
go vet ./...
go test ./...

EXPECTED_FILE_ARCH=$([[ "$TARGET_ARCH" == "amd64" ]] && printf 'x86_64' || printf 'arm64')
file "$ARTIFACT" | grep -Eq "Mach-O 64-bit dynamically linked shared library ${EXPECTED_FILE_ARCH}"
for symbol in cliproxy_plugin_init cliproxyPluginCall cliproxyPluginFree cliproxyPluginShutdown; do
  nm -gU "$ARTIFACT" | grep -Eq "[[:space:]]_?${symbol}$" || {
    printf 'Required exported symbol missing: %s\n' "$symbol" >&2
    exit 1
  }
done

TEST_ROOT=$(mktemp -d "${TMPDIR:-/tmp}/cap-token-usage-darwin.XXXXXX")
trap 'rm -rf -- "$TEST_ROOT"' EXIT
INSTALL_DIR="$TEST_ROOT/CLIProxyAPI/plugins/darwin/$HOST_ARCH"
INSTALLED_PLUGIN="$INSTALL_DIR/tokens-statistic.dylib"
EXPECTED_DATABASE="$TEST_ROOT/CLIProxyAPI/data/tokens-statistic.db"
SMOKE_BINARY="$TEST_ROOT/abi-smoke-darwin"

mkdir -p "$INSTALL_DIR"
if [[ "$HOST_ARCH" == "$TARGET_ARCH" ]]; then
  cp "$ARTIFACT" "$INSTALLED_PLUGIN"
else
  CGO_ENABLED=1 GOOS=darwin GOARCH="$HOST_ARCH" \
    go build -buildmode=c-shared -trimpath -buildvcs=false -o "$INSTALLED_PLUGIN" .
fi
clang -O2 -Wall -Wextra -o "$SMOKE_BINARY" "$ROOT_DIR/scripts/abi-smoke.c"

(
  cd /
  "$SMOKE_BINARY" "$INSTALLED_PLUGIN" "$EXPECTED_DATABASE"
)

printf 'Verified macOS %s default database path: %s\n' "$TARGET_ARCH" "$EXPECTED_DATABASE"
