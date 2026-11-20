# How Tokimeki works

What happens to your work, from the moment you submit it until you read its
results, and which files carry it along the way. [USAGE.md](USAGE.md) has
every command and flag; the Python package's README covers its API.

## The pieces

```
  your login node                     shared filesystem                  compute nodes
  ───────────────                     ($TOKIMEKI_HOME)                   ─────────────
  tok submit ...  ───────────────►   queue, jobs, logs   ◄─────────────  tok runner (node01)
  tok ps / group / logs ◄────────                         ◄─────────────  tok runner (node02)
```

- **The state directory** (`$TOKIMEKI_HOME`, default `~/.tokimeki`) is
  where everything lives: the queue, every job's record and logs, and the
  list of runners. Every node that submits work or runs it must see the same
  directory, e.g. a home or project directory on the cluster's shared
  filesystem.
- **A runner** (`tok runner`) is a process you start on a compute node,
  usually inside a PBS/SLURM allocation. It takes jobs from the queue and
  runs them on that node. There is no central server: runners only read
  and write the state directory, and you can start or stop them at any time.
- **The CLI** (`tok ...`) reads and writes the same directory. Submitting
  works even when no runner is up yet; the job waits in the queue.

## A job's life

```
tok submit ──► queued ──► running ──► completed     (exit code 0)
                 │            │  └──► failed        (non-zero exit; retried if --retries allow)
                 │            ├─────► killed        (tok kill)
                 │            └─────► crashed       (its runner died; seen when that runner restarts)
                 └── tok cancel removes it
```

1. **Submit.** `tok submit` stores the job and appends it to one queue that
   all runners share. You get a job ID back.
2. **Pick-up.** Every runner checks the queue every couple of seconds. A runner
   with a free slot takes the next job it may run: highest `--priority`
   first, then the oldest. A job waits while its `--after` jobs haven't
   completed, while it is pinned to another runner (`-w`), or while it needs
   more `--cpus`/`--mem-mb` than a runner has left.
3. **Run.** The runner starts the job on its node and captures its stdout and
   stderr. `tok logs` shows them, `tok ps` shows it running, and
   `tok stats` shows its CPU, memory and GPU use.
4. **Finish.** The exit code decides the status. A failed or crashed job
   with `--retries` left is queued again as a new job (after `--backoff`).
   A killed job is never retried.

If a runner dies (its allocation ended, its node failed), its running jobs
show as **stopped** in `tok ps`: nobody is running them, but nothing has
marked them finished either. When a runner with the same ID starts again, it
marks them **crashed** and retries those that allow it.

## Runners and slots

```
tok runner --compute-threads 3
├── 1 coordinator slot
└── 3 compute slots
```

- **Compute jobs** (the default) do the work: training runs, evaluations,
  scripts. A runner runs up to `--compute-threads` of them side by side, so
  several runs can share one GPU node.
- **Coordinator jobs** (`tok submit -C`) do no heavy work: they submit
  compute jobs, wait for them and combine their results. Each runner runs at
  most one, in a slot of its own, so a coordinator never takes a compute
  slot from the work it is waiting for.
- `tok runners` shows each runner's busy compute slots (`BUSY 2/3`).
- **Manner period.** A runner with nothing to do exits after
  `--manner-period` (default 1h), handing the node back to the cluster's
  scheduler.

## Groups and coordinators

A **group** is a label on jobs (`-g NAME`). It has no other existence: the
group is simply every job carrying that label, until `tok gc` removes them.

The usual pattern for a sweep:

```
tok submit -C -g sweep1 sweep.sh
   └─ coordinator: expands the grid, then for each config:
        tok submit --desc <run_id> ...        (joins group sweep1 automatically)
      tok group sweep1 --wait                 (until no job but itself is active)
      merge the results
```

- Jobs a coordinator submits join its group and record it as their parent.
- Whichever runner has a free compute slot takes the next config, so no node
  sits idle while another still has a backlog.
- `tok group NAME` shows the group's counts, its failed and stopped jobs, and
  its jobs. `tok kill` or `tok cancel` on the coordinator stops the whole
  group.
- If the coordinator itself crashes, its jobs keep running. With `--retries`
  it starts again; it should check `tok group --json` and submit only what
  is missing, since Tokimeki doesn't skip duplicates for you.

## What a job runs

| Kind | You submit | The runner runs |
|---|---|---|
| Script | `tok submit train.sh` | `train.sh`, copied at submit time (later edits don't affect the job) |
| Inline command | `tok submit -c "python train.py --lr 0.1"` | that command, in a small script |
| Python function | `group.submit(run_experiment, cfg)` from the Python package, or `tok submit --from-json` with type `python-uv` | `run_experiment(cfg)` in your uv project |

Any of these can also be given as JSON (`tok submit --from-json FILE`, or `-`
for stdin), which is how programs submit jobs.

A script runs in its job's own folder unless you set a working directory;
use absolute paths, or `cd` at the top of the script. A Python function runs
in the directory you submitted from.

Every job can see where it is: `$TOKIMEKI_JOB_ID`, `$TOKIMEKI_GROUP`,
`$TOKIMEKI_HOME` and more (see [Job environment](USAGE.md#job-environment)).
That's how `tok submit` inside a coordinator knows which group to join.

## Results

- **Logs.** Every job's stdout and stderr are kept; `tok logs <id>` shows
  them, `tok logs -1` the latest job's.
- **A result value.** A job can report one JSON value as its result
  (`tok report`); `tok result <id>` reads it back. A Python function's return
  value is reported for you.
- **Why it failed.** A job can also report what went wrong (a stage, an
  error type and a message); `tok group` shows the type next to each failed
  job.
- Your own output files (checkpoints, CSVs) are yours: write them wherever
  your code normally does.

## Cleaning up

Finished jobs stay, with their logs, until `tok gc` removes them (by default
those finished more than a week ago; see [Cleanup](USAGE.md#cleanup)). Runners
that stopped are removed from `tok runners` the same way.

## Under the hood: files and who reads them

Everything above is files in `$TOKIMEKI_HOME`. You never need to edit them,
but knowing who writes what helps when something looks stuck.

```
$TOKIMEKI_HOME/
├── shared/
│   ├── queue.json          # job IDs waiting to run, in submission order
│   ├── .global-lock        # held for a moment by whoever changes the queue or a job record
│   └── events.ndjson       # append-only log: submit, start, finish, kill, cancel (tok events)
├── workers/<runner id>/
│   ├── info.json           # host, PID, version, compute threads (written at start)
│   ├── heartbeat           # timestamp, rewritten every poll
│   ├── inbox/  outbox/     # requests to this runner (kill, status, exec) and its answers
│   ├── running/<job>.json  # one file per job it is running: PID, start time
│   └── stats.json          # CPU / memory / GPU sample per running job (tok stats)
├── jobs/<job id>/
│   ├── meta.json           # the job record: what to run, settings, status, timestamps, exit code
│   ├── script.sh           # script jobs: the script, copied at submit
│   ├── stdout.log  stderr.log
│   └── result.json  error.json   # what the job reported (tok report)
└── tmp/                    # scratch for writes (below)
```

**Who writes what.**

| Step | Who | Files |
|---|---|---|
| `tok submit` | the CLI, on your node | creates `jobs/<id>/` (`meta.json`, `script.sh`), appends the ID to `queue.json`, logs `submit` |
| pick-up | a runner, each poll (`--poll`, default 2s) | removes the ID from `queue.json`, sets `meta.json` to running, writes `running/<id>.json`, logs `start` |
| run | the runner | starts the process; it writes `stdout.log` / `stderr.log`; `stats.json` is refreshed each poll |
| `tok report` | the job (through the CLI) | `result.json` or `error.json` |
| finish | the runner | final status and exit code in `meta.json`, removes `running/<id>.json`, logs `finish`, queues a retry if allowed |
| `tok kill` | the CLI → the runner | the CLI drops a request in the runner's `inbox/`; the runner signals the job and answers in `outbox/` |
| `tok cancel` | the CLI | removes a queued job from `queue.json` and deletes its folder (no runner involved) |
| `tok gc` | the CLI | deletes old finished job folders and dead runners' folders |

**How the readers decide.** `tok ps`, `tok group` and `tok runners` only
read; they never contact a runner.

- A job's status comes from its `meta.json`.
- A runner counts as alive while its `heartbeat` is less than 30 seconds old.
  A job whose `meta.json` says running on a runner that isn't alive is shown
  as **stopped**.
- That is also why `tok kill` needs the runner alive (it has to answer its
  inbox), while `tok cancel` of a queued job doesn't.

**Safe on a shared filesystem.**

- Every file is written to `tmp/` first and then renamed into place, so a
  reader never sees half a file. The filesystem must make renames atomic,
  which local disks and NFS do.
- Changes to the queue and to job records happen while holding
  `shared/.global-lock`, so two runners never take the same job.
- Expect a few seconds of delay on NFS between one node writing a file and
  another seeing it; Tokimeki is built for that.

## Under the hood: Python jobs

The Python package (`tokimeki`, imported as `import tokimeki as tk`) is a thin layer over the CLI: each call runs one
`tok ... --json` command, and the package writes no files.

```
your coordinator (Python)                                 a runner with a free compute slot
──────────────────────────                                ─────────────────────────────────
group.submit(run_experiment, cfg)
  └─ tok submit --from-json -         ──► jobs/<id>/meta.json:
       {"type": "python-uv",                execute = {type: python-uv,
        "entry": "evident.train:run_experiment",       entry, project, cwd, arg: cfg}
        "arg": cfg, ...}
                                                       ──► uv run --project <project> --no-sync \
                                                              python -m tokimeki.call evident.train:run_experiment
                                                             1. tok job $TOKIMEKI_JOB_ID      (reads arg)
                                                             2. import evident.train; run_experiment(cfg)
                                                             3. tok report --from-json -      (return value or exception)
group.wait()        └─ tok group <g> --wait
group.result(job)   └─ tok result <id>         ◄── jobs/<id>/result.json
```

- The function is named `module:function`, or `/abs/path/script.py:function`
  for a function in the script you run. It must be importable on the
  runner's node: from your installed project, or from the job's working
  directory.
- The runner uses the `uv` on its own `PATH` and your project's existing
  `.venv` (`--no-sync`, so many jobs starting at once don't fight over one
  environment). Run `uv sync` before submitting.
- A Python job exits 0 when the function returns, 1 when it raises (the error
  is reported with stage `run`), and 2 when it couldn't be imported (stage
  `load`).
- Errors from the CLI become `tk.TokimekiError` with the CLI's error
  code; CLI warnings become `tk.TokimekiWarning`.

## Versions

All runners and every `tok` that share a state directory should run the same
version. Jobs from older versions keep working; the CLI prints a warning when
it reads them (see [Jobs from older versions](USAGE.md#jobs-from-older-versions)).
