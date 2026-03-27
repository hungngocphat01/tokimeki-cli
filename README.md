<div style="text-align: center;">
<h1>🌈 TOKIMEKI</h1>
</div>

A masterless, filesystem-based job runner.

## ❓ What is this?

Tokimeki lets you submit and manage jobs across nodes that share a common filesystem but have no direct network connectivity. All communication happens through the shared filesystem. No master node, no networking involved at all.

## ❓ Why Tokimeki?

If you work with PBS, SLURM, or similar job schedulers on a shared cluster, you've run into these situations:

1. You submitted a job, but the config is wrong. The job is already running. If you delete it and resubmit, you go back to the end of the queue, possibly waiting another day for a node. You want to replace the process running inside your allocation without giving up the slot.

2. Your task finishes early. You requested 24 hours of running time, but the computation completed in 10 hours, and the job exits. The remaining 14 hours that should have been allocated to you are wasted. You want to feed another task into the same allocation.

Tokimeki solves both. You submit a PBS/SLURM job that starts a Tokimeki Runner, then interact with that runner from any node that shares the same filesystem with the runners:

```bash
# In the scheduler allocation: start one runner process
#!/bin/bash
#PBS -l walltime=24:00:00
tokimeki runner --id node01 --manner-period 30m


# From any shared fs node: submit work at any time
tokimeki submit -c "python do_compute.py --alpha 0.001"
tokimeki submit -w node01 -c "python do_compute.py --alpha 0.001"

# Wrong config? replace process immediately without re-entering scheduler queue
tokimeki kill node01 <job_id>
tokimeki submit -w node01 -c "python do_compute.py --alpha 0.0003"

# Finished early? enqueue the next experiment in the same allocation
tokimeki submit eval-script.sh
```

The runner is polite and has good manners. It ends its lifecycle after being jobless for 1 hour (default), releasing the node to the underlying job scheduler.

## Etymology

Tokimeki was originally designed to run on top of the job system in the Kagayaki cluster.

In _a certain anime series_'s timeline, Tokimeki is the thing that comes after Kagayaki, which gives way to the name. The system's runner daemons are, of course, called _Tokimeki Runners_.

## Acknowledgement

This project was developed autonomously by Claude Code and OpenAI Codex based on my requirements and guidance.

## 💻 Quick start

```bash
# Build
go build -o tokimeki \
  -ldflags "-X main.versionCommit=$(git rev-parse --short=12 HEAD) -X main.versionBranch=$(git rev-parse --abbrev-ref HEAD) -X main.versionBuildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  ./cmd/tokimeki

tokimeki version

# On a compute node: start a runner
tokimeki runner --id node01 --lifetime 48h

# From anywhere: submit a job (works even before the runner starts)
tokimeki submit solve.sh
tokimeki submit --burst 8h important-job.sh

# Check status
tokimeki runners        # list all runners
tokimeki ps             # running + queued jobs (queued jobs are unassigned)
tokimeki ps -a          # include finished jobs
tokimeki logs <job_id>  # view output
tokimeki job <job_id>   # job details
```

## Architecture

Tokimeki is **masterless**. There is no coordinator process. 

Tokimeki works over a shared filesystem. Each runner daemon is independent, and the CLI interacts with runners through that shared filesystem. Currently, one runner executes only one job at a time. 

Runners can also advertise their maximum lifetime so submission can take remaining time into account when scheduling work. Scheduling is done on a voluntary basis, which means runners only pick jobs that's within their execution budget. 

This means:

- No single point of failure. Runners are independent.
- Adding a new runner is just starting a new process with a unique `--id`.
- The system tolerates multi-second filesystem propagation delays by design.

### Manner period

Runners are polite. If a runner has been **jobless** for the manner period (default: 1 hour), it exits gracefully to give place to other jobs on the underlying job system. This also applies on startup — if a runner starts and finds no jobs in its queue, it waits for the manner period and exits if still jobless.

The manner timer resets every time a job is running or queued. So if jobs keep arriving, the runner stays alive indefinitely.

### Lifetime and burst scheduling

When starting a runner, you can also provide a maximum lifetime such as `tokimeki runner --lifetime 48h`. This should match the time you registered with the underlying job system. It does not extend or reduce the actual lifetime of the node by itself, but it gives Tokimeki useful scheduling information.

When submitting a job, you can provide an estimated required runtime with `tokimeki submit --burst 8h`. Tokimeki scans the current runners and only dispatches work to a runner whose remaining lifetime can accommodate that burst. If no current runner can finish the job, the CLI will notify and does not submit the job. This behavior is intended for jobs where resuming is difficult, and must be finished within the runner's lifetime.


## CLI Reference

- `tokimeki runner`: start a runner daemon on the current node, optionally with `--lifetime`
- `tokimeki runners`: list known runners and their liveness
- `tokimeki ps`: inspect queued and running jobs
- `tokimeki submit`: submit a script or inline command, optionally with `--burst`
- `tokimeki exec`: run a one-off command on a target runner
- `tokimeki kill`: stop the currently running job on a runner
- `tokimeki cancel`: cancel a queued job
- `tokimeki logs`: show stdout and stderr for a job
- `tokimeki job`: show job metadata
- `tokimeki gc`: clean up stale state
- `tokimeki version`: print build metadata

## Configuration

| Environment Variable | Description | Default |
|---|---|---|
| `TOKIMEKI_HOME` | Base directory for all state | `~/.tokimeki` |

## Atomic writes

Almost every file write in the system goes through the same primitive: write to a temp file, fsync, then rename to the final path.

```text
write(content) -> ~/.tokimeki/tmp/<random>.tmp
fsync()
rename() -> final destination
```

`rename()` on POSIX is atomic within the same filesystem. This guarantees no reader ever sees a half-written file. The temp directory must be on the same filesystem as the final destination for the rename to be atomic.
