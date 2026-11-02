<img src="assets/tokimeki.jpeg" style="max-width: 768px;">

## ❓ What is this?

A masterless, filesystem-based job runner.

Tokimeki lets you submit and manage jobs across nodes that share a common filesystem but have no direct network connectivity. All communication happens through the shared filesystem, with no master node. The one exception is `tokimeki shell`: it sets up through the filesystem, then uses a direct TCP connection for the interactive terminal.

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
# This works before any runner is ready
tokimeki submit do_compute.sh

# Wrong config? replace process immediately without re-entering scheduler queue
tokimeki submit -w node01 -c "python do_compute.py --alpha 0.0003"
#                ^~~~ submits directly to node01
tokimeki kill <old_job_id>

# Finished early? enqueue the next experiment in the same allocation
tokimeki submit eval-script.sh
```

The runner is polite and has good manners. It ends its lifecycle after being jobless for 1 hour (default), releasing the node to the underlying job scheduler. It does not work outside the limit commited to the underlying (e.g. PBS) scheduler.

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
tokimeki runner --id node01

# From anywhere: submit a job (works even before the runner starts)
tokimeki submit solve.sh

# Check status
tokimeki runners        # list all runners
tokimeki ps             # running + queued jobs (queued jobs are unassigned)
tokimeki ps -a          # include finished jobs
tokimeki tui            # interactive live jobs, logs, and resources
tokimeki logs <job_id>  # view output
tokimeki job <job_id>   # job details
```

## Architecture

Tokimeki is **masterless**. There is no coordinator process. 

Tokimeki works over a shared filesystem. Each runner daemon is independent, and the CLI interacts with runners through that shared filesystem. Currently, one runner executes only one job at a time. Only `tokimeki shell` uses networking; everything else, including `exec`, stays on the filesystem protocol.

This means:

- No single point of failure. Runners are independent.
- Adding a new runner is just starting a new process with a unique `--id`.
- The system tolerates multi-second filesystem propagation delays by design.

### Manner period

Runners are polite. If a runner has been **jobless** for the manner period (default: 1 hour), it exits gracefully to give place to other jobs on the underlying job system. This also applies on startup — if a runner starts and finds no jobs in its queue, it waits for the manner period and exits if still jobless.

The manner timer resets every time a job is running or queued. So if jobs keep arriving, the runner stays alive indefinitely.

### Communication protocol: atomic writes

The shared filesystem is the communication channel between Tokimeki runners and the user (see [Interactive shell](#interactive-shell) for the one exception). A request is made by writing a file to a predetermined location, and poll for response in another location.

Atomicity is achieved through a simple trick. Almost every file write in the system goes through the same primitive: write to a temp file, fsync, then rename to the final path.

```text
write(content) -> ~/.tokimeki/tmp/<random>.tmp
fsync()
rename() -> final destination
```

`rename()` on POSIX is atomic within the same filesystem. This guarantees no reader ever sees a half-written file. The temp directory must be on the same filesystem as the final destination for the rename to be atomic. Synchronization between workers rely on a similar trick. Note that Tokimeki might not work reliably on remote filesystems that does not obey this assumption.


### Interactive shell

`tokimeki shell gpu01` opens a real terminal on a runner without SSH, similar to `srun --pty`: colors, vim, htop, Ctrl-C, and window resizing all work. You can also press `s` on a runner in `tokimeki tui`.

```text
tokimeki shell                         runner gpu01
 1. listen on a TCP port, write private
    session file ---- shared FS ------>
 2. "shell" request ----- inbox ------> 3. spawn `shell-daemon` child
                                        4. read session file
 5. accept  <------- TLS over TCP ------   connect back, present token
 6. keystrokes, output, resizes <-----> 7. shell on a PTY
```

- **Network requirement:** the runner's node must be able to open a TCP connection to the machine running `tokimeki shell`. Runners listen on no ports. Use `--listen` and `--advertise` if the port or address needs to be specific.
- **Isolation:** all networking and PTY code runs in the separate `shell-daemon` process, so a bug there cannot crash the runner. If the runner dies, even by `kill -9`, its open shells are closed.
- **Same user:** Tokimeki assumes the client and runners log in as the same user and can all read and write the state directory. Multi-user setups are not supported.
- **Security:** traffic is encrypted, and the secrets are kept in a file only your user can read, so other users on the cluster can't join a session.
- **Manner period:** an open shell keeps the runner from exiting for being jobless, so close shells you're done with.

Protocol details are in [engine/README.md](engine/README.md#interactive-shell-protocol).

## CLI Reference

For a complete usage guide with examples, flags, and recipes (dependencies, retries, priority, resource hints, interactive exec, JSON output, event log, shell completion), see **[docs/USAGE.md](docs/USAGE.md)**.

Commands at a glance:

- `tokimeki runner`: start a runner daemon on the current node
- `tokimeki runners`: list known runners and their liveness
- `tokimeki ps` / `ls`: inspect queued and running jobs
- `tokimeki top`: live dashboard with RUNNERS / JOBS / RECENT panels (`--plain`, `--once`)
- `tokimeki submit`: submit scripts or an inline command (`--after`, `--priority`, `--cpus`, `--mem-mb`, `--retries`, `--backoff`, `--name`, `--desc`, `--live`, `--wait`, `--quiet`, `--json`)
- `tokimeki watch <job>`: block until terminal state; exit code = job's exit code
- `tokimeki shell <node>`: open a full interactive terminal on a runner (uses TCP)
- `tokimeki exec` (`-i` for interactive): run a one-off command on a target runner
- `tokimeki kp <node>`: run `killall python` on a target runner
- `tokimeki kill`: stop the currently running job on a runner
- `tokimeki cancel`: cancel a queued job
- `tokimeki logs`: show stdout and stderr for a job (`--follow`, `--tail`, `--stdout`, `--stderr`)
- `tokimeki job`: show job metadata
- `tokimeki events`: stream the shared event log
- `tokimeki gc`: clean up stale state (`--older-than`, `--max-size`, `--dry-run`)
- `tokimeki completion`: generate shell completion (bash/zsh/fish)
- `tokimeki version`: print build metadata

The binary can also be aliased to `tok` for brevity (`ln -s "$(which tokimeki)" ~/.local/bin/tok`); `tok` and `tokimeki` are interchangeable.

## Configuration

| Environment Variable | Description | Default |
|---|---|---|
| `TOKIMEKI_HOME` | Base directory for all state | `~/.tokimeki` |
