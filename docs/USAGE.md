# Tokimeki — Usage Guide

What you can do with the `tokimeki` CLI, and how. For what Tokimeki is, see
the [README](../README.md); for how jobs, runners and groups fit together, see
[HOW-IT-WORKS.md](HOW-IT-WORKS.md). `tokimeki <command> --help` lists every
flag of a command.

## Contents

- [Setup](#setup)
- [Commands at a glance](#commands-at-a-glance)
- [Runners](#runners)
- [Submitting jobs](#submitting-jobs)
- [Coordinators and groups](#coordinators-and-groups)
- [Following jobs](#following-jobs)
- [The dashboard (`tok tui`)](#the-dashboard-tok-tui)
- [Stopping jobs](#stopping-jobs)
- [Working on a runner (`tok shell`, `tok exec`)](#working-on-a-runner-tok-shell-tok-exec)
- [Cleanup](#cleanup)
- [Scripting with `--json`](#scripting-with---json)
- [Job environment](#job-environment)
- [Jobs from older versions](#jobs-from-older-versions)

---

## Setup

Point every node at the same state directory on the shared filesystem:

```bash
export TOKIMEKI_HOME=/shared-fs/tokimeki   # default ~/.tokimeki; --base PATH overrides it per command
```

The binary can be linked to a shorter name; `tok` and `tokimeki` are the same
program, and the examples below use `tok`:

```bash
ln -s "$(which tokimeki)" ~/.local/bin/tok
```

Shell completion (completes runner IDs and job IDs):

```bash
tok completion bash > /etc/bash_completion.d/tokimeki
tok completion zsh > "${fpath[1]}/_tokimeki"
tok completion fish > ~/.config/fish/completions/tokimeki.fish
```

Tables are coloured on a terminal (`NO_COLOR=1` turns that off) and show local
time (set `TZ` to change it). `--verbose` prints the engine's debug log on
stderr, e.g. `tok --verbose runner` to see scheduling decisions as they happen.

---

## Commands at a glance

| Command | What it does |
|---|---|
| `runner` | Start a runner on this node |
| `runners` | List runners, their busy slots (`BUSY 2/3`) and liveness |
| `submit` | Queue scripts or an inline command |
| `ps` / `ls` | List running and queued jobs (`ls` adds runners) |
| `group <name>` | A group's progress and jobs; `--wait` blocks until it is done |
| `job <id>` | A job's details and script |
| `logs <id>` | A job's stdout and stderr |
| `watch <id>` | Wait for a job; exits with the job's exit code |
| `result <id>` | The value a Python job returned |
| `tui` | Interactive dashboard of jobs, runners, logs and resources |
| `top` | One-shot summary of runners, jobs and recently finished jobs |
| `stats` | CPU, memory and GPU use of running jobs |
| `events` | The shared event log |
| `cancel <id>` | Remove a queued job |
| `kill <id>` | Signal a running job |
| `shell <runner>` | Open a terminal on a runner's node |
| `exec <runner> <cmd>` | Run a one-off command on a runner's node |
| `kp <runner>` | `killall python` on a runner's node |
| `gc` | Remove old jobs and dead runners |
| `version` | Build information |

---

## Runners

Start one runner per allocation, usually from your PBS/SLURM job script:

```bash
tok runner --id gpu01 --compute-threads 2
```

- `--id`: the runner's name (default `<hostname>-<4 hex>`). Restarting a
  runner with the same ID lets it clean up jobs left behind by the previous
  one.
- `--compute-threads N`: how many jobs run at once (default 1). Every runner
  also has one coordinator slot on top of these.
- `--manner-period`: exit after being idle this long (default `1h`, `0`
  never), handing the node back to the scheduler.
- `--ttl 6d23h`: the allocation's expected lifetime, shown in `tok runners`.
  It is only a note; it does not stop the runner.
- `--capacity-cpus`, `--capacity-mem-mb`: skip jobs whose `--cpus` / `--mem-mb`
  don't fit. Leave them unset unless runners differ in size or share a node.

```bash
tok runners        # live runners
tok runners -a     # include stopped ones
```

---

## Submitting jobs

```bash
tok submit train.sh                  # one job
tok submit prep.sh train.sh          # one job per script
tok submit -c "python train.py --lr 0.01"
```

The script is copied when you submit it and runs in the job's own folder, so
`cd` to your project at its top. `tok submit` prints the job ID; the job waits
in the shared queue until a runner has room, so you can submit before any
runner is up.

Useful flags:

| Flag | Effect |
|---|---|
| `-w RUNNER` | Run only on that runner (applies to the next script) |
| `-r -w RUNNER ID` | Resubmit a copy of job `ID` to a running runner |
| `-g NAME` | Put the job in a group |
| `-C` | Submit as a coordinator (see below) |
| `--after ID,ID` | Start only after these jobs completed; if one fails, this one fails too |
| `--priority N` | Higher runs sooner (default 0) |
| `--retries N --backoff 30s` | Rerun on failure or crash, as a new job (a killed job is never retried) |
| `--cpus N --mem-mb M` | Resource hints, checked against runners' capacity |
| `--desc TEXT` | A free-text note, shown with the job |

How `submit` reports back:

```bash
tok submit -l train.sh          # live: show where it runs and stream its output (Ctrl-C stops watching only)
tok submit --wait train.sh      # block until it finishes; exit with its exit code
JOB=$(tok submit -q train.sh)   # print only the job ID
```

`--from-json FILE` (or `-` for stdin) takes the whole request as one JSON
object, for programs; the [Python package](https://github.com/hungngocphat01/tokimeki-py)
submits its jobs this way. A script job looks like
`{"abi": 2, "type": "bash", "script_file": "train.sh", "group": "g"}`; the
other fields have the same names as the flags, and flags given on the command
line win.

---

## Coordinators and groups

A **group** is a label on related jobs (`-g NAME`). A **coordinator**
(`tok submit -C`) is a job that submits other jobs, usually one per trial of a
sweep, then waits for them and summarises. It runs in its runner's coordinator
slot, and the jobs it submits join its group automatically. The
[Python package's README](https://github.com/hungngocphat01/tokimeki-py#running-a-hyperparameter-sweep)
walks through a full sweep.

A coordinator can also be a plain shell script:

```bash
#!/bin/bash
# sweep_alexnet.sh
set -e
cd /shared/projects/model_trainer
for lr in 0.01 0.001 0.0001; do
    tok submit -c "cd /shared/projects/model_trainer && python train.py --backbone alexnet --lr $lr"
done
tok group "$TOKIMEKI_GROUP" --wait
python summarize.py --backbone alexnet > results/alexnet-summary.csv
```

```bash
tok submit -C -g alexnet-lr-01 sweep_alexnet.sh
```

Following a group:

```bash
tok group alexnet-lr-01          # progress counts, failed and stopped jobs, then the job list
tok group alexnet-lr-01 --wait   # block until nothing in the group is queued, running or retrying
tok group alexnet-lr-01 --json   # the same report for scripts
```

- A group is every job carrying the name until `tok gc` removes them, so use
  a fresh name for each sweep.
- `--wait` ignores the job calling it, so a coordinator can wait on its own
  group. Jobs on a runner that died count as stopped, not active, so check
  that every trial produced a result before summarising.
- `tok kill` or `tok cancel` on a coordinator stops its whole group.
- If a coordinator dies, its trials keep running. A retried coordinator
  should check `tok group --json` and submit only what is missing; Tokimeki
  does not deduplicate.

---

## Following jobs

```bash
tok ps                 # running and queued jobs
tok ps -a              # all jobs, finished ones first
tok ps -w gpu01        # one runner's jobs
tok job 3f2a9c01       # details and the script
tok watch 3f2a9c01     # wait; exits with the job's exit code (--timeout 5m to give up)
tok result 3f2a9c01    # the value a Python job returned
```

Logs are kept until `tok gc` removes the job:

```bash
tok logs 3f2a9c01      # last 100 lines of stdout and stderr
tok logs -1            # the job you submitted last (-2, -3, ... for earlier ones)
tok logs 3f2a9c01 -f   # follow until the job ends
tok logs 3f2a9c01 -a   # everything (-p opens it in a pager)
```

A quick look at the whole cluster:

```bash
tok top                # runners, jobs, and jobs finished in the last 10 minutes
watch -n 3 tok top     # refreshed every 3 seconds
tok stats              # CPU%, memory, GPU% and GPU memory of each running job
tok events -f          # follow the event log (submits, starts, finishes, crashes)
```

`tok stats` needs Linux and NVIDIA tools on the runner for its GPU columns.

---

## The dashboard (`tok tui`)

`tok tui` is an interactive view of the cluster, refreshed every second. The
left column picks a view, the middle one lists its items, and the right one
shows the selected item:

- **Jobs**: running and queued jobs, with their live logs.
- **History**: every job, newest at the bottom.
- **Runners**: live runners with heartbeat, current jobs and resources; `t`
  switches to a table of every runner's CPU, memory and GPU use.
- **Activity**: recent events; Enter opens the job or runner.

| Key | Action |
|---|---|
| Up / Down, Left / Right | Select, move between columns |
| `v` | Switch between the upper and lower detail panes |
| Page Up / Page Down, End | Scroll; End follows the log |
| `s` | On a job: show its script. On a runner: open a shell there |
| `k` | Kill the selected running job (asks to confirm) |
| `c` | Cancel the selected queued job (asks to confirm) |
| `p` | `killall python` on the job's runner (asks to confirm) |
| `r`, `q` | Refresh, quit |

---

## Stopping jobs

```bash
tok cancel 3f2a9c01            # a queued job
tok kill 3f2a9c01              # a running job, SIGTERM
tok kill 3f2a9c01 --signal 9   # SIGKILL
tok kp gpu01                   # killall python on gpu01's node
```

You don't need to know which runner a job is on. On a coordinator, `cancel`
and `kill` also stop the rest of its group and print what they stopped.

---

## Working on a runner (`tok shell`, `tok exec`)

`tok shell` opens a full terminal on a runner's node, like `ssh` or
`srun --pty`: colours, `vim`, `htop`, Ctrl-C and resizing all work.

```bash
tok shell gpu01
```

It is the only command that uses the network: the runner's node connects back
to your machine, so it must be able to reach it. If it can't, or only some
ports are open, say where to connect:

```bash
tok shell gpu01 --advertise login01 --listen :50000
```

Run it as the user who started the runner. An open shell keeps the runner
from exiting for being idle, so close it when you are done. If the node
doesn't know your terminal type, use `TERM=xterm-256color tok shell gpu01`.

For a single command, `tok exec` needs no network at all:

```bash
tok exec gpu01 nvidia-smi
tok exec gpu01 -i python       # interactive, line by line (no terminal: fine for REPLs, not for vim)
```

---

## Cleanup

Nothing is removed automatically. Run `tok gc` from time to time:

```bash
tok gc                     # jobs finished more than 7 days ago, and dead runners
tok gc --older-than 24h
tok gc --max-size 10G      # also cap the total size, removing the oldest first
tok gc --dry-run           # show what would be removed
```

---

## Scripting with `--json`

`ps`, `runners`, `job`, `group`, `result`, `stats`, `events`, `submit`,
`watch`, `kill`, `cancel`, `exec` and `gc` accept `--json`:

```bash
tok ps --json | jq 'group_by(.worker_id) | map({runner: .[0].worker_id, jobs: length})'
tok group alexnet-lr-01 --json | jq '.compute.active'
```

- Times are Unix nanoseconds (fields ending `_ns`), durations are seconds
  (`_secs`), sizes are bytes. Fields may be added, but existing ones stay.
- `tok group --json` has the counts under `coordinators` and `compute`
  (`active`, `queued`, `running`, `completed`, `failed`, `stopped`, ...), then
  `failed`, `stopped` and `jobs` as lists of jobs.
- On failure, stdout gets only `{"error": {"code": "...", "message": "..."}}`
  and the command exits 1. Codes: `not_found`, `invalid_state`,
  `invalid_argument`, `timeout`, `runner_rejected`, `runner_not_running`,
  `internal`.
- Warnings go to stderr, as one JSON line each.

---

## Job environment

Every job runs with these variables set:

| Variable | Value |
|---|---|
| `TOKIMEKI_HOME` | The state directory |
| `TOKIMEKI_JOB_ID` | The job's ID |
| `TOKIMEKI_GROUP` | The job's group (may be empty) |
| `TOKIMEKI_JOB_KIND` | `compute` or `coordinator` |
| `TOKIMEKI_BIN` | The runner's `tokimeki` binary |

`tok submit` inside a job uses `TOKIMEKI_GROUP` as its default group, which
is how a coordinator's jobs join its group.

---

## Jobs from older versions

v0.5.0 changed how jobs are recorded. Jobs created by v0.4 and older still
list, run and resubmit, with a warning like
`warning: 3 jobs use a deprecated meta.json schema (1); upgraded to schema 2 in memory`
until they next change or `tok gc` removes them. Run the same version of
`tokimeki` on every runner and login node.
