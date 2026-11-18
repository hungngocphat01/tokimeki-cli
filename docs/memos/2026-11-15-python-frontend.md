# Memo: engine-as-core, sub-jobs, and the Python front-end

Started: 2026-11-15

| Phase | Scope | Status |
|---|---|---|
| 1 | Engine = core (typed data, raw units, coded errors, no printing); CLI = shim; `--json` everywhere | **Done**, released v0.3.0 (2026-11-18) |
| 2 | Coordinator jobs, groups, multi-slot runners (this memo's design) | **Design agreed 2026-11-18**; next: plan + implement in engine and CLI |
| 3 | Python package: thin shim for training scripts | Deferred until phase 2 lands |

## Motivation

Current ML workflow (the `evident` project, `/Volumes/s2510159/source/evident`):

1. `scripts/sweep_expand.py` expands a grid YAML (`sweep.grid` / `sweep.list`)
   into concrete runs with deterministic `run_id`s, deals them **round-robin
   into fixed shards** (`configs/.../tmp/<exp>/configs/worker-NNN.yaml`) and
   writes `manifest.jsonl`.
2. One batch script per shard (`sh/run_cifar100_redl_worker_000.sh`) runs
   `scripts/train.py --config worker-NNN.yaml --resume`, which runs the shard's
   configs **sequentially**.
3. `scripts/sweep_merge.py --manifest` joins every
   `runs/<exp>/runs/<run_id>/logs/test_results.csv` into one table.

To keep a GPU busy, several trainings share it (the HPO scripts use
`SLOTS=3` on A100 hosts, 2 elsewhere).

**Problem:** shards are fixed before anything runs. Submit two shards; when one
finishes first, its runner idles while the other still has configs left.

Each config is already a good unit of work: stable `run_id`, resumable
(`--resume`, `ckpt/last.pt`), results at a known path. The fix is to make each
config its own job that any free slot on any runner can take.

Out of scope: the Optuna HPO flow (`hpo_worker.py`); its trial processes pull
from a shared study and already balance themselves.

## Phase 2 design: coordinators, groups, multi-slot runners

### Job kinds

| Kind | What it is | Where it runs |
|---|---|---|
| `compute` (default) | An ordinary job, as today | One of a runner's compute slots |
| `coordinator` | A script that does no compute: submits compute jobs, polls its group, combines results (e.g. the training driver) | A runner's single coordinator slot |

- Submit: `tok submit -C/--coordinator script.sh` (`-c` stays `--command`). Without `-C` a job is compute.
- A coordinator can run on any runner; its children run wherever a compute
  slot is free. Neither cares where the other runs.

### Runners

```
tok runner --compute-threads 3
├─ coordinator slot: at most 1 coordinator   (not counted in --compute-threads)
└─ compute slots:    up to 3 compute jobs, from any coordinator or the CLI
```

- `--compute-threads` is per runner (e.g. 3 on A100 nodes, 2 elsewhere).
  Default 1, so a plain `tok runner` behaves like today plus a coordinator slot.
- Example: 3 nodes, 4 coordinators submitted. Coordinators 1–3 start, one per
  node; coordinator 4 stays queued until one of them exits. Compute jobs from
  all running coordinators fill all 9 compute slots.
- **Implementation (chosen: approach A).** One daemon process with one
  coordinator slot and N compute slots, each slot its own executor. Each tick
  fills free slots from the queue. `current.json` becomes one file per running
  job. `tok runners` still shows one row per node.
  - Rejected B (one runner process per slot): cheap on the executor, but
    `tok runners` shows 4 rows per node, and heartbeats, TTL, manner period,
    kill and stats sampling get duplicated per slot.

### Groups

- Any job can carry `-g/--group <name>`. A group is a **plain label managed by
  the user**: there is no `group create`, and a group is "all jobs on disk with
  that label". Finished jobs stay in it until `tok gc` removes them, so reusing
  a name means either running `tok gc` first or choosing a fresh one (e.g.
  `c100-redl-1118`).
- Jobs a coordinator submits inherit its group (and record it as parent)
  through the environment, so the training script needs no `-g`. A
  coordinator submitted without `-g` uses its own job ID as its group.

### Coordinator lifecycle

- **Runner expires or the coordinator crashes:** the engine does nothing
  special. Children keep running; queued children still start. The
  coordinator is an ordinary failed/crashed job; its own `--retries` applies.
- **`tok kill` / `tok cancel` on a coordinator:** also stops its group — queued
  children are cancelled and running children are killed.
- **No dedup in the engine.** Before submitting, user code reads the group's
  state and skips configs that already have a job (matching e.g. `run_id` in
  `--desc`). This is how a retried coordinator re-attaches.

### `tok group <id>` (new read path)

- Human output: the `ps` table filtered to the group, with a summary on top
  (coordinators running, job counts by status, failed list).
- `--json`: everything — counts by status (coordinators and compute separately),
  an `active` count (queued + running), the failed list, and every job (the
  same `Job` type `ps --json` uses).
- Typical coordinator loop (user code; engine sets no interval):
  ```
  expand grid → for each config without a job in the group: submit
  every 60 s: tok group <g> --json → when active == 0: merge results, exit
  ```

### Debug logging

- Engine gets debug logs (dequeue decisions, slot fills, kill cascades, group
  reads, lock waits). They are written only when the front-end enables them;
  the engine still never prints on its own.
- CLI: global `--verbose` turns them on for any command, including `tok runner`.

### `evident` under phase 2

```
tok submit -C -g c100-redl sh/sweep_c100_redl.sh
  └─ coordinator: python scripts/sweep.py --config configs/.../grid.yaml
       1. expand grid → one concrete config per run_id   (as today)
       2. for each run_id not yet in the group:
            tok submit --desc <run_id> -- train.py --config <run>.yaml --resume
       3. poll `tok group c100-redl --json` every minute until active == 0
       4. merge test_results.csv                          (sweep_merge, unchanged)
runners: tok runner --compute-threads 3   (A100)   /   --compute-threads 2
```

Sharding into `worker-NNN.yaml` goes away. Until phase 3, the coordinator
calls `tok ... --json` via subprocess; the Python package later wraps the
same calls.

### Calls made by Claude (not discussed explicitly; correct if wrong)

1. **Group = the existing `name` field.** `-g/--group` writes `meta.json`'s
   `name` (so old jobs' tags read as groups); `-n/--name` stays as an alias.
   `tok group` matches exactly; `ps --name` keeps its substring filter.
2. **New `meta.json` fields** (all `omitempty`): `kind` (`"coordinator"`;
   absent = compute) and `parent` (submitting coordinator's job ID).
   `info.json` gains `compute_threads`.
3. **Job environment.** The runner exports `TOKIMEKI_HOME`, `TOKIMEKI_JOB_ID`,
   `TOKIMEKI_GROUP` (and `TOKIMEKI_JOB_KIND`) to every job. `tok submit` uses
   `TOKIMEKI_GROUP` as the default group and `TOKIMEKI_JOB_ID` as `parent`
   when they are set.
4. **Kill/cancel cascade is by group**, not by parent, so it still reaches
   children submitted by an earlier attempt of a retried coordinator. It runs
   in the engine `client` and returns what it cancelled/killed; it covers
   every active job in the group, including ones submitted from the CLI.
   Order: stop the coordinator first (so it can't submit more), then cancel
   queued children, then kill running ones.
5. **Children on a dead runner.** A child whose runner died stays `running`
   on disk (effective status `stopped`) until that runner ID restarts. The
   group report counts `stopped` separately and does **not** include it in
   `active`, so a coordinator never waits forever; user code decides whether
   to resubmit. The engine does not reap them.
6. **Scheduling across groups** stays as today: priority, then FIFO. A group
   submitted first drains first.
7. **Per-job running state:** `workers/<id>/running/<job_id>.json`
   (job ID, PID, kind, start time). Crash recovery already marks every job
   assigned to the runner as crashed; it also clears a legacy `current.json`.
8. **Stats:** `stats.json` keeps its top-level fields as the node aggregate
   and gains a per-job list; `tok stats`/`top` show one row per running job.
9. **`--cpus/--mem` capacity** is checked against the runner's capacity minus
   what its running jobs already reserve.
10. **Mixed versions:** old runners don't know job kinds and would run a
    coordinator in their single slot. A cluster upgrades all runners
    together. Release as v0.4.0 in both repos.

### Testing

- Unit and integration tests in both repos (multi-slot dequeue under the lock,
  coordinator slot limit, group report, kill cascade, recovery with several
  running jobs).
- Throwaway Docker cluster (shared volume, several runners with different
  `--compute-threads`, `--verbose` logs), cleaned up afterwards. Scenarios:
  - 3 runners, 4 coordinators: 4th waits; compute fills all slots.
  - Kill a coordinator: queued children cancelled, running children killed.
  - Coordinator crash + `--retries`: children unaffected; retried coordinator
    re-attaches using the group report and submits only missing configs.
  - Runner container killed mid-child: child shows `stopped`, `active`
    excludes it, coordinator finishes.
  - Runner restart with several running jobs: all marked crashed, retries fire.
  - `tok gc` then reuse of the group name.

## Phase 3: Python package (deferred)

- Thin shim for training scripts: submit, group status, wait, cancel. No
  protocol or registry logic of its own.
- Binding: (a) cgo shared library + ctypes, or (b) exec the `tokimeki` binary
  with `--json`. Leaning (b): fork-safe with `multiprocessing`, one static
  binary, same schema as the CLI. Decide when phase 3 starts.

## Log

- 2026-11-15 — Scoped phase 1; memo created.
- 2026-11-15 — Rescoped: phase 1 is the Go refactor only; Python deferred.
  Machine output = `--json` on regular commands (no `api` command).
- 2026-11-15 — Phase 1 implemented and verified side by side with the
  pre-refactor binary (local and two-node Docker).
- 2026-11-18 — Released v0.3.0 in lockstep (engine and CLI).
- 2026-11-18 — Phase 2 designed with the user: coordinator/compute kinds,
  one coordinator slot + `--compute-threads` per runner (single daemon, multi
  slot), plain-label groups, `tok group` read path, cascade on kill/cancel,
  no engine handling of coordinator crashes, `--verbose` debug logs. Sub-jobs
  ahead of the Python package.
- 2026-11-18 — Design approved. `--coordinator` short flag is `-C` (`-c` keeps `--command`). Plan: `tokimeki-engine/docs/superpowers/plans/2026-11-18-coordinators-groups.md`.
- 2026-11-18 — Phase 2 implemented on `feat/coordinators` (engine + CLI). Docker
  cluster test (3 runners: `--compute-threads` 3/2/2, shared volume,
  `--verbose`), all PASS: 4 coordinators on 3 runners (≤1 coordinator per
  runner, compute ≤ threads, all 7 slots used, 4th waits); kill coordinator
  (4 queued cancelled, 6 running killed, other group untouched); coordinator
  `--retries` re-attaches (6 distinct descs, children from both attempts);
  `docker kill` a runner mid-child (2 `stopped`, not active, coordinator
  finishes; restart → 2 crashed + retried to completion); `gc` then reuse a
  group name; per-job stats (3 busy jobs, 3 samples); stress 3×40 short jobs
  (120 starts, no double runs, no leaks). Found and fixed: cascade must cancel
  queued before killing running; TUI kill bypassed the cascade.
