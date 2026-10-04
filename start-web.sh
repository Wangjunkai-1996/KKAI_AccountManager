#!/usr/bin/env bash
set -euo pipefail

# Keep the Web launcher on the same driver/Node setup as the local launcher.
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
exec "$SCRIPT_DIR/start-local.sh" "$@"
