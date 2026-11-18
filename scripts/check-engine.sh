#!/usr/bin/env bash
# Verifies that go.mod pins the engine to a release tag (vX.Y.Z) with no
# replace directive, so release builds link exactly the engine this CLI
# version was tested against. The CLI and engine share one version number:
# on a commit tagged vX.Y.Z, the engine must be pinned at vX.Y.Z too. go.work is ignored (GOWORK=off) to match how
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

cli=$(git describe --tags --exact-match --match 'v[0-9]*.[0-9]*.[0-9]*' HEAD 2>/dev/null || true)
if [[ -n $cli && $cli != "$ver" ]]; then
	fail "CLI is tagged $cli but go.mod requires $engine $ver; versions must match"
fi

echo "engine $ver${cli:+ (matches CLI $cli)}"
