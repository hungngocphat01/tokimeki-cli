# Memo: Python front-end and engine-as-core rework

Status: **phase 1 planned** (refactor only; Python deferred). Plan:
`tokimeki-engine/docs/superpowers/plans/2026-11-15-engine-core-cli-shim.md`.
Started: 2026-11-15

## Motivation

Current ML workflow (e.g. the `evident` project):

1. One bash script submits one Python training driver as one Tokimeki job.
2. The driver expands a grid (~20 configs) and runs it **sequentially**.
3. To keep one GPU busy, the driver packs `n_workers` (e.g. 3) independent
   training processes at a time.
4. Results are combined at the end.

**Problem:** grid work is bound to the job that owns it. Submit two drivers;
if one finishes first, its runner goes idle while the other still has
configs queued.

**Eventual fix (later phase):** submit each config as its own unit of work
(sub-job) so any free runner can pick it up. The existing protocol is mostly
sufficient; the grid expand / run / combine logic is deferred.

- `evident` reference path: `/Volumes/s2510159/sources/evident` — not mounted
  on the dev Mac as of 2026-11-15; needed when the sub-job phase starts.

## Phase 1: engine = core, front-ends = shims

| Layer | Owns | Must not |
|---|---|---|
| Engine (`hungngocphat01/tokimeki`) | Data processing, protocol handling, all filesystem reads/writes; returns typed structs with raw units and error codes | Format strings, print, write to `io.Writer` for display |
| Go CLI (this repo) | Argument parsing, calling the engine, rendering (tables, colors, local time, relative time) | Read or parse state files, parse timestamps from disk |
| Python package (new) | Submit API (+ maybe status/wait/cancel) for ML scripts, mapping engine results/errors to Python objects/exceptions | Reimplement protocol or registry logic |

The engine API must be generic enough for further front-ends.

### Return conventions (proposed)

- Timestamps: int64 Unix nanoseconds (`*_ns`); 0 = unset.
- Durations: seconds (float).
- Status: enum values, not display strings.
- Errors: stable error codes front-ends can branch on, plus a message.
- On-disk JSON format (`meta.json` etc., RFC3339 strings) stays unchanged so
  mixed-version runners keep working; conversion happens in the engine API.

## Findings: what has to move

- Engine `client/` renders output: `ps.go`, `stats.go`, `top.go`,
  `workers.go`, `logs.go`, `exec.go`, `shell.go` write formatted text to
  writers (~89 print calls). → return data; formatting moves to the CLI.
- CLI TUI reads state directly: `tui/data.go`, `tui/actions.go` read
  `meta.json`, `queue.json`, `info.json`, heartbeat, `current.json`,
  `events.ndjson`, `stats.json`. → move behind engine functions.
- CLI parses timestamps from disk: `cmd/tokimeki/main.go` (~L642 elapsed time),
  `tui/view.go`. → disappears once the engine returns epochs/seconds.
- This breaks the engine Go API → engine minor release (e.g. `v0.2.0`) plus a
  CLI release adopting it via `make bump-engine`.

## Open questions

1. **Python ↔ Go binding**
   - (a) cgo `c-shared` library + ctypes, JSON across the C boundary. Literal
     "call Go functions", but needs cgo + per-platform builds (cluster:
     linux-amd64), and puts a Go runtime inside the training process. The Go
     runtime is not fork-safe, and training code relies on `multiprocessing`.
   - (b) Python execs the `tokimeki` binary's regular commands with `--json`
     (decided 2026-11-15: **no separate `api` command**). Same engine schema
     for both front-ends, fork-safe, one static binary; costs a process spawn
     (few ms) per call.
   - Decided: machine mode = `--json` on normal commands, emitting engine
     result types + `{"error":{"code","message"}}`. Binding choice (a vs b)
     for the Python package itself is still open, deferred with Python.
2. ~~Timestamp unit~~ — decided: int64 Unix ns (`*_ns`), durations float64 seconds (`*_secs`); ns needed to round-trip event timestamps.
3. Python phase-1 scope: submit only, submit + status/wait/cancel, or
   read-only parity (`ps`, `logs`, …)?
4. Ordering: land the version-coupling work first (push engine `b39295a`,
   tag `v0.1.0`, `make bump-engine V=v0.1.0`, commit CLI)?

## Status (2026-11-15)

- Phase 1 (Go refactor: engine core returns typed data, CLI shim, `--json`
  engine schema) is done on `refactor/engine-core` in both repos.
- Python front-end remains deferred; open questions 1 (binding a vs b) and 3
  (scope) above still stand.
- Release steps (push engine `main`, tag `v0.1.0`/`v0.2.0`, `make
  bump-engine`) await the user's go-ahead.
- Engine smoke harness still builds `./cmd/tokimeki`, which only exists in
  the CLI repo; use `--binary` until fixed.

## Log

- 2026-11-15 — Scoped phase 1; understanding confirmed with user; memo created.
- 2026-11-15 — Rescoped: phase 1 is the Go refactor only, preserving behavior; Python deferred.
  Machine output = `--json` on regular commands (no `api` command). Plan written.
- 2026-11-15 — Phase 1 implemented: engine `client` returns typed results (ns/secs/bytes, coded errors) and
  never prints; CLI renders from them (goldens unchanged except documented diffs); `--json` emits the
  engine schema everywhere. Verified side by side with the pre-refactor binary locally and on a two-node
  Docker setup (scheduling, targeting, exec, cross-node kill, TLS shell).
