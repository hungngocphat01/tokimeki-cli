# AGENTS.md — tokimeki CLI

The `tokimeki` (alias `tok`) binary: cobra commands and a Bubble Tea TUI on
top of the Tokimeki engine. All job-runner logic — queueing, runner daemon,
filesystem protocol, shell sessions — lives in the **separate engine repo**;
this repo is the user-facing layer. Read `README.md` for what Tokimeki is,
`docs/USAGE.md` for command behavior, `docs/BUILD.md` for builds and
versioning.

- Module: `github.com/ngocphat/tokimeki-cli`
- Engine dependency: `github.com/hungngocphat01/tokimeki`, a regular Go module
  pinned in `go.mod` (no submodule, no `replace`). The repo is private:

  ```bash
  go env -w GOPRIVATE='github.com/hungngocphat01/*'
  git config --global url."git@github.com:".insteadOf "https://github.com/"
  ```

## Layout

| Path | Responsibility |
|---|---|
| `cmd/tokimeki/main.go` | All cobra commands (`runner`, `submit`, `ps`, `runners`, `ls`, `top`, `stats`, `tui`, `job`, `logs`, `watch`, `events`, `kill`, `cancel`, `shell`, `exec`, `kp`, `gc`, `version`, `completion`): parse arguments, call the engine `client`/`runner`, hand results to a renderer |
| `cmd/tokimeki/render_jobs.go`, `render_runners.go` | Human tables and the `top` dashboard, rendered from engine result types |
| `cmd/tokimeki/jsonout.go` | `--json` output (`writeJSON`, `writeNDJSON`) and the `{"error":{"code","message"}}` envelope; `execute()` is the entry point shared by `main` and tests |
| `cmd/tokimeki/golden_test.go`, `testdata/` | Golden tests: every command's stdout/stderr against a static state directory (`testdata/fixture`); regenerate with `go test ./cmd/tokimeki -run TestGolden -update` and review the diff |
| `display/` | Pure formatting helpers: colors, status glyphs, durations, sizes, timestamps, command shortening |
| `cmd/tokimeki/version.go` | `tokimeki version`: CLI version (ldflags), engine version (`version.Engine()`), commit, branch, build time |
| `tui/` | `tok tui` dashboard: `tui.go` model/update, `view.go` rendering, `data.go` loading through the engine `client`, `actions.go` kill/cancel/shell actions |
| `scripts/check-engine.sh` | Release guard for the engine pin (see Versioning) |
| `docs/` | `USAGE.md` (CLI reference), `BUILD.md` (build/CI/versioning) |
| `docs/memos/` | Working memos / scratchpad for in-flight design work (`YYYY-MM-DD-topic.md`); check here for current plans and open questions |
| `RELEASE_NOTES.txt` | User-facing notes mailed with binary drops |

**The CLI is a shim.** It parses arguments and formats engine results; it
never reads or writes state files, never imports `registry`, `eventlog` or
`fsutil`, and never parses on-disk timestamps. Check with:

```bash
grep -rnE 'tokimeki/(registry|eventlog|fsutil)"|os\.(ReadFile|Open|ReadDir)\(|json\.Unmarshal|time\.Parse\(' cmd tui display | grep -v _test
```

The only expected match is `events --since` parsing the user's argument.
Engine results use raw units (Unix ns timestamps, seconds, bytes) and coded
errors (`client.CodeOf`); conversion to human units happens in `display/`.

Behavior changes to queueing, runners, on-disk state, or the protocol belong
in the engine repo, not here. If a CLI feature needs new engine behavior,
make the engine change first (see "Working alongside the engine").

## Commands

```bash
make                 # dev build → ./tokimeki (no version stamp, honors go.work)
make fast            # dev build with version stamp
go vet ./... && go test ./...

make check-engine    # verify go.mod pins a tagged engine release
make release         # host-arch stripped build (runs check-engine, GOWORK=off)
make linux-amd64     # cluster build; also linux-arm64, darwin-arm64, cross
make bump-engine V=vX.Y.Z   # pin the engine release with the same version
```

`CGO_ENABLED=0` is exported by the Makefile; binaries are static.

## Versioning

CLI and engine share one version number. A release tags both repos with the
same `vX.Y.Z`, and **CLI `vX.Y.Z` pins engine `vX.Y.Z` in `go.mod`**; the pin
only changes via `make bump-engine`.

- Release flow: engine tag → `make bump-engine V=vX.Y.Z` → commit → CLI tag
  `vX.Y.Z`.
- If only one repo changed, still tag both; the unchanged repo is tagged at its
  current `main`.
- `make check-engine` fails on a commit tagged `vX.Y.Z` whose engine pin is a
  different version.

Release targets (`release`, `install`, `linux-*`, `darwin-*`) run
`scripts/check-engine.sh` and build with `GOWORK=off`. The check fails if the
engine requirement is a pseudo-version or is `replace`d. Don't weaken it to
make a build pass — tag the engine instead. `tokimeki version` showing
`engine: (devel)` or `... => <path>` means the binary was built against a
local engine checkout and must not be shipped.

## Working alongside the engine

For changes spanning both repos, use an **uncommitted** `go.work` in a parent
directory:

```bash
cd .. && go work init ./tokimeki-cli ./tokimeki-engine
```

`make`/`make fast`/`go test` pick it up; release targets ignore it. Never
commit a `go.work` or add a `replace` to `go.mod`. Ship by tagging the engine
and bumping the pin.

## Conventions

- Keep `docs/USAGE.md` (and the README CLI reference) in sync when adding or
  changing commands, flags, or TUI keys.
- Human output is colorized only on a TTY and respects `NO_COLOR`; tables use
  local time (`TZ`). `--json` prints the engine result types unchanged
  (schema in `docs/USAGE.md`), and errors as the coded envelope.
- Run `gofmt` on files you touch.
- Build artifacts (`tokimeki-linux-*`, tarballs) are not tracked; don't commit
  them.
- Commits: conventional-style subjects (`feat:`, `fix:`, `docs:`, `chore:`).
  Commit or push only when asked.
