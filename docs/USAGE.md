# Tokimeki — Usage Guide

CLI reference and recipes for the `tokimeki` binary. See `README.md` for architecture; this file is "what can I do with it, and how."

## Table of contents

- [Setup](#setup)
- [Quick reference](#quick-reference)
- [Commands by use case](#commands-by-use-case)
  - [Run a runner](#run-a-runner)
  - [Submit work](#submit-work)
  - [Inspect state](#inspect-state)
  - [Control running jobs](#control-running-jobs)
  - [Logs and history](#logs-and-history)
  - [Cleanup](#cleanup)
  - [Live debugging on a runner](#live-debugging-on-a-runner)
- [Scheduling features](#scheduling-features)
  - [Dependencies (`--after`)](#dependencies---after)
  - [Priority (`--priority`)](#priority---priority)
  - [Resource hints (`--cpus`, `--mem-mb`)](#resource-hints---cpus---mem-mb)
  - [Retries (`--retries`, `--backoff`)](#retries---retries---backoff)
  - [Burst scheduling](#burst-scheduling)
- [JSON output](#json-output)
- [Event log](#event-log)
- [Shell completion](#shell-completion)
- [Environment and configuration](#environment-and-configuration)

---

## Setup

State directory:

```bash
export TOKIMEKI_HOME=/shared-fs/tokimeki   # or use --base on every command
```

Defaults to `~/.tokimeki`. On a cluster, point this at a shared filesystem visible to every node that runs a runner or submits jobs.

---

## Quick reference

| Command | One-liner |
|---|---|
| `runner` | Start the daemon on this node |
| `submit` | Queue a job (script or inline command) |
| `ps` | List running/queued jobs |
| `runners` | List runners and heartbeat status |
| `ls` | `ps` + `runners` together |
| `top` | Live dashboard, refreshes each tick |
| `job <id>` | Show full job metadata + script |
| `logs <id>` | Print stdout/stderr (with `--follow`, `--tail`) |
| `events` | Stream the shared event log |
| `kill` | Send signal to a running job |
| `cancel` | Remove a queued job |
| `exec` | Run an ad-hoc command on a runner |
| `gc` | Garbage-collect old jobs and dead workers |
| `version` | Build/version info |
| `completion` | Generate shell completion |

Every command accepts `--base PATH` to override `TOKIMEKI_HOME`.

---

## Commands by use case

### Run a runner

```bash
tokimeki runner --id w-gpu01 --poll 2s --manner-period 1h
```

Flags:

- `--id`: explicit worker ID (default: `<hostname>-<4 hex>`)
- `--poll`: how often the daemon scans the inbox / queue
- `--manner-period`: exit cleanly after being jobless this long. `0` disables.
- `--capacity-cpus N`: free CPUs available for capacity-aware scheduling. `0` = unconstrained.
- `--capacity-mem-mb M`: free memory MB. `0` = unconstrained.

**When to set capacity**: when multiple runners share a node or you want to refuse jobs that don't fit. Otherwise leave at 0.

### Submit work

Inline command:

```bash
tokimeki submit -c "python train.py --epochs 5"
```

Script file:

```bash
tokimeki submit ./scripts/eval.sh
```

Pin to a specific runner:

```bash
tokimeki submit -w w-gpu01 -c "nvidia-smi"
```

Resubmit a previous job (requires running daemon and `-w`):

```bash
tokimeki submit -r -w w-gpu01 abc12345
```

All scheduling flags are stackable on submit: `--after`, `--priority`, `--cpus`, `--mem-mb`, `--retries`, `--backoff`. See [scheduling features](#scheduling-features).

### Inspect state

Active jobs:

```bash
tokimeki ps                # running + queued
tokimeki ps -a             # include finished
tokimeki ps -w w-gpu01     # filter by worker
tokimeki ps --json | jq    # machine-readable
```

Runners:

```bash
tokimeki runners            # only running
tokimeki runners -a         # include stopped
tokimeki runners --json
```

A specific job:

```bash
tokimeki job abc12345           # human form (meta + script)
tokimeki job abc12345 --json    # meta only
```

Live dashboard:

```bash
tokimeki top                 # refresh every 1s
tokimeki top -i 500ms        # faster
tokimeki top --once          # one frame, exit (good for scripts)
```

### Control running jobs

Cancel a queued (not yet running) job:

```bash
tokimeki cancel abc12345
```

Send a signal to a running job:

```bash
tokimeki kill w-gpu01 abc12345              # SIGTERM (15)
tokimeki kill w-gpu01 abc12345 --signal 9   # SIGKILL
```

### Logs and history

Print logs:

```bash
tokimeki logs abc12345                  # stdout + stderr
tokimeki logs abc12345 --stdout         # stdout only
tokimeki logs abc12345 --stderr         # stderr only
tokimeki logs abc12345 -n 50            # last 50 lines
tokimeki logs abc12345 -f               # follow until job finishes
tokimeki logs abc12345 -n 50 -f         # like `tail -f`
```

Logs persist after the job finishes (until `gc` removes them).

### Cleanup

```bash
tokimeki gc                                  # remove jobs >7d old + dead worker dirs
tokimeki gc --older-than 24h                 # last day only
tokimeki gc --max-size 10G                   # cap total done size, evict oldest
tokimeki gc --older-than 24h --max-size 5G   # both rules
tokimeki gc --dry-run                        # preview, no removals
```

Run `gc` periodically — there is no automatic cleanup.

### Live debugging on a runner

Run an ad-hoc command (batch — wait, get output back):

```bash
tokimeki exec w-gpu01 "nvidia-smi"
tokimeki exec w-gpu01 "cat /proc/cpuinfo | head"
```

Run interactively (line-buffered, **no PTY** — works for REPLs/shells, not vim/htop):

```bash
tokimeki exec w-gpu01 -i bash
tokimeki exec w-gpu01 -i python
tokimeki exec w-gpu01 -i psql -d mydb
```

Use `Ctrl-D` to close stdin and exit. For full-screen TUIs, use your cluster's interactive job mechanism (`qsub -I`, `srun --pty`) instead.

---

## Scheduling features

### Dependencies (`--after`)

Job B runs only after job A reaches `completed`:

```bash
JOB_A=$(tokimeki submit ./preprocess.sh | awk '/Job/{print $2}')
tokimeki submit --after $JOB_A ./train.sh
```

Multiple deps, comma-separated:

```bash
tokimeki submit --after $JOB_A,$JOB_B ./aggregate.sh
```

**Cascade behavior**: if any dep ends in `failed`, `killed`, or `crashed`, the dependent job is automatically marked `failed` and removed from the queue. Use this to short-circuit pipelines.

### Priority (`--priority`)

Higher number = scheduled sooner, among eligible jobs:

```bash
tokimeki submit --priority 10 -c "urgent.sh"
tokimeki submit -c "background.sh"               # priority=0 default
```

Tiebreak among equal priority is FIFO (submission order).

### Resource hints (`--cpus`, `--mem-mb`)

Mark resource needs:

```bash
tokimeki submit --cpus 8 --mem-mb 16384 ./big.sh
```

A runner with `--capacity-cpus 4` will skip this job. Useful for heterogeneous clusters where some nodes are big and some small.

**Both hints and capacity are advisory**: nothing enforces actual usage. They only affect scheduling decisions.

### Retries (`--retries`, `--backoff`)

Auto-retry on `failed` / `crashed` (not on `killed` — that's user intent):

```bash
tokimeki submit --retries 3 --backoff 30s ./flaky.sh
```

Each retry creates a new job ID with `orig_job` set to the original. View the chain via `events --job <orig>` (events for an original job currently — retries get new IDs; we may add a chain field later).

`--backoff` accepts duration strings: `30s`, `5m`, `1h`. The retry sits in the queue with a `not_before` time, blocking dequeue until elapsed.

### Burst scheduling

For jobs that must run uninterrupted within a runner's `--manner-period`:

```bash
tokimeki submit --burst 30m ./short-burst.sh
```

The CLI refuses to submit if no runner has at least 30 minutes of remaining lifetime. Use this for jobs that can't checkpoint and resume.

---

## JSON output

Stable JSON for scripting:

```bash
tokimeki ps --json
tokimeki runners --json
tokimeki job abc12345 --json
tokimeki events --json
```

Field names follow the docs at `docs/SPEC.md` and the Go types in `engine/protocol/types.go`. New fields may be added; existing ones are stable.

Example: count running jobs per worker:

```bash
tokimeki ps --json | jq 'group_by(.runner) | map({runner: .[0].runner, count: length})'
```

---

## Event log

Every state transition is appended to `<base>/shared/events.ndjson`:

```bash
tokimeki events                          # all events from start
tokimeki events -f                       # follow new ones
tokimeki events --since 5m               # last 5 minutes
tokimeki events --since 2026-05-10T00:00:00Z
tokimeki events --job abc12345           # one job's history
tokimeki events --json                   # raw NDJSON
```

Event kinds: `submit`, `start`, `finish`, `killed`, `crashed`, `cancel`, `runner_start`, `runner_exit`.

**NFS caveat**: the log uses `O_APPEND` writes ≤ 4 KB. Atomic on POSIX/NFSv4. On NFSv3 with concurrent writers you may see interleaved lines — the reader skips unparseable lines and continues.

---

## Shell completion

Cobra auto-generates completion. Install once:

```bash
# bash
tokimeki completion bash > /etc/bash_completion.d/tokimeki
# zsh
tokimeki completion zsh > "${fpath[1]}/_tokimeki"
# fish
tokimeki completion fish > ~/.config/fish/completions/tokimeki.fish
```

Then `<tab>` completes:

- worker IDs after `kill`, `exec`, `submit -r -w`
- job IDs after `cancel`, `kill`, `logs`, `job`

---

## Environment and configuration

| Variable | Purpose | Default |
|---|---|---|
| `TOKIMEKI_HOME` | Base directory for all state | `~/.tokimeki` |
| `USER` | Recorded as `user` on submitted jobs (for future fair-share) | (empty if unset) |

The `--base` flag overrides `TOKIMEKI_HOME`.

State layout under `<base>/`:

```
shared/
  queue.json           # global queue
  events.ndjson        # append-only event log
  reservations/        # per-job reservation records
  .global-lock         # exclusive lock during queue mutations
workers/<id>/
  info.json            # registration metadata
  heartbeat            # unix-nanos timestamp
  current.json         # currently running job, if any
  inbox/  outbox/      # request/response FIFOs (files)
  exec/<sid>/          # interactive exec session FIFOs
jobs/<id>/
  meta.json            # job metadata
  script.sh
  stdout.log  stderr.log
tmp/                   # atomic-write staging
```

You don't normally interact with these directly — but knowing they exist makes `gc`, debugging, and stuck-state recovery easier.
