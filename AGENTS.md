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
| `cmd/tokimeki/main.go` | All cobra commands (`runner`, `submit`, `ps`, `runners`, `ls`, `top`, `stats`, `tui`, `job`, `group`, `result`, `report`, `logs`, `watch`, `events`, `kill`, `cancel`, `shell`, `exec`, `kp`, `gc`, `version`, `completion`): parse arguments, call the engine `client`/`runner`, hand results to a renderer |
| `cmd/tokimeki/render_jobs.go`, `render_runners.go` | Human tables (`ps`, `group` summary, `runners` slot use) and the `top` dashboard, rendered from engine result types |
| `cmd/tokimeki/root.go` | Root command: global `--base`, `--verbose` (turns on engine `debug` output on stderr) |
| `cmd/tokimeki/notices.go` | Prints engine notices: `warning: …` / `--verbose` debug lines on stderr, NDJSON on stderr with `--json`; `tok tui` holds them until exit |
| `cmd/tokimeki/jsonout.go` | `--json` output (`writeJSON`, `writeNDJSON`) and the `{"error":{"code","message"}}` envelope; `execute()` is the entry point shared by `main` and tests |
| `cmd/tokimeki/golden_test.go`, `testdata/` | Golden tests: every command's stdout/stderr against a static state directory (`testdata/fixture`); regenerate with `go test ./cmd/tokimeki -run TestGolden -update` and review the diff |
| `display/` | Pure formatting helpers: colors, status glyphs, durations, sizes, timestamps, command shortening |
| `cmd/tokimeki/version.go` | `tokimeki version`: CLI version (ldflags), engine version (`version.Engine()`), commit, branch, build time |
| `tui/` | `tok tui` dashboard: `tui.go` model/update, `view.go` rendering, `data.go` loading through the engine `client`, `actions.go` kill/cancel/shell actions |
| `scripts/check-engine.sh` | Release guard for the engine pin (see Versioning) |
| `docs/` | `HOW-IT-WORKS.md` (how jobs, runners, groups and files fit together, for users), `USAGE.md` (CLI reference), `BUILD.md` (build/CI/versioning). User-facing only — see "Where docs go" |
| `RELEASE_NOTES.txt` | User-facing notes mailed with binary drops |

**The CLI is a shim.** It parses arguments and formats engine results; it
never reads or writes state files, never imports `registry`, `eventlog` or
`fsutil`, and never parses on-disk timestamps. Check with:

```bash
grep -rnE 'tokimeki/(registry|eventlog|fsutil|jobschema)"|os\.(ReadFile|Open|ReadDir)\(|json\.Unmarshal|time\.Parse\(' cmd tui display | grep -v _test
```

The only expected match is `events --since` parsing the user's argument.
Engine results use raw units (Unix ns timestamps, seconds, bytes) and coded
errors (`client.CodeOf`); conversion to human units happens in `display/`.

Behavior changes to queueing, runners, on-disk state, or the protocol belong
in the engine repo, not here. If a CLI feature needs new engine behavior,
make the engine change first (see "Working alongside the engine").

## Where docs go

| Content | Location |
|---|---|
| Internal behaviour: protocol, filesystem layout, `meta.json` schema, job environment contract, design memos, plans | Engine repo `docs/` (`tokimeki-engine/docs`) |
| Anything a user sees: commands, flags, output and JSON schemas, config files, recipes, build/install | This repo's `docs/` and `README.md` — keep it to what a user needs; don't describe engine internals |
| Python package specifics | `tokimeki-py/docs` |

Design memos and in-flight plans live in the engine repo
(`docs/memos/`, `docs/superpowers/plans/`).

## Diagrams

README figures are TikZ, not Mermaid. Sources are in
`assets/diagrams/tikz/`, one `.tex` per figure, sharing `style.tex`
(colours, styles and the `\machine`, `\proc`, `\tray`, `\diskslab` macros).

```bash
assets/diagrams/tikz/render.sh                # every figure → assets/<name>.svg
assets/diagrams/tikz/preview.sh OUTDIR name   # SVG → PNG via Quick Look (macOS), to check the result
```

`render.sh` needs `latex` and `dvisvgm` (TeX Live). It goes through DVI, so
Ghostscript is not needed; glyphs become paths, so the SVGs carry no fonts.
Render straight into `assets/` and look at every render before reporting it
done. `coordinators.svg`, `loops.svg` and `python-job.svg` are used by the
Python package's README: copy them to `tokimeki-py/assets/` after
re-rendering.

What to draw:

- **One figure, one situation.** A reader glancing at it should see how
  that part of the system works. The README text around a figure describes
  exactly the situation drawn (e.g. "three runners, two coordinators
  submitted, so node 3 has none").
- **User-level concepts only:** physical nodes, runners, jobs and the
  scripts they come from, the queue, the shared filesystem, and inside a
  runner the coordinator and executors. No file names, paths or JSON;
  `inbox.svg` is the one figure where inbox/outbox detail is the point.
- **No text that isn't a label.** No captions, notes or disclaimers in the
  image; explanation goes in the README prose. A legend is only for an
  encoding that labels can't carry, kept small at the bottom left, like the
  completed / running / pending key in `loops.svg`.

How to draw it:

- A physical node is one rectangle with a dark header (`\machine`); the
  runner is named in that header or drawn as the green box inside
  (`\proc`). The shared filesystem is the grey slab along the bottom
  (`\diskslab`); places on it are `\tray`s.
- Things running inside a runner (coordinator, executors) are stacked
  vertically inside the node, not spread horizontally.
- Jobs are cards (`card`); jobs of different groups are told apart by
  colour (`groupA`, `groupB`), the coordinator by a thicker border, a free
  slot by a dashed outline.
- Solid arrow = writes a file, dashed arrow = reads one. Every exchange
  between nodes goes through the filesystem slab, never node to node.
- Label arrows with a short verb (`submit`, `take`) at the end of the line
  nearest the filesystem, not in the middle. Numbered circles (`num`) mark
  steps when order matters.
- No overlaps: lines never cross labels, boxes never touch, and lanes are
  nested so they don't cross each other.
- Keep it compact: small gaps between nodes and the slab, no empty space
  inside boxes.
- Boxes have square corners; don't add `rounded corners`.

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
- A command that takes its arguments as JSON does so with `--from-json FILE`;
  `--from-json -` reads the JSON from stdin. Use this flag name for every such
  command (not `--input`, `--spec`, etc.).
- Run `gofmt` on files you touch.
- Build artifacts (`tokimeki-linux-*`, tarballs) are not tracked; don't commit
  them.
- Commits: conventional-style subjects (`feat:`, `fix:`, `docs:`, `chore:`).
  Commit or push only when asked.
