#!/usr/bin/env bash
# Compatibility entry point; binary and updates now belong to ilnli/v2node.
set -euo pipefail
exec /usr/local/bin/proxynode-manager "$@"
