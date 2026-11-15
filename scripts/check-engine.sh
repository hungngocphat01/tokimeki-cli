#!/usr/bin/env bash
# Verifies that go.mod pins the engine to a release tag (vX.Y.Z) with no
# replace directive, so release builds link exactly the engine this CLI
# version was tested against. go.work is ignored (GOWORK=off) to match how
# release targets build.
set -euo pipefail

cd "$(dirname "$0")/.."

engine=github.com/hungngocphat01/tokimeki
line=$(GOWORK=off go list -m -f '{{.Version}}|{{with .Replace}}{{.Path}}{{end}}' "$engine")
ver=${line%%|*}
repl=${line#*|}

fail() {
	echo "check-engine: $*" >&2
	echo "check-engine: run 'make bump-engine V=vX.Y.Z' with a tagged engine release" >&2
	exit 1
}

if [[ -n $repl ]]; then
	fail "go.mod replaces $engine with $repl; remove the replace directive"
fi
if ! [[ $ver =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
	fail "go.mod requires $engine $ver, which is not a release tag"
fi

echo "engine $ver"
