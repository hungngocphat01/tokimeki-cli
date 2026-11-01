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
  - [Wait for a job](#wait-for-a-job)
  - [Logs and history](#logs-and-history)
  - [Cleanup](#cleanup)
  - [Live debugging on a runner](#live-debugging-on-a-runner)
  - [Resource usage across all runners (`tok stats`)](#resource-usage-across-all-runners-tok-stats)
- [Scheduling features](#scheduling-features)
  - [Dependencies (`--after`)](#dependencies---after)
  - [Priority (`--priority`)](#priority---priority)
  - [Resource hints (`--cpus`, `--mem-mb`)](#resource-hints---cpus---mem-mb)
  - [Retries (`--retries`, `--backoff`)](#retries---retries---backoff)
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

### Short alias (`tok`)

Symlink the binary to a shorter name on your `$PATH`:

```bash
ln -s "$(which tokimeki)" ~/.local/bin/tok
# or
sudo ln -s "$(which tokimeki)" /usr/local/bin/tok
```

`tok` and `tokimeki` are the same binary. Examples in this doc use `tok`.

### Colors

CLI output is colorized when stdout is a TTY. Disable with:

```bash
NO_COLOR=1 tok ps
```

Pipes / redirects / non-interactive sessions automatically skip ANSI colors and box-drawing characters.

### Timezone

Timestamps in `tok ps` and `tok runners` (the `STARTED` columns) are formatted in your local timezone. Override with the standard `TZ` env var:

```bash
TZ=Asia/Tokyo tok ps          # JST (GMT+9)
TZ=America/New_York tok ps    # ET
TZ=UTC tok ps                 # back to UTC
```

Persist it for the shell session:

```bash
export TZ=Asia/Tokyo
```

Or system-wide via `/etc/timezone` (Linux) — Go's `time.Local` reads it.

JSON output (`--json`) keeps RFC3339 / UTC for machine consumers; only the human-readable tables convert.

---

## Quick reference

| Command | One-liner |
|---|---|
| `runner` | Start the daemon on this node |
| `submit` | Queue scripts or an inline command; supports `--live`, `--wait`, `--quiet`, `--json` |
| `ps` | List running/queued jobs |
| `runners` | List runners and heartbeat status |
| `ls` | `ps` + `runners` together |
| `top` | One-shot dashboard combining runners + jobs + recent jobs (use `watch -n 3 tok top` for auto-refresh) |
| `stats` | Snapshot of CPU/RAM/GPU usage across all running jobs |
| `tui` | Interactive active jobs, live logs, and resource usage |
| `job <id>` | Show full job metadata + script |
| `logs <id>` | Print stdout/stderr (with `--follow`, `--tail`) |
| `watch <id>` | Block until terminal state; exit code = job's exit code |
| `events` | Stream the shared event log |
| `kill <job_id>` | Send signal to a running job (auto-resolves runner) |
| `cancel` | Remove a queued job |
| `exec` | Run an ad-hoc command on a runner |
| `kp <node>` | Run `killall python` on a runner |
| `gc` | Garbage-collect old jobs and dead workers |
| `version` | Build/version info |
| `completion` | Generate shell completion |

Every command accepts `--base PATH` to override `TOKIMEKI_HOME`.

### Interactive dashboard (`tok tui`)

`tokimeki tui` opens three columns: features, items, and details. Jobs mirrors
`ps` and shows running and queued jobs. Its detail pane shows submission time
and elapsed time: waiting time for queued jobs, runtime for running jobs.
History mirrors `ps -a`: all jobs in ascending submission time, with the newest
at the bottom.
Runners lists live daemons. Its default details view shows runner names in the
list and heartbeat, current-job state, TTL, and resources in the detail pane.
Press `t` to switch to a full-width stats view with each runner's job, CPU,
RSS, GPU, GPU memory, process count, and sample age; press `t` again to return.
Activity shows recent events and
Enter opens the related job or runner. Job rows use the same status colors as
`ps`: cyan running, yellow queued, green completed, red failed, magenta
killed/crashed, and gray stopped.

The bottom help bar changes with the selected view. Use Up/Down to select a
feature or item, Left/Right to move focus, `v` to switch between upper and
lower detail panes, Page Up/Page Down to scroll faster, and End to follow logs.
On a running job, `k` sends SIGTERM and `p` runs `killall python` on its runner.
On a queued job, `c` cancels it. These actions ask for `y` confirmation. Press
`s` to toggle a syntax-highlighted view of the original Bash script, `r` to
refresh, or `q` to quit. The view refreshes every second. Resource values come
from the runner's `stats.json` sample; GPU usage needs Linux and NVIDIA tooling
on the runner.

---

## Commands by use case

### Run a runner

```bash
tokimeki runner --id w-gpu01 --poll 2s --manner-period 1h --ttl 6d23h
```

Flags:

- `--id`: explicit worker ID (default: `<hostname>-<4 hex>`)
- `--poll`: how often the daemon scans the inbox / queue
- `--manner-period`: exit cleanly after being jobless this long. `0` disables.
- `--ttl`: optional expected runner lifetime in whole hours and days, such as `7d`, `1h`, `6d23h`, or `25h`. This is tracking metadata; it does not stop the runner or change the manner period.
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
tokimeki submit preprocess.sh train.sh
```

Each script creates a separate job. `-w` targets only the next script; scripts
without a preceding `-w` go to the general queue. Repeating the same worker is
valid:

```bash
tokimeki submit -w node1 preprocess.sh train.sh
tokimeki submit -w node1 preprocess.sh -w node2 train.sh
tokimeki submit -w node1 preprocess.sh -w node1 train.sh
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

#### Output modes (`--quiet`, `--json`, `--wait`, `--live`)

For shell scripts and AI agents:

```bash
# Just the job ID, nothing else
JOB=$(tok submit --quiet -c "python train.py")

# JSON envelope: {"job_id":"...","position":3,"worker":"...","worker_acked":false}
tok submit --json -c "python train.py" | jq .job_id

# Submit, then block until done — exit code propagates from the job
tok submit --wait -c "python train.py"
```

`--quiet` and `--json` are mutually exclusive. `--wait` can be combined with either.

Watch a submission live:

```bash
tokimeki submit -l script.sh
tokimeki submit -l -w node1 script.sh
```

`--live` (`-l`) reports queue position and worker acknowledgment, then shows
the assigned runner and streams the complete job output to stdout and stderr.
It prints a colored success or failure line on terminals when the job ends. Ctrl-C stops
watching without canceling the submitted job. With multiple scripts, all jobs
are submitted first, then their logs are streamed in submission order.
`--live` cannot be combined with `--quiet`, `--json`, or `--resubmit`.

### Inspect state

Active jobs:

```bash
tokimeki ps                # running + queued
tokimeki ps -a             # include finished
tokimeki ps -w w-gpu01     # filter by worker
tokimeki ps --json | jq    # machine-readable
```

With `-a`, active jobs appear after historical jobs so they remain visible at
the end of long terminal output.

The `COMMAND` column collapses long absolute paths to the script's basename when it can — `bash /home/.../train_llama.sh` shows as `train_llama.sh`. Recognized launchers: `bash`, `sh`, `zsh`, `python[2/3]`, `node`, `deno`, `bun`, `ruby`, `perl`, `lua`, `Rscript`. Other commands are shown verbatim (truncated to 40 chars).

A `PRIO` column appears only when at least one visible job has a non-zero priority; otherwise it's hidden to reduce noise. Sort within each status group is `priority desc, submitted_at asc`.

Runners:

```bash
tokimeki runners            # only running
tokimeki runners -a         # include stopped
tokimeki runners --json
```

The `TTL` and `REMAINING` columns show the expected lifetime and time left since the runner started. Durations are displayed as days and hours (`25h` becomes `1d 1h`). A runner without TTL shows `-`; a stopped runner has no remaining time estimate. JSON includes `ttl_hours` and, for a running runner with TTL, `remaining_secs`.

A specific job:

```bash
tokimeki job abc12345           # human form (meta + script)
tokimeki job abc12345 --json    # meta only
```

Combined dashboard (one-shot):

```bash
tok top                      # print runners + jobs + recent, then exit
tok top --plain              # no box borders, plain sections (good for piping)
watch -n 3 tok top           # auto-refresh via watch(1) — no flicker
```

Three panels:

- **RUNNERS** — live workers + heartbeat age
- **JOBS** — running + queued (sorted by priority then FIFO)
- **RECENT** — last 8 jobs that finished within the past 10 minutes, with exit code

`tok top` is intentionally one-shot. `watch(1)` already does double-buffered redraw without flicker, so we don't reimplement an in-process loop. When stdout is not a TTY (pipe / redirect / agent), borders and colors are skipped automatically.

### Control running jobs

Cancel a queued (not yet running) job:

```bash
tokimeki cancel abc12345
```

Send a signal to a running job:

```bash
tok kill abc12345                  # SIGTERM (15) — runner resolved from meta
tok kill abc12345 --signal 9       # SIGKILL
tok kill abc12345 --worker w-gpu01 # explicit worker (rare; only if meta is corrupt)
```

The owning runner is looked up from the job's `meta.json` automatically — no need to remember which worker is running it. Use `tok cancel <job_id>` for queued jobs that haven't started yet.

### Wait for a job

```bash
tok watch <job_id>            # block until terminal; exits with job's exit code
tok watch <job_id> --quiet    # suppress progress lines; only final status
tok watch <job_id> --timeout 5m  # give up after 5 minutes (exit non-zero)
```

Use this in CI / agent scripts that need to make a decision based on a job's outcome:

```bash
JOB=$(tok submit --quiet -c "./build.sh")
if tok watch "$JOB" --quiet; then
  echo "build ok"
else
  echo "build failed (exit $?)"
fi
```

### Logs and history

Print logs:

```bash
tokimeki logs abc12345                  # last 100 lines of stdout and stderr
tokimeki logs -1                        # most recently submitted job
tokimeki logs -2                        # second most recently submitted job
tokimeki logs abc12345 --stdout         # stdout only
tokimeki logs abc12345 --stderr         # stderr only
tokimeki logs abc12345 -n 50            # last 50 lines
tokimeki logs abc12345 -a               # full logs
tokimeki logs abc12345 -p               # full logs in less (or $PAGER)
tokimeki logs abc12345 -f               # follow until job finishes
tokimeki logs abc12345 -n 50 -f         # like `tail -f`
tokimeki logs -1 | grep ERROR           # search both stdout and stderr
```

The line limit applies to each stream. Both streams are written to stdout so
pipes such as `grep` see both. Logs persist after the job finishes (until `gc`
removes them).

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
tokimeki kp w-gpu01                 # same as: tokimeki exec w-gpu01 "killall python"
```

Run interactively (line-buffered, **no PTY** — works for REPLs/shells, not vim/htop):

```bash
tokimeki exec w-gpu01 -i bash
tokimeki exec w-gpu01 -i python
tokimeki exec w-gpu01 -i psql -d mydb
```

Use `Ctrl-D` to close stdin and exit. For full-screen TUIs, use your cluster's interactive job mechanism (`qsub -I`, `srun --pty`) instead.

### Resource usage across all runners (`tok stats`)

```bash
tok stats          # snapshot table
tok stats --json   # machine-readable
```

Example output:

```
RUNNER    HOST          JOB       PID      CPU%  RSS    GPU%  GPU_MEM  PROCS  AGE
trainer1  spcc-a100g09  9a1cb1c6  1196193  450   12.3G  87%   18.0G    24     1s
trainer2  spcc-a100g01  32ce852e  1910257  102   3.4G   45%   5.0G     8      <1s
trainer3  spcc-a100g01  5ff7f693  2940210  380   8.7G   92%   15.0G    22     2s
```

How it works:

- Each runner samples its own running child process every tick (~1s) — read from `/proc/<pid>/stat` (CPU + threads), `/proc/<pid>/status` (RSS), and `nvidia-smi` (GPU mem + per-process SM utilization).
- Sample is written to `workers/<id>/stats.json` via atomic rename.
- `tok stats` just reads those files from shared FS — no SSH, no RPC.
- The `AGE` column is the staleness of the sample. Anything ≤ a few seconds is current.

What you can use this for:

- Check whether jobs are CPU-bound (CPU% pinned at `cores × 100`) vs GPU-bound (high `GPU%`) vs idle (low both).
- Spot under-utilized GPU runs (e.g. `GPU% = 20%` when it should be ≥ 80% — likely dataloader bottleneck).
- See which runner is RAM-pressured before OOM hits.

CPU% and GPU% cells are colored by tier so under-utilization stands out:

| Metric | 🟢 green | 🟡 yellow | 🔴 red |
|---|---|---|---|
| GPU% (SM utilization) | ≥ 80 | 40–79 | < 40 |
| CPU% (sum across cores) | ≥ 200 | 50–199 | < 50 |

Caveats:

- Linux + NVIDIA only. On macOS / non-NVIDIA hosts, GPU columns are `n/a` and CPU/RSS are empty.
- CPU% sums across cores: a 16-thread job on 16 cores reports `1600%`, like `top -1`.
- Sampling tree includes descendants — a Python parent that spawns CUDA workers is reported as one bundle.
- Idle runners don't show up; only currently-running jobs.

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

---

## JSON output

Stable JSON for scripting:

```bash
tok ps --json
tok runners --json
tok job abc12345 --json
tok events --json
tok submit --json -c "..."   # {job_id, position, worker, worker_acked}
tok stats --json             # array of {worker_id, job_id, pid, cpu_percent, rss_bytes, gpu_util_percent, gpu_mem_bytes, ...}
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

- worker IDs after `exec`, `submit -r -w`, `kill --worker`
- job IDs after `cancel`, `kill`, `logs`, `job`

---

## Environment and configuration

| Variable | Purpose | Default |
|---|---|---|
| `TOKIMEKI_HOME` | Base directory for all state | `~/.tokimeki` |
| `TZ` | Timezone for `STARTED` columns in `tok ps` / `tok runners` (per-process; doesn't affect other users or system clock) | system default |
| `USER` | Recorded as `user` on submitted jobs (for future fair-share) | (empty if unset) |
| `NO_COLOR` | Disable ANSI color even on TTY | unset |

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
