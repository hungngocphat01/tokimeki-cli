# Build & CI

## Make targets

```bash
make            # dev: fast incremental, no version stamp
make fast       # dev with version stamp
make release    # stripped + trimpath, smaller binary (host arch)
make install    # go install with version stamp

# Cross-compile (release-style; output suffixed with GOOS-GOARCH)
make linux-amd64    # → tokimeki-linux-amd64
make linux-arm64    # → tokimeki-linux-arm64
make darwin-arm64   # → tokimeki-darwin-arm64
make cross          # all three
```

Run from `tokimeki-cli/`. The engine is an independent Go module,
`github.com/hungngocphat01/tokimeki`, fetched like any other dependency. The
repo is private, so Go needs to skip the public proxy and fetch over SSH:

```bash
go env -w GOPRIVATE='github.com/hungngocphat01/*'
git config --global url."git@github.com:".insteadOf "https://github.com/"
```

Developing both side by side: use an uncommitted `go.work` in a parent dir
(`go work init ./tokimeki-cli ./tokimeki-engine`) instead of a `replace`.
`make dev` and `make fast` honor it; release targets ignore it (see below).

## Versioning (CLI ↔ engine)

The two repos are versioned independently with semver tags. **Each CLI commit
pins exactly one engine release in `go.mod`**; that pin is the CLI version's
engine requirement, and it moves only when the CLI deliberately adopts a new
engine.

| Change | What to do |
|---|---|
| CLI-only change | Tag the CLI (`vX.Y.Z`). `go.mod` is untouched, so it keeps the same engine. |
| Engine-only change | Tag the engine. Existing CLI versions keep building against their pinned engine. |
| Engine change the CLI should ship (usual case) | Tag the engine, then in the CLI run `make bump-engine V=vX.Y.Z`, commit, and tag a CLI release. |

Enforcement:

- `make check-engine` (`scripts/check-engine.sh`) fails unless go.mod requires
  the engine at a release tag (`vX.Y.Z`, not a pseudo-version) with no
  `replace`.
- `release`, `install`, and the cross targets run `check-engine` first and
  build with `GOWORK=off`, so a stray `go.work` can't swap in a local engine.
- `go.sum` pins the engine's content hash, so a re-tagged engine fails the build
  instead of silently changing.
- `tokimeki version` prints both versions: `version` (CLI, from
  `git describe`) and `engine` (from Go build info). An engine of `(devel)` or
  `… => path` means the binary was built against a local checkout.
  Runners record the engine version in `workers/<id>/info.json`.

## Cross-compile for clusters

Tokimeki is pure Go (`CGO_ENABLED=0`), so any host can produce a static binary
for any target without a foreign toolchain. Typical workflow when developing
on macOS and deploying to a Linux shared-FS cluster:

```bash
# 1. Build on dev machine (Mac)
cd tokimeki-cli
make linux-amd64                       # use linux-arm64 on aarch64 nodes

# 2. Ship to the cluster
scp tokimeki-linux-amd64 user@host:~/bin/tokimeki
ssh user@host 'chmod +x ~/bin/tokimeki && ~/bin/tokimeki version'
```

**Picking the right GOARCH**: on the target, `uname -m` →
`x86_64` = `amd64`, `aarch64` = `arm64`. PBS/SLURM head nodes usually match
the compute nodes, but verify on a real worker if mixed.

**Hot-replacing a running cluster binary** is safe: Tokimeki runners exec
their own copy on startup (no `tokimeki run --replace-self` magic), and a
new `scp` over the file replaces the inode that future processes will exec.
Already-running runners keep their old text segment until they exit. Roll
out by either letting the manner period reap them, or `tok kill <runner>`
followed by a fresh `tok runner` invocation.

## Why these flags

- `CGO_ENABLED=0` — pure-Go, static binary, faster link, runs anywhere.
- `-buildvcs=false` — we stamp version manually via `-ldflags -X`, so Go's
  auto VCS stamping is redundant. Also avoids failures on detached HEAD or
  missing git config.
- `-ldflags "-X main.version*=..."` — populates `cmd/tokimeki/version.go`
  so `tokimeki version` prints commit/branch/build time.
- `-ldflags "-s -w"` (release only) — strips DWARF + symbols. ~30% smaller.
  **Don't use in dev**: breaks pprof stack traces and debuggers.
- `-trimpath` (release only) — removes absolute paths, reproducible builds,
  doesn't leak `/Users/<you>/...` into the artifact.

## CI

Two workflows, one per repo, both on push to `main`/`master` and on PRs:

- `tokimeki/.github/workflows/ci.yml` — `go vet`, `go build`, `go test -race`.
- `tokimeki-cli/.github/workflows/ci.yml` — test, build, smoke check
  `tokimeki version`. Engine comes in via `go mod download`.

`-race` matters: this codebase is concurrent + FS-heavy.

### Future-me notes

- **Engine repo is private:** CLI CI needs `GOPRIVATE` set and a token for
  module download — e.g. `git config --global url."https://x-access-token:${{ secrets.ENGINE_PAT }}@github.com/".insteadOf "https://github.com/"`.
- **Smoke tests are not in CI** (chose minimal scope). Add an Ubuntu job
  running `bash smoke_test/run_smoke_tests.sh --case all` when E2E coverage
  becomes worth the ~3-5min.
- **NFS bugs won't reproduce in CI** — runners use ext4. Run smoke tests
  on the real cluster before releases.
- **No release automation yet.** When ready: tag-triggered workflow with
  matrix linux/darwin × amd64/arm64 running `make release`, upload via
  `softprops/action-gh-release` or GoReleaser.

## Tip

Auto-rebuild on save: `watchexec -e go -- make dev` (`brew install watchexec`).
