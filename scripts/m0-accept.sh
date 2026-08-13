#!/usr/bin/env bash
# Thin wrapper kept for CI compatibility: the full acceptance lives in accept.sh.
exec "$(dirname "${BASH_SOURCE[0]}")/accept.sh" "$@"
