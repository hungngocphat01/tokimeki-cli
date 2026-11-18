// Package main provides the tokimeki CLI binary.
package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/hungngocphat01/tokimeki/client"
	"github.com/hungngocphat01/tokimeki/protocol"
	"github.com/hungngocphat01/tokimeki/runner"
	"github.com/hungngocphat01/tokimeki/shell"
	"github.com/hungngocphat01/tokimeki/shell/session"
	"github.com/ngocphat/tokimeki-cli/display"
	"github.com/ngocphat/tokimeki-cli/tui"
	"github.com/spf13/cobra"
)

// baseDir is bound to the --base persistent flag.
var baseDir string

// completeJobIDs returns candidate job IDs for shell completion.
func completeJobIDs(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	c := client.New(resolveBase())
	ids, err := c.JobIDs()
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}
	return ids, cobra.ShellCompDirectiveNoFileComp
}

// completeWorkerIDs returns candidate worker IDs for shell completion.
func completeWorkerIDs(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	c := client.New(resolveBase())
	ids, err := c.WorkerIDs()
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}
	return ids, cobra.ShellCompDirectiveNoFileComp
}

// resolveBase returns the base directory where all tokimeki state lives.
// Priority: --base flag > TOKIMEKI_HOME env > ~/.tokimeki
func resolveBase() string {
	if baseDir != "" {
		return baseDir
	}
	if env := os.Getenv("TOKIMEKI_HOME"); env != "" {
		return env
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".tokimeki")
}

// parseDuration extends time.ParseDuration with a leading day component.
func parseDuration(s string) (time.Duration, error) {
	if i := strings.IndexByte(s, 'd'); i >= 0 {
		days, err := strconv.ParseInt(s[:i], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid day duration: %q", s)
		}
		if days > int64(time.Duration(1<<63-1)/(24*time.Hour)) || days < int64(time.Duration(-1<<63)/(24*time.Hour)) {
			return 0, fmt.Errorf("day duration out of range: %q", s)
		}
		dayDuration := time.Duration(days) * 24 * time.Hour
		if i == len(s)-1 {
			return dayDuration, nil
		}
		rest, err := time.ParseDuration(s[i+1:])
		if err != nil || (rest > 0 && dayDuration > time.Duration(1<<63-1)-rest) || (rest < 0 && dayDuration < time.Duration(-1<<63)-rest) {
			return 0, fmt.Errorf("invalid duration: %q", s)
		}
		return dayDuration + rest, nil
	}
	return time.ParseDuration(s)
}

var ttlPattern = regexp.MustCompile(`^(?:[0-9]+d)?(?:[0-9]+h)?$`)

func parseTTL(s string) (time.Duration, error) {
	if !ttlPattern.MatchString(s) || s == "" {
		return 0, fmt.Errorf("invalid TTL %q: specify hours and days, for example 6d23h", s)
	}
	ttl, err := parseDuration(s)
	if err != nil || ttl <= 0 || ttl%time.Hour != 0 {
		return 0, fmt.Errorf("invalid TTL %q: specify a positive whole number of hours and days", s)
	}
	return ttl, nil
}

// randomHex returns n random hex characters.
func randomHex(n int) string {
	b := make([]byte, (n+1)/2)
	rand.Read(b)
	hex := fmt.Sprintf("%x", b)
	return hex[:n]
}

func main() {
	os.Args = normalizeLogIndexArgs(os.Args)
	os.Exit(execute(os.Args[1:]))
}

func tuiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "tui",
		Short: "Open the interactive jobs dashboard",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return tui.Run(resolveBase())
		},
	}
}

// --- runner ---

func runnerCmd() *cobra.Command {
	var workerID string
	var poll time.Duration
	var mannerPeriod time.Duration
	var ttlText string
	var capCPUs, capMemMB int
	var computeThreads int

	cmd := &cobra.Command{
		Use:   "runner",
		Short: "Start the runner daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			base := resolveBase()
			if computeThreads < 1 {
				return argError("--compute-threads must be at least 1")
			}
			var ttl time.Duration
			if ttlText != "" {
				var err error
				ttl, err = parseTTL(ttlText)
				if err != nil {
					return fmt.Errorf("--ttl: %w", err)
				}
			}

			if workerID == "" {
				hostname, _ := os.Hostname()
				workerID = hostname + "-" + randomHex(4)
			}

			d := runner.NewDaemon(workerID, base)
			d.SetMannerPeriod(mannerPeriod)
			d.SetTTL(ttl)
			d.SetCapacity(runner.Capacity{CPUs: capCPUs, MemMB: capMemMB})
			d.SetComputeThreads(computeThreads)
			if err := d.Register(); err != nil {
				return fmt.Errorf("register: %w", err)
			}

			ttlLabel := "none"
			if ttl > 0 {
				ttlLabel = display.TTL(ttl)
			}
			log.Printf("Runner %s started (compute_threads=%d, poll=%s, manner=%s, ttl=%s, base=%s)", d.WorkerID(), computeThreads, poll, mannerPeriod, ttlLabel, base)

			// Handle graceful shutdown.
			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

			go func() {
				<-sigCh
				fmt.Printf("\nRunner %s stopping...\n", d.WorkerID())
				d.Stop()
			}()

			d.Run(poll)
			fmt.Printf("Runner %s stopped.\n", d.WorkerID())
			return nil
		},
	}

	cmd.Flags().StringVar(&workerID, "id", "", "worker ID (default: <hostname>-<4 hex>)")
	cmd.Flags().DurationVar(&poll, "poll", 2*time.Second, "poll interval")
	cmd.Flags().DurationVar(&mannerPeriod, "manner-period", 1*time.Hour, "exit after being jobless for this long (0 to disable)")
	cmd.Flags().StringVar(&ttlText, "ttl", "", "optional runner lifetime metadata in hours and days (e.g. 6d23h)")
	cmd.Flags().IntVar(&capCPUs, "capacity-cpus", 0, "available CPU count for capacity-aware scheduling (0 = unconstrained)")
	cmd.Flags().IntVar(&capMemMB, "capacity-mem-mb", 0, "available memory in MB for capacity-aware scheduling (0 = unconstrained)")
	cmd.Flags().IntVar(&computeThreads, "compute-threads", 1, "compute jobs to run at once (one coordinator slot is extra)")

	return cmd
}

// --- runners ---

func runnersCmd() *cobra.Command {
	var showAll, asJSON bool

	cmd := &cobra.Command{
		Use:   "runners",
		Short: "List running runners",
		RunE: func(cmd *cobra.Command, args []string) error {
			runners, err := client.New(resolveBase()).ListRunners(client.ListRunnersOpts{All: showAll})
			if err != nil {
				return err
			}
			if asJSON {
				return writeJSON(runners)
			}
			return printRunners(os.Stdout, runners)
		},
	}
	cmd.Flags().BoolVarP(&showAll, "all", "a", false, "show all runners including stopped")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of a table")
	return cmd
}

// --- ps ---

func psCmd() *cobra.Command {
	var filterWorker string
	var filterName string
	var showAll, asJSON bool

	cmd := &cobra.Command{
		Use:   "ps [worker_id]",
		Short: "List jobs (running/queued by default, all with -a)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := client.New(resolveBase())
			if len(args) == 1 {
				if filterWorker != "" {
					return argError("use either positional worker_id or -w/--worker, not both")
				}
				filterWorker = args[0]
			}
			jobs, err := c.ListJobs(client.ListJobsOpts{All: showAll, Worker: filterWorker, Name: filterName})
			if err != nil {
				return err
			}
			if asJSON {
				return writeJSON(jobs)
			}
			return printPS(os.Stdout, jobs)
		},
	}
	cmd.Flags().BoolVarP(&showAll, "all", "a", false, "show all jobs including finished")
	cmd.Flags().StringVarP(&filterWorker, "worker", "w", "", "filter jobs by worker ID")
	cmd.Flags().StringVar(&filterName, "name", "", "filter jobs by tag (case-insensitive substring)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of a table")
	return cmd
}

// --- ls ---

func lsCmd() *cobra.Command {
	var filterName string
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List jobs, then runners",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := client.New(resolveBase())
			jobs, err := c.ListJobs(client.ListJobsOpts{Name: filterName})
			if err != nil {
				return err
			}
			if err := printPS(os.Stdout, jobs); err != nil {
				return err
			}
			fmt.Print("\n\n")
			runners, err := c.ListRunners(client.ListRunnersOpts{})
			if err != nil {
				return err
			}
			return printRunners(os.Stdout, runners)
		},
	}
	cmd.Flags().StringVar(&filterName, "name", "", "filter jobs by tag (case-insensitive substring)")
	return cmd
}

// --- submit ---

func submitCmd() *cobra.Command {
	var timeout time.Duration
	var resubmit bool
	var inlineCommand string
	var workerID string
	var afterCSV string
	var priority, cpus, memMB, retries int
	var backoff string
	var name, group, desc string
	var coordinator bool
	var wait, quiet, asJSON, live bool

	cmd := &cobra.Command{
		Use:   "submit [scripts...]",
		Short: "Submit a job to the global queue",
		Long: `Submit work to the global queue.
Use either:
- a script path argument
- --command/-c for an inline command
- -r/--resubmit with a job ID (copies its script and settings; -w targets a runner).

-C/--coordinator submits a coordinator: a job that does no compute itself but
submits compute jobs, polls its group (tok group) and combines results. Each
runner runs at most one coordinator next to its compute jobs. Jobs submitted
from inside a job join that job's group unless -g is given.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := client.New(resolveBase())
			if live && (quiet || asJSON) {
				return argError("--live cannot be combined with --quiet or --json")
			}
			if live && resubmit {
				return argError("--live cannot be combined with --resubmit")
			}

			if resubmit {
				if len(args) != 1 {
					return argError("resubmit requires: submit -r [-w <worker_id>] <job_id>")
				}
				if inlineCommand != "" {
					return argError("--command cannot be used with --resubmit")
				}
				res, err := c.Resubmit(args[0], workerID, client.SubmitOpts{Timeout: timeout})
				if err != nil {
					return err
				}
				if asJSON {
					return writeJSON(res)
				}
				if quiet {
					fmt.Println(res.JobID)
					return nil
				}
				fmt.Printf("Job %s submitted", res.JobID)
				if res.Position > 0 {
					fmt.Printf(" (queue position %d)", res.Position)
				}
				fmt.Println()
				return nil
			}

			var deps []string
			if afterCSV != "" {
				for _, d := range strings.Split(afterCSV, ",") {
					d = strings.TrimSpace(d)
					if d != "" {
						deps = append(deps, d)
					}
				}
			}
			if backoff != "" {
				if _, err := time.ParseDuration(backoff); err != nil {
					return argError("invalid --backoff: %v", err)
				}
			}
			user := os.Getenv("USER")
			if group != "" && name != "" && group != name {
				return argError("--group and --name differ")
			}
			if group == "" {
				group = name
			}
			kind := ""
			if coordinator {
				kind = protocol.KindCoordinator
			}

			spec := protocol.JobSpec{
				TargetWorkerID: workerID,
				DependsOn:      deps,
				Priority:       priority,
				CPUs:           cpus,
				MemMB:          memMB,
				User:           user,
				Retries:        retries,
				Backoff:        backoff,
				Name:           group,
				Desc:           desc,
				Kind:           kind,
			}

			if quiet && asJSON {
				return argError("--quiet and --json are mutually exclusive")
			}
			if live {
				var paths []string
				if inlineCommand == "" {
					if len(args) == 0 {
						return argError("script submit requires: submit <script_path> [script_path...]")
					}
					paths = args
				} else if len(args) != 0 {
					return argError("inline submit requires: submit -c <command>")
				}
				targets := []string{workerID}
				if inlineCommand == "" {
					var err error
					targets, err = submitTargets(os.Args, cmd, len(paths))
					if err != nil {
						return err
					}
				}
				var ids []string
				for i := range targets {
					spec.TargetWorkerID = targets[i]
					spec.Command = inlineCommand
					path := ""
					if inlineCommand == "" {
						path = paths[i]
					}
					res, err := submitLiveOne(c, spec, path, timeout)
					if err != nil {
						return err
					}
					ids = append(ids, res.JobID)
				}
				return runLiveJobs(c, ids)
			}
			var submitted []client.SubmitResult
			submit := func(spec protocol.JobSpec, path string) error {
				res, err := submitOne(c, spec, path, quiet || asJSON, timeout)
				if err != nil {
					return err
				}
				if quiet {
					fmt.Println(res.JobID)
				}
				submitted = append(submitted, res)
				return nil
			}
			if inlineCommand != "" {
				if len(args) != 0 {
					return argError("inline submit requires: submit -c <command>")
				}
				spec.Command = inlineCommand
				if err := submit(spec, ""); err != nil {
					return err
				}
			} else {
				if len(args) == 0 {
					return argError("script submit requires: submit <script_path> [script_path...]")
				}
				targets, err := submitTargets(os.Args, cmd, len(args))
				if err != nil {
					return err
				}
				for i, path := range args {
					spec.TargetWorkerID = targets[i]
					if err := submit(spec, path); err != nil {
						return &batchError{err: fmt.Errorf("submit %s: %w", path, err), submitted: submitted}
					}
				}
			}
			if !wait {
				if !asJSON {
					return nil
				}
				if len(submitted) == 1 {
					return writeJSON(submitted[0])
				}
				return writeJSON(submitted)
			}
			failureCode := 0
			var results []client.WatchResult
			for _, res := range submitted {
				jobID := res.JobID
				opts := client.WatchOpts{}
				if !asJSON {
					opts.OnStatus = printWatchStatus(jobID)
				}
				res, err := c.Watch(jobID, opts)
				if err != nil {
					return err
				}
				results = append(results, res)
				if !asJSON {
					if res.ExitCode != nil {
						fmt.Printf("[wait %s] %s exit=%d\n", jobID, res.Status, *res.ExitCode)
					} else {
						fmt.Printf("[wait %s] %s\n", jobID, res.Status)
					}
				}
				if failureCode == 0 {
					if res.ExitCode != nil && *res.ExitCode != 0 {
						failureCode = *res.ExitCode
					} else if res.ExitCode == nil && res.Status != protocol.StatusCompleted {
						failureCode = 1
					}
				}
			}
			if asJSON {
				if err := writeJSON(map[string]any{"submitted": submitted, "results": results}); err != nil {
					return err
				}
			}
			if failureCode != 0 {
				os.Exit(failureCode)
			}
			return nil
		},
	}

	cmd.Flags().DurationVar(&timeout, "timeout", 30*time.Second, "response timeout")
	cmd.Flags().StringVarP(&workerID, "worker", "w", "", "target worker ID (optional)")
	cmd.Flags().StringVarP(&inlineCommand, "command", "c", "", "inline command to submit to the global queue")
	cmd.Flags().BoolVarP(&resubmit, "resubmit", "r", false, "resubmit a copy of an existing job by ID")
	cmd.Flags().StringVar(&afterCSV, "after", "", "wait for these comma-separated job IDs to complete first")
	cmd.Flags().IntVar(&priority, "priority", 0, "scheduling priority; higher runs sooner (default 0)")
	cmd.Flags().IntVar(&cpus, "cpus", 0, "required CPU count (resource hint)")
	cmd.Flags().IntVar(&memMB, "mem-mb", 0, "required memory in MB (resource hint)")
	cmd.Flags().IntVar(&retries, "retries", 0, "max retries on failed/crashed (default 0)")
	cmd.Flags().StringVar(&backoff, "backoff", "", "delay between retries (e.g. 30s, 5m)")
	cmd.Flags().StringVarP(&group, "group", "g", "", "group label (default: $TOKIMEKI_GROUP inside a job)")
	cmd.Flags().StringVarP(&name, "name", "n", "", "alias of --group")
	cmd.Flags().BoolVarP(&coordinator, "coordinator", "C", false, "submit a coordinator job (runs in the runner's coordinator slot)")
	cmd.Flags().StringVar(&desc, "desc", "", "optional free-text description")
	cmd.Flags().BoolVar(&wait, "wait", false, "block until job reaches a terminal state; exit with the job's exit code")
	cmd.Flags().BoolVarP(&live, "live", "l", false, "show assignment and stream job output until completion; Ctrl-C leaves the job running")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "print only the job ID on success")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the submit result as JSON (see docs/USAGE.md)")
	return cmd
}

// submitOne submits one job. Unless silent, it prints each submit stage as
// it happens.
func submitOne(c *client.Client, spec protocol.JobSpec, path string, silent bool, timeout time.Duration) (client.SubmitResult, error) {
	opts := client.SubmitOpts{Timeout: timeout}
	if !silent {
		opts.OnProgress = printSubmitProgress
	}
	return submitSpec(c, spec, path, opts)
}

// submitSpec submits an inline command (path empty) or a script file.
func submitSpec(c *client.Client, spec protocol.JobSpec, path string, opts client.SubmitOpts) (client.SubmitResult, error) {
	if path == "" {
		return c.Submit(spec, opts)
	}
	return c.SubmitFile(spec, path, opts)
}

// printSubmitProgress prints each submit stage as it happens, so the queue
// position shows before the runner ack arrives.
func printSubmitProgress(stage client.SubmitStage, res client.SubmitResult) {
	switch stage {
	case client.SubmitQueued:
		if res.Worker == "" {
			fmt.Printf("Job %s submitted to global queue\n", res.JobID)
		} else {
			fmt.Printf("Job %s submitted to global queue (target worker: %s)\n", res.JobID, res.Worker)
		}
		if res.Position > 0 {
			fmt.Printf("Global queue position: %d\n", res.Position)
		}
	case client.SubmitAcked:
		if res.WorkerAcked {
			fmt.Printf("Runner %s is running and acknowledged\n", res.Worker)
		} else {
			fmt.Printf("Runner %s is not running yet. Job will be picked up when it starts.\n", res.Worker)
		}
	}
}

func submitLiveOne(c *client.Client, spec protocol.JobSpec, path string, timeout time.Duration) (client.SubmitResult, error) {
	res, err := submitSpec(c, spec, path, client.SubmitOpts{
		Timeout: timeout,
		OnProgress: func(stage client.SubmitStage, res client.SubmitResult) {
			if stage != client.SubmitQueued {
				return
			}
			fmt.Printf("Job %s submitted to global queue\n", res.JobID)
			if res.Position > 0 {
				fmt.Printf("Global queue position: %d\n", res.Position)
			}
			if res.Worker != "" {
				fmt.Printf("Target runner: %s\n", res.Worker)
			}
		},
	})
	if err != nil {
		return res, err
	}
	if res.WorkerAcked {
		fmt.Println("Worker acknowledgment: received")
	} else {
		fmt.Println("Worker acknowledgment: pending")
	}
	return res, nil
}

func runLiveJobs(c *client.Client, ids []string) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	failureCode := 0
	for _, id := range ids {
		code, err := streamLiveJob(ctx, c, id)
		if errors.Is(err, context.Canceled) {
			fmt.Println("\nLive stream stopped. Submitted jobs were not canceled.")
			return nil
		}
		if err != nil {
			return err
		}
		if code != 0 && failureCode == 0 {
			failureCode = code
		}
	}
	if failureCode != 0 {
		os.Exit(failureCode)
	}
	return nil
}

func streamLiveJob(ctx context.Context, c *client.Client, id string) (int, error) {
	job, err := c.FollowJob(ctx, id, client.FollowOpts{
		Stdout: os.Stdout, Stderr: os.Stderr, Poll: 250 * time.Millisecond,
		OnStart: func(job client.Job) {
			if job.StartedAtNs != 0 && job.Worker != "" {
				fmt.Printf("Job assigned to %s\n", job.Worker)
			}
			fmt.Println("#### BEGIN JOB LOGS ####")
		},
	})
	if err != nil {
		return 0, err
	}
	fmt.Println("#### END JOB LOGS ####")
	message, code := liveSummary(job, liveColorEnabled())
	fmt.Println(message)
	return code, nil
}

// liveSummary is the closing line of live mode and the exit code to use. The
// duration runs from start (or, if it never started, submission) to finish.
func liveSummary(job client.Job, color bool) (string, int) {
	code := 0
	label := "Job finished successfully"
	shade := "\x1b[32m"
	if job.Status != protocol.StatusCompleted {
		label, shade, code = "Job failed", "\x1b[31m", 1
		if job.ExitCode != nil && *job.ExitCode != 0 {
			code = *job.ExitCode
		}
	}
	start := job.StartedAtNs
	if start == 0 {
		start = job.SubmittedAtNs
	}
	if job.FinishedAtNs != 0 && start != 0 {
		seconds := max(0, int(time.Duration(job.FinishedAtNs-start).Seconds()))
		if seconds < 60 {
			label += fmt.Sprintf(" in %ds", seconds)
		} else {
			label += fmt.Sprintf(" in %dm%ds", seconds/60, seconds%60)
		}
	}
	if color {
		label = shade + label + "\x1b[0m"
	}
	return label, code
}

func liveColorEnabled() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	info, err := os.Stdout.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// submitTargets binds each --worker to the next script token only. Cobra's
// parsed positional args no longer contain flag positions, so inspect the
// original command tokens using the same flag value rules.
func submitTargets(argv []string, cmd *cobra.Command, count int) ([]string, error) {
	start := -1
	for i, arg := range argv {
		if arg == "submit" {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return nil, fmt.Errorf("submit command not found")
	}
	var targets []string
	pending := ""
	positional := false
	for i := start; i < len(argv); i++ {
		arg := argv[i]
		if arg == "--" {
			positional = true
			continue
		}
		if !positional && strings.HasPrefix(arg, "-") {
			name, value, assigned := strings.Cut(strings.TrimLeft(arg, "-"), "=")
			var flagName string
			if len(name) == 1 {
				if f := cmd.Flags().ShorthandLookup(name); f != nil {
					flagName = f.Name
				}
			} else {
				flagName = name
			}
			flag := cmd.Flags().Lookup(flagName)
			if flag == nil {
				flag = cmd.InheritedFlags().Lookup(flagName)
			}
			if flag == nil {
				return nil, fmt.Errorf("unknown flag %s", arg)
			}
			if !assigned && flag.NoOptDefVal == "" {
				i++
				if i >= len(argv) {
					return nil, fmt.Errorf("%s needs a value", arg)
				}
				value = argv[i]
			}
			if flagName == "worker" {
				pending = value
			}
			continue
		}
		targets = append(targets, pending)
		pending = ""
	}
	if len(targets) != count {
		return nil, fmt.Errorf("could not match workers to scripts")
	}
	return targets, nil
}

// --- exec ---

func execCmd() *cobra.Command {
	var timeout time.Duration
	var interactive, asJSON bool

	cmd := &cobra.Command{
		Use:   "exec <worker_id> <command...>",
		Short: "Run a command immediately on a target runner and stream output",
		Args:  cobra.MinimumNArgs(2),
		ValidArgsFunction: func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			if len(args) == 0 {
				return completeWorkerIDs(nil, args, toComplete)
			}
			return nil, cobra.ShellCompDirectiveDefault
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			c := client.New(resolveBase())
			workerID := args[0]
			command := strings.Join(args[1:], " ")
			if interactive {
				if asJSON {
					return argError("--interactive and --json are mutually exclusive")
				}
				return c.ExecInteractive(workerID, command, timeout)
			}
			if asJSON {
				// The caller reads exit_code; only a failed request is an error.
				res, err := c.Exec(workerID, command, timeout)
				if err != nil {
					return err
				}
				return writeJSON(res)
			}
			return runExec(c, workerID, command, timeout)
		},
	}

	cmd.Flags().DurationVar(&timeout, "timeout", 30*time.Second, "response timeout")
	cmd.Flags().BoolVarP(&interactive, "interactive", "i", false, "stream stdin/stdout/stderr via FIFOs (line-buffered, no PTY)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print {stdout, stderr, exit_code} as JSON")
	return cmd
}

// --- shell ---

func shellCmd() *cobra.Command {
	var opts client.ShellOptions

	cmd := &cobra.Command{
		Use:   "shell <worker_id>",
		Short: "Open an interactive terminal on a runner (uses TCP, no SSH needed)",
		Long: `Open a full interactive terminal (PTY) on a runner, with color, full-screen
programs, job control, and window resizing.

Unlike other commands, the session uses the network: the runner's node connects
back to this machine over TLS. The shared filesystem is only used to set it up.`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeWorkerIDs,
		SilenceUsage:      true,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Status = os.Stderr
			code, err := client.New(resolveBase()).Shell(args[0], opts)
			if err != nil {
				return err
			}
			if code != 0 {
				os.Exit(code)
			}
			return nil
		},
	}

	cmd.Flags().DurationVar(&opts.Timeout, "timeout", 60*time.Second, "how long to wait for the runner to connect back")
	cmd.Flags().StringVar(&opts.Listen, "listen", "", "local address to listen on, e.g. :50000 (default: all interfaces, random port)")
	cmd.Flags().StringVar(&opts.Advertise, "advertise", "", "hostname or IP the runner should connect to (default: auto-detect)")
	return cmd
}

// shellDaemonCmd is the child process a runner spawns for each shell session.
func shellDaemonCmd() *cobra.Command {
	var workerID, sessionID string

	cmd := &cobra.Command{
		Use:           session.DaemonCommand,
		Short:         "Serve one shell session (started by the runner)",
		Hidden:        true,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Keep the descriptor out of the user's shell.
			syscall.CloseOnExec(session.LifelineFD)
			err := shell.RunDaemon(shell.DaemonOptions{
				Paths:     protocol.NewPaths(resolveBase()),
				WorkerID:  workerID,
				SessionID: sessionID,
				Lifeline:  os.NewFile(session.LifelineFD, "lifeline"),
			})
			if err != nil {
				fmt.Fprintf(os.Stderr, "shell-daemon %s: %v\n", sessionID, err)
			}
			return err
		},
	}

	cmd.Flags().StringVar(&workerID, session.FlagWorker, "", "worker ID")
	cmd.Flags().StringVar(&sessionID, session.FlagSession, "", "session ID")
	_ = cmd.MarkFlagRequired(session.FlagWorker)
	_ = cmd.MarkFlagRequired(session.FlagSession)
	return cmd
}

// runExec runs command on a runner, copies its output to ours, and turns a
// non-zero exit into an error.
func runExec(c *client.Client, workerID, command string, timeout time.Duration) error {
	res, err := c.Exec(workerID, command, timeout)
	if err != nil {
		return err
	}
	fmt.Fprint(os.Stdout, res.Stdout)
	fmt.Fprint(os.Stderr, res.Stderr)
	if res.ExitCode != 0 {
		return fmt.Errorf("exec command exited with code %d", res.ExitCode)
	}
	return nil
}

// kp is a short form for running `killall python` on a runner.
func kpCmd() *cobra.Command {
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:               "kp <node_name>",
		Short:             "Run killall python on a runner",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeWorkerIDs,
		RunE: func(_ *cobra.Command, args []string) error {
			return runExec(client.New(resolveBase()), args[0], "killall python", timeout)
		},
	}
	cmd.Flags().DurationVar(&timeout, "timeout", 30*time.Second, "response timeout")
	return cmd
}

// --- kill ---

func killCmd() *cobra.Command {
	var sig int
	var timeout time.Duration
	var worker string
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "kill <job_id>",
		Short: "Send a signal to a running job",
		Long: `Send a signal to a running job. The owning runner is resolved from the
job's meta.json automatically — pass --worker to override (rare; useful
only when meta is missing or corrupt).`,
		Args: cobra.ExactArgs(1),
		ValidArgsFunction: func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			if len(args) == 0 {
				return completeJobIDs(nil, args, toComplete)
			}
			return nil, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			c := client.New(resolveBase())
			jobID := args[0]
			var err error
			if worker != "" {
				err = c.Kill(worker, jobID, sig, timeout)
			} else {
				var res client.KillResult
				res, err = c.KillJob(jobID, sig, timeout)
				worker = res.Worker
			}
			if err != nil {
				return err
			}
			if sig == 0 {
				sig = 15
			}
			if asJSON {
				return writeJSON(map[string]any{"job_id": jobID, "worker_id": worker, "signal": sig})
			}
			fmt.Printf("Kill signal %d sent to job %s\n", sig, jobID)
			return nil
		},
	}

	cmd.Flags().IntVar(&sig, "signal", 15, "signal number to send")
	cmd.Flags().DurationVar(&timeout, "timeout", 30*time.Second, "response timeout")
	cmd.Flags().StringVar(&worker, "worker", "", "explicit worker ID (override meta lookup)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print {job_id, worker_id, signal} as JSON")
	return cmd
}

// --- cancel ---

func cancelCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:               "cancel <job_id>",
		Short:             "Cancel a queued job",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeJobIDs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := client.New(resolveBase()).Cancel(args[0]); err != nil {
				return err
			}
			if asJSON {
				return writeJSON(map[string]string{"job_id": args[0]})
			}
			fmt.Printf("Canceled queued job %s\n", args[0])
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print {job_id} as JSON")
	return cmd
}

// --- logs ---

func logsCmd() *cobra.Command {
	var follow, stdoutOnly, stderrOnly, all, page bool
	var tail int

	cmd := &cobra.Command{
		Use:   "logs <job_id>",
		Short: "Print job logs",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if stdoutOnly && stderrOnly {
				return argError("--stdout and --stderr are mutually exclusive")
			}
			c := client.New(resolveBase())
			jobID := args[0]
			if strings.HasPrefix(jobID, "-") {
				index, err := strconv.Atoi(jobID[1:])
				if err != nil || index < 1 {
					return argError("invalid job index %q", jobID)
				}
				jobID, err = c.RecentJobID(index)
				if err != nil {
					return err
				}
			}
			if all && cmd.Flags().Changed("tail") {
				return argError("--all and --tail are mutually exclusive")
			}
			if all || (page && !cmd.Flags().Changed("tail")) {
				tail = 0
			}
			opts := client.LogsOpts{
				Stdout: stdoutOnly || (!stdoutOnly && !stderrOnly),
				Stderr: stderrOnly || (!stdoutOnly && !stderrOnly),
				Tail:   tail,
				Follow: follow,
				Output: os.Stdout,
			}
			if page {
				return pageLogs(c, jobID, opts)
			}
			return c.Logs(jobID, opts)
		},
	}

	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "follow log output")
	cmd.Flags().BoolVar(&stdoutOnly, "stdout", false, "only print stdout")
	cmd.Flags().BoolVar(&stderrOnly, "stderr", false, "only print stderr")
	cmd.Flags().IntVarP(&tail, "tail", "n", 100, "show only the last N lines")
	cmd.Flags().BoolVarP(&all, "all", "a", false, "show full logs")
	cmd.Flags().BoolVarP(&page, "pager", "p", false, "show full logs in $PAGER (default: less)")
	cmd.ValidArgsFunction = completeJobIDs
	return cmd
}

func pageLogs(c *client.Client, jobID string, opts client.LogsOpts) error {
	pager := os.Getenv("PAGER")
	if pager == "" {
		pager = "less"
	}
	r, w, err := os.Pipe()
	if err != nil {
		return err
	}
	defer r.Close()
	command := exec.Command("sh", "-c", "exec "+pager)
	command.Stdin = r
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		w.Close()
		return err
	}
	opts.Output = w
	logErr := c.Logs(jobID, opts)
	w.Close()
	pagerErr := command.Wait()
	if logErr != nil {
		return logErr
	}
	return pagerErr
}

// Cobra treats -1 as a shorthand flag. Move a logs job index behind "--"
// while leaving all real flags available in their original positions.
func normalizeLogIndexArgs(argv []string) []string {
	start := -1
	for i, arg := range argv {
		if arg == "logs" {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return argv
	}
	for i := start; i < len(argv); i++ {
		arg := argv[i]
		if arg == "--" {
			return argv
		}
		if len(arg) < 2 || arg[0] != '-' {
			continue
		}
		if _, err := strconv.Atoi(arg[1:]); err != nil {
			continue
		}
		if i > start && (argv[i-1] == "-n" || argv[i-1] == "--tail" || argv[i-1] == "--base") {
			continue
		}
		out := append([]string(nil), argv[:i]...)
		out = append(out, argv[i+1:]...)
		out = append(out, "--", arg)
		return out
	}
	return argv
}

// --- job ---

func jobCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "job <job_id>",
		Short: "Show detailed job information",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := client.New(resolveBase())
			if asJSON {
				job, err := c.GetJob(args[0])
				if err != nil {
					return err
				}
				return writeJSON(job)
			}
			meta, err := c.JobRecord(args[0])
			if err != nil {
				return err
			}
			data, err := json.MarshalIndent(meta, "", "  ")
			if err != nil {
				return fmt.Errorf("marshal meta: %w", err)
			}
			fmt.Println(string(data))
			if script, err := c.JobScript(args[0]); err == nil {
				fmt.Println("\n--- script.sh ---")
				fmt.Print(script)
				if len(script) > 0 && script[len(script)-1] != '\n' {
					fmt.Println()
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the job as JSON (no script body)")
	cmd.ValidArgsFunction = completeJobIDs
	return cmd
}

// --- top ---

func topCmd() *cobra.Command {
	var plain bool
	cmd := &cobra.Command{
		Use:   "top",
		Short: "One-shot dashboard combining runners, jobs, and recently-finished jobs",
		Long: `Print a single dashboard frame combining runners, jobs, and recently-finished
jobs and exit. For auto-refresh, pipe through watch(1):

  watch -n 3 tok top

This is intentionally one-shot — watch(1) does double-buffered redraw without
flicker, so we don't reimplement it here.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			base := resolveBase()
			d, err := loadTop(client.New(base), base)
			if err != nil {
				return err
			}
			printTop(os.Stdout, d, plain)
			return nil
		},
	}
	cmd.Flags().BoolVar(&plain, "plain", false, "skip box borders and colors (auto on non-TTY)")
	return cmd
}

// --- events ---

func eventsCmd() *cobra.Command {
	var follow, asJSON bool
	var jobID, since string

	cmd := &cobra.Command{
		Use:   "events",
		Short: "Stream the shared event log (submit/start/finish/crash/...)",
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := client.EventFilter{JobID: jobID}
			if since != "" {
				if d, err := time.ParseDuration(since); err == nil {
					opts.Since = time.Now().Add(-d)
				} else if t, err := time.Parse(time.RFC3339, since); err == nil {
					opts.Since = t
				} else {
					return argError("invalid --since: %q (expected duration like 5m or RFC3339 timestamp)", since)
				}
			}
			c := client.New(resolveBase())
			events, err := c.ListEvents(opts)
			if err != nil {
				return err
			}
			for _, ev := range events {
				printEvent(ev, asJSON)
			}
			if !follow {
				return nil
			}
			return c.FollowEvents(context.Background(), opts, 500*time.Millisecond, func(ev client.Event) {
				printEvent(ev, asJSON)
			})
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "follow new events")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit raw NDJSON")
	cmd.Flags().StringVar(&jobID, "job", "", "filter to a single job ID")
	cmd.Flags().StringVar(&since, "since", "", "show events newer than DURATION (e.g. 5m) or RFC3339 timestamp")
	return cmd
}

// --- watch ---

func watchCmd() *cobra.Command {
	var interval time.Duration
	var quiet, asJSON bool
	var timeout time.Duration

	cmd := &cobra.Command{
		Use:               "watch <job_id>",
		Short:             "Block until a job reaches a terminal state; exit with the job's exit code",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeJobIDs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := client.New(resolveBase())
			opts := client.WatchOpts{Interval: interval, Timeout: timeout}
			if !quiet && !asJSON {
				opts.OnStatus = printWatchStatus(args[0])
			}
			res, err := c.Watch(args[0], opts)
			if err != nil {
				return err
			}
			if asJSON {
				if err := writeJSON(res); err != nil {
					return err
				}
			} else if !quiet {
				if res.ExitCode != nil {
					fmt.Printf("[watch %s] %s exit=%d\n", args[0], res.Status, *res.ExitCode)
				} else {
					fmt.Printf("[watch %s] %s\n", args[0], res.Status)
				}
			}
			if res.ExitCode != nil {
				os.Exit(*res.ExitCode)
			}
			if res.Status != protocol.StatusCompleted {
				os.Exit(1)
			}
			return nil
		},
	}
	cmd.Flags().DurationVarP(&interval, "interval", "i", 500*time.Millisecond, "poll interval")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "only print final status (or nothing if exit code is 0)")
	cmd.Flags().DurationVar(&timeout, "timeout", 0, "max wait before giving up (0 = forever)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the final {status, exit_code} as JSON")
	return cmd
}

func printWatchStatus(jobID string) func(string) {
	return func(status string) { fmt.Printf("[watch %s] %s\n", jobID, status) }
}

// --- stats ---

func statsCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Show CPU / RAM / GPU usage of currently running jobs across all runners",
		Long: `Show a snapshot of resource usage for every job currently running on a
runner. Each runner samples its own job once per tick (~1s) and writes
the result to workers/<id>/stats.json; this command reads those files.

Linux + NVIDIA only — on other platforms or when nvidia-smi is missing,
the GPU columns show "n/a". CPU% can exceed 100% on multi-threaded jobs
(it's the sum across cores, like ` + "`top`" + ` in irix mode).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			samples, err := client.New(resolveBase()).ListStats()
			if err != nil {
				return err
			}
			if asJSON {
				return writeJSON(samples)
			}
			fmt.Println("TOKIMEKI JOB SYSTEM")
			return printStats(os.Stdout, samples)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON for machine consumers")
	return cmd
}

// --- gc ---

func gcCmd() *cobra.Command {
	var olderThan, maxSize string
	var dryRun, asJSON bool

	cmd := &cobra.Command{
		Use:   "gc",
		Short: "Garbage-collect dead workers, old jobs, and stale tmp files",
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := client.GCOpts{DryRun: dryRun}
			if olderThan != "" {
				dur, err := parseDuration(olderThan)
				if err != nil {
					return argError("invalid --older-than: %v", err)
				}
				opts.OlderThan = dur
			}
			if maxSize != "" {
				n, err := display.ParseSize(maxSize)
				if err != nil {
					return argError("invalid --max-size: %v", err)
				}
				opts.MaxSize = n
			}
			rep, err := client.New(resolveBase()).GC(opts)
			if err != nil {
				return err
			}
			if asJSON {
				return writeJSON(rep)
			}
			printGCReport(rep)
			return nil
		},
	}

	cmd.Flags().StringVar(&olderThan, "older-than", "7d", "remove jobs older than this duration (empty = disable)")
	cmd.Flags().StringVar(&maxSize, "max-size", "", "cap total size of terminal job dirs (e.g. 10G, 500M)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print what would be removed without removing")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the GC report as JSON")
	return cmd
}

func printGCReport(rep client.GCReport) {
	verb := "removed"
	if rep.DryRun {
		verb = "would remove"
	}
	for _, item := range rep.Removed {
		switch item.Kind {
		case client.GCDeadWorker:
			fmt.Printf("%s dead worker: %s\n", verb, item.ID)
		case client.GCExpiredJob:
			fmt.Printf("%s job: %s (%s)\n", verb, item.ID, item.Status)
		case client.GCSizeCapJob:
			fmt.Printf("%s job (size cap): %s (%s, %s)\n", verb, item.ID, item.Status, display.Size(item.SizeBytes))
		}
	}
	for _, w := range rep.Warnings {
		what := map[client.GCItemKind]string{client.GCDeadWorker: "worker", client.GCTmpFile: "tmp"}[w.Kind]
		if what == "" {
			what = "job"
		}
		fmt.Fprintf(os.Stderr, "warning: remove %s %s: %v\n", what, w.ID, w.Err)
	}
	if rep.DryRun {
		fmt.Printf("GC dry-run: %d items would be removed\n", len(rep.Removed))
	} else {
		fmt.Printf("GC complete: %d items removed\n", len(rep.Removed))
	}
}

// printEvent prints one event as "<ts> <kind> key=value..." or, with
// asJSON, as one NDJSON line.
func printEvent(ev client.Event, asJSON bool) {
	if asJSON {
		_ = writeNDJSON(ev)
		return
	}
	tail := ""
	if ev.WorkerID != "" {
		tail += " worker=" + ev.WorkerID
	}
	if ev.JobID != "" {
		tail += " job=" + ev.JobID
	}
	if ev.ExitCode != nil {
		tail += fmt.Sprintf(" exit=%d", *ev.ExitCode)
	}
	if ev.Status != "" {
		tail += " status=" + ev.Status
	}
	fmt.Printf("%s %s%s\n", display.RFC3339Nano(ev.TSNs), ev.Kind, tail)
}
