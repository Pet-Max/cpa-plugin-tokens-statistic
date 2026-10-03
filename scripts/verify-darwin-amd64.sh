#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
exec bash "$ROOT_DIR/scripts/verify-darwin.sh" "${1:-$ROOT_DIR/tokens-statistic.dylib}" amd64
