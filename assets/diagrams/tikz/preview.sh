#!/usr/bin/env bash
# Preview rendered SVGs as PNGs (macOS Quick Look): ./preview.sh OUTDIR name...
set -euo pipefail
out="$1"; shift
for n in "$@"; do qlmanage -t -s 1800 -o "$out" "$(dirname "$0")/../../$n.svg" > /dev/null 2>&1; done
