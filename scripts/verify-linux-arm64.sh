#!/usr/bin/env bash
set -euo pipefail

# Verify a linux/arm64 c-shared plugin artifact: ELF layout, exported
# symbols, and a runtime ABI smoke test (dlopen + plugin.register) under
# qemu. Mirrors scripts/verify-darwin.sh.
#
# Usage: bash scripts/verify-linux-arm64.sh <tokens-statistic-v<version>.so>
# The artifact may be passed as a bare name, a relative or an absolute path;
# it is resolved to an absolute path below because dlopen treats slash-less
# names as library sonames and never searches the current directory.
#
# On Debian/Ubuntu: sudo apt-get install -y gcc-aarch64-linux-gnu qemu-user

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
ARTIFACT=${1:-}

if [[ -z "$ARTIFACT" || ! -f "$ARTIFACT" ]]; then
  printf 'usage: %s <linux-arm64-plugin-library>\n' "${BASH_SOURCE[0]}" >&2
  exit 2
fi

# The smoke binary dlopen()s this path inside qemu; a slash-less name is
# looked up as a soname (LD_LIBRARY_PATH, ld.so.cache, /lib, /usr/lib) and
# never in the current directory, while file/readelf/nm above open it as a
# plain file. Resolve to an absolute path so dlopen always succeeds.
ARTIFACT=$(readlink -f -- "$ARTIFACT")

for command in go file readelf nm aarch64-linux-gnu-gcc qemu-aarch64; do
  command -v "$command" >/dev/null || {
    printf 'Required command not found: %s\n' "$command" >&2
    exit 1
  }
done

cd "$ROOT_DIR"

if [[ -n "$(gofmt -l -- *.go)" ]]; then
  printf 'Go source is not formatted.\n' >&2
  gofmt -l -- *.go >&2
  exit 1
fi

go vet ./...
go test ./...

file "$ARTIFACT" | grep -Eq 'ELF 64-bit.*(ARM aarch64|AArch64)'
readelf -h "$ARTIFACT" | grep -Eq 'Class:[[:space:]]+ELF64'
readelf -h "$ARTIFACT" | grep -Eq 'Type:[[:space:]]+DYN'
readelf -h "$ARTIFACT" | grep -Eq 'Machine:[[:space:]]+AArch64'

for symbol in cliproxy_plugin_init cliproxyPluginCall cliproxyPluginFree cliproxyPluginShutdown; do
  nm -D --defined-only "$ARTIFACT" | grep -Eq "[[:space:]]${symbol}$" || {
    printf 'Required exported symbol missing: %s\n' "$symbol" >&2
    exit 1
  }
done

printf 'Dynamic dependencies:\n'
readelf -d "$ARTIFACT" | grep NEEDED || true

SMOKE_BINARY=$(mktemp "${TMPDIR:-/tmp}/abi-smoke-arm64.XXXXXX")
trap 'rm -f -- "$SMOKE_BINARY"' EXIT
aarch64-linux-gnu-gcc -O2 -Wall -Wextra -o "$SMOKE_BINARY" "$ROOT_DIR/scripts/abi-smoke.c" -ldl
qemu-aarch64 -L /usr/aarch64-linux-gnu "$SMOKE_BINARY" "$ARTIFACT"

printf 'Verified linux/arm64 artifact: %s\n' "$ARTIFACT"
