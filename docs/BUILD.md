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

Run from `tokimeki-cli/`. Engine must be populated at `engine/`:
`git submodule update --init` if empty.

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
- `tokimeki-cli/.github/workflows/ci.yml` — checkout with `submodules: recursive`,
  test, build, smoke check `tokimeki version`.

`-race` matters: this codebase is concurrent + FS-heavy.

### Future-me notes

- **If engine repo goes private:** CLI checkout needs a token —
  `with: { submodules: recursive, token: ${{ secrets.ENGINE_PAT }} }`.
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
