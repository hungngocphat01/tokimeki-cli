// Package main provides the tokimeki CLI binary.
package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/ngocphat/tokimeki/client"
	"github.com/ngocphat/tokimeki/protocol"
	"github.com/ngocphat/tokimeki/registry"
	"github.com/ngocphat/tokimeki/runner"
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

// parseDuration extends time.ParseDuration with support for "Nd" notation
// (e.g. "7d" → 7*24h).
func parseDuration(s string) (time.Duration, error) {
	if strings.HasSuffix(s, "d") {
		trimmed := strings.TrimSuffix(s, "d")
		days, err := strconv.Atoi(trimmed)
		if err != nil {
			return 0, fmt.Errorf("invalid day duration: %q", s)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
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
	// Use the actual invocation name (tok, tokimeki, ...) so help text shows
	// the command the user actually typed.
	progName := filepath.Base(os.Args[0])

	rootCmd := &cobra.Command{
		Use:   progName,
		Short: "TOKIMEKI Runners — filesystem-based job submission system",
	}

	rootCmd.PersistentFlags().StringVar(&baseDir, "base", "", "base directory for state (default: ~/.tokimeki)")

	rootCmd.AddGroup(
		&cobra.Group{ID: "run", Title: "Run:"},
		&cobra.Group{ID: "inspect", Title: "Inspect:"},
		&cobra.Group{ID: "control", Title: "Control:"},
		&cobra.Group{ID: "maintain", Title: "Maintain:"},
	)

	add := func(cmd *cobra.Command, group string) *cobra.Command {
		cmd.GroupID = group
		return cmd
	}

	rootCmd.AddCommand(
		add(runnerCmd(), "run"),
		add(submitCmd(), "run"),

		add(psCmd(), "inspect"),
		add(runnersCmd(), "inspect"),
		add(lsCmd(), "inspect"),
		add(topCmd(), "inspect"),
		add(jobCmd(), "inspect"),
		add(logsCmd(), "inspect"),
		add(eventsCmd(), "inspect"),
		add(watchCmd(), "inspect"),
		add(statsCmd(), "inspect"),

		add(execCmd(), "control"),
		add(kpCmd(), "control"),
		add(killCmd(), "control"),
		add(cancelCmd(), "control"),

		add(gcCmd(), "maintain"),
		add(versionCmd(), "maintain"),
	)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

// --- runner ---

func runnerCmd() *cobra.Command {
	var workerID string
	var poll time.Duration
	var mannerPeriod time.Duration
	var capCPUs, capMemMB int

	cmd := &cobra.Command{
		Use:   "runner",
		Short: "Start the runner daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			base := resolveBase()

			if workerID == "" {
				hostname, _ := os.Hostname()
				workerID = hostname + "-" + randomHex(4)
			}

			d := runner.NewDaemon(workerID, base)
			d.SetMannerPeriod(mannerPeriod)
			d.SetCapacity(registry.WorkerCapacity{CPUs: capCPUs, MemMB: capMemMB})
			if err := d.Register(); err != nil {
				return fmt.Errorf("register: %w", err)
			}

			fmt.Printf("Runner %s started (poll=%s, manner=%s, base=%s)\n", d.WorkerID(), poll, mannerPeriod, base)

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
	cmd.Flags().IntVar(&capCPUs, "capacity-cpus", 0, "available CPU count for capacity-aware scheduling (0 = unconstrained)")
	cmd.Flags().IntVar(&capMemMB, "capacity-mem-mb", 0, "available memory in MB for capacity-aware scheduling (0 = unconstrained)")

	return cmd
}

// --- runners ---

func runnersCmd() *cobra.Command {
	var showAll, asJSON bool

	cmd := &cobra.Command{
		Use:   "runners",
		Short: "List running runners",
		RunE: func(cmd *cobra.Command, args []string) error {
			c := client.New(resolveBase())
			if asJSON {
				return c.WorkersJSON(showAll)
			}
			return c.Workers(showAll)
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
					return fmt.Errorf("use either positional worker_id or -w/--worker, not both")
				}
				filterWorker = args[0]
			}
			if asJSON {
				return c.PSJSON(filterWorker, filterName, showAll)
			}
			return c.PS(filterWorker, filterName, showAll)
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
			if err := c.PS("", filterName, false); err != nil {
				return err
			}
			fmt.Print("\n\n")
			return c.Workers(false)
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
	var name, desc string
	var wait, quiet, asJSON, live bool

	cmd := &cobra.Command{
		Use:   "submit [scripts...]",
		Short: "Submit a job to the global queue",
		Long: `Submit work to the global queue.
Use either:
- a script path argument
- --command/-c for an inline command
- -r/--resubmit with a job ID (requires running daemon and --worker).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := client.New(resolveBase())
			if live && (quiet || asJSON) {
				return fmt.Errorf("--live cannot be combined with --quiet or --json")
			}
			if live && resubmit {
				return fmt.Errorf("--live cannot be combined with --resubmit")
			}

			if resubmit {
				if len(args) != 1 {
					return fmt.Errorf("resubmit requires: submit -r -w <worker_id> <job_id>")
				}
				if inlineCommand != "" {
					return fmt.Errorf("--command cannot be used with --resubmit")
				}
				if workerID == "" {
					return fmt.Errorf("resubmit requires --worker/-w")
				}
				return c.Resubmit(workerID, args[0], timeout)
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
					return fmt.Errorf("invalid --backoff: %w", err)
				}
			}
			user := os.Getenv("USER")

			spec := protocol.JobSpec{
				TargetWorkerID: workerID,
				DependsOn:      deps,
				Priority:       priority,
				CPUs:           cpus,
				MemMB:          memMB,
				User:           user,
				Retries:        retries,
				Backoff:        backoff,
				Name:           name,
				Desc:           desc,
			}

			if quiet && asJSON {
				return fmt.Errorf("--quiet and --json are mutually exclusive")
			}
			if live {
				var paths []string
				if inlineCommand == "" {
					if len(args) == 0 {
						return fmt.Errorf("script submit requires: submit <script_path> [script_path...]")
					}
					paths = args
				} else if len(args) != 0 {
					return fmt.Errorf("inline submit requires: submit -c <command>")
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
			silent := quiet || asJSON
			var jobs []string
			if inlineCommand != "" {
				if len(args) != 0 {
					return fmt.Errorf("inline submit requires: submit -c <command>")
				}
				spec.Command = inlineCommand
				id, err := submitOne(c, spec, "", silent, asJSON, timeout)
				if err != nil {
					return err
				}
				jobs = append(jobs, id)
			} else {
				if len(args) == 0 {
					return fmt.Errorf("script submit requires: submit <script_path> [script_path...]")
				}
				targets, err := submitTargets(os.Args, cmd, len(args))
				if err != nil {
					return err
				}
				for i, path := range args {
					spec.TargetWorkerID = targets[i]
					id, err := submitOne(c, spec, path, silent, asJSON, timeout)
					if err != nil {
						return fmt.Errorf("submit %s: %w", path, err)
					}
					jobs = append(jobs, id)
				}
			}
			if !wait {
				return nil
			}
			failureCode := 0
			for _, jobID := range jobs {
				res, err := c.Watch(jobID, client.WatchOpts{})
				if err != nil {
					return err
				}
				if res.ExitCode != nil {
					fmt.Printf("[wait %s] %s exit=%d\n", jobID, res.Status, *res.ExitCode)
					if *res.ExitCode != 0 && failureCode == 0 {
						failureCode = *res.ExitCode
					}
				} else {
					fmt.Printf("[wait %s] %s\n", jobID, res.Status)
					if res.Status != protocol.StatusCompleted && failureCode == 0 {
						failureCode = 1
					}
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
	cmd.Flags().BoolVarP(&resubmit, "resubmit", "r", false, "resubmit an existing job by ID (requires running daemon)")
	cmd.Flags().StringVar(&afterCSV, "after", "", "wait for these comma-separated job IDs to complete first")
	cmd.Flags().IntVar(&priority, "priority", 0, "scheduling priority; higher runs sooner (default 0)")
	cmd.Flags().IntVar(&cpus, "cpus", 0, "required CPU count (resource hint)")
	cmd.Flags().IntVar(&memMB, "mem-mb", 0, "required memory in MB (resource hint)")
	cmd.Flags().IntVar(&retries, "retries", 0, "max retries on failed/crashed (default 0)")
	cmd.Flags().StringVar(&backoff, "backoff", "", "delay between retries (e.g. 30s, 5m)")
	cmd.Flags().StringVarP(&name, "name", "n", "", "short tag for grouping/filtering (shared, non-unique)")
	cmd.Flags().StringVar(&desc, "desc", "", "optional free-text description")
	cmd.Flags().BoolVar(&wait, "wait", false, "block until job reaches a terminal state; exit with the job's exit code")
	cmd.Flags().BoolVarP(&live, "live", "l", false, "show assignment and stream job output until completion; Ctrl-C leaves the job running")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "print only the job ID on success")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print a JSON object {job_id, position, worker, worker_acked}")
	return cmd
}

func submitOne(c *client.Client, spec protocol.JobSpec, path string, silent, asJSON bool, timeout time.Duration) (string, error) {
	if !silent {
		if path == "" {
			return c.DirectSubmitCommandSpecID(spec, timeout)
		}
		return c.DirectSubmitFileSpecID(spec, path, timeout)
	}
	var res client.SubmitResult
	var err error
	if path == "" {
		res, err = c.SubmitCommandSpec(spec, timeout)
	} else {
		res, err = c.SubmitFileSpec(spec, path, timeout)
	}
	if err != nil {
		return "", err
	}
	if asJSON {
		b, err := json.Marshal(res)
		if err != nil {
			return "", err
		}
		fmt.Println(string(b))
	} else {
		fmt.Println(res.JobID)
	}
	return res.JobID, nil
}

func submitLiveOne(c *client.Client, spec protocol.JobSpec, path string, timeout time.Duration) (client.SubmitResult, error) {
	var res client.SubmitResult
	var err error
	if path == "" {
		res, err = c.SubmitCommandSpec(spec, timeout)
	} else {
		res, err = c.SubmitFileSpec(spec, path, timeout)
	}
	if err != nil {
		return res, err
	}
	fmt.Printf("Job %s submitted to global queue\n", res.JobID)
	if res.Position > 0 {
		fmt.Printf("Global queue position: %d\n", res.Position)
	}
	if res.Worker != "" {
		fmt.Printf("Target runner: %s\n", res.Worker)
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
	reg := registry.New(protocol.NewPaths(resolveBase()))
	failureCode := 0
	for _, id := range ids {
		code, err := streamLiveJob(ctx, c, reg, id)
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

func streamLiveJob(ctx context.Context, c *client.Client, reg *registry.Registry, id string) (int, error) {
	meta, err := waitForJobStart(ctx, reg, id)
	if err != nil {
		return 0, err
	}
	if meta.StartedAt != nil && meta.WorkerID != "" {
		fmt.Printf("Job assigned to %s\n", meta.WorkerID)
	}
	fmt.Println("#### BEGIN JOB LOGS ####")
	err = c.Logs(id, client.LogsOpts{
		Follow: true, Output: os.Stdout, StderrOutput: os.Stderr,
		Context: ctx, PollInterval: 250 * time.Millisecond,
	})
	if err != nil {
		return 0, err
	}
	fmt.Println("#### END JOB LOGS ####")
	meta, err = reg.ReadJobMeta(id)
	if err != nil {
		return 0, err
	}
	message, code := liveSummary(meta, liveColorEnabled())
	fmt.Println(message)
	return code, nil
}

func waitForJobStart(ctx context.Context, reg *registry.Registry, id string) (*protocol.JobMeta, error) {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		meta, err := reg.ReadJobMeta(id)
		if err != nil {
			return nil, err
		}
		if meta != nil && (meta.Status == protocol.StatusRunning || meta.FinishedAt != nil) {
			return meta, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func liveSummary(meta *protocol.JobMeta, color bool) (string, int) {
	code := 0
	label := "Job finished successfully"
	shade := "\x1b[32m"
	if meta.Status != protocol.StatusCompleted {
		label, shade, code = "Job failed", "\x1b[31m", 1
		if meta.ExitCode != nil && *meta.ExitCode != 0 {
			code = *meta.ExitCode
		}
	}
	start, startErr := time.Parse(time.RFC3339, meta.SubmittedAt)
	if meta.StartedAt != nil {
		start, startErr = time.Parse(time.RFC3339, *meta.StartedAt)
	}
	if meta.FinishedAt != nil && startErr == nil {
		if end, err := time.Parse(time.RFC3339, *meta.FinishedAt); err == nil {
			seconds := int(end.Sub(start).Seconds())
			if seconds < 0 {
				seconds = 0
			}
			if seconds < 60 {
				label += fmt.Sprintf(" in %ds", seconds)
			} else {
				label += fmt.Sprintf(" in %dm%ds", seconds/60, seconds%60)
			}
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
	var interactive bool

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
				return c.ExecInteractive(workerID, command, timeout)
			}
			return c.Exec(workerID, command, timeout)
		},
	}

	cmd.Flags().DurationVar(&timeout, "timeout", 30*time.Second, "response timeout")
	cmd.Flags().BoolVarP(&interactive, "interactive", "i", false, "stream stdin/stdout/stderr via FIFOs (line-buffered, no PTY)")
	return cmd
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
			return client.New(resolveBase()).Exec(args[0], "killall python", timeout)
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
			if worker != "" {
				return c.Kill(worker, jobID, sig, timeout)
			}
			return c.KillJob(jobID, sig, timeout)
		},
	}

	cmd.Flags().IntVar(&sig, "signal", 15, "signal number to send")
	cmd.Flags().DurationVar(&timeout, "timeout", 30*time.Second, "response timeout")
	cmd.Flags().StringVar(&worker, "worker", "", "explicit worker ID (override meta lookup)")
	return cmd
}

// --- cancel ---

func cancelCmd() *cobra.Command {
	return &cobra.Command{
		Use:               "cancel <job_id>",
		Short:             "Cancel a queued job",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeJobIDs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := client.New(resolveBase())
			return c.Cancel(args[0])
		},
	}
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
				return fmt.Errorf("--stdout and --stderr are mutually exclusive")
			}
			c := client.New(resolveBase())
			jobID := args[0]
			if strings.HasPrefix(jobID, "-") {
				index, err := strconv.Atoi(jobID[1:])
				if err != nil || index < 1 {
					return fmt.Errorf("invalid job index %q", jobID)
				}
				jobID, err = c.RecentJobID(index)
				if err != nil {
					return err
				}
			}
			if all && cmd.Flags().Changed("tail") {
				return fmt.Errorf("--all and --tail are mutually exclusive")
			}
			if all || (page && !cmd.Flags().Changed("tail")) {
				tail = 0
			}
			opts := client.LogsOpts{
				Stdout: stdoutOnly || (!stdoutOnly && !stderrOnly),
				Stderr: stderrOnly || (!stdoutOnly && !stderrOnly),
				Tail:   tail,
				Follow: follow,
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
			return c.Job(args[0], asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON only (omit script body)")
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
			c := client.New(resolveBase())
			return c.Top(client.TopOpts{Plain: plain})
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
			opts := client.EventsOpts{
				JobID:  jobID,
				Follow: follow,
				JSON:   asJSON,
			}
			if since != "" {
				if d, err := time.ParseDuration(since); err == nil {
					opts.Since = time.Now().Add(-d)
				} else if t, err := time.Parse(time.RFC3339, since); err == nil {
					opts.Since = t
				} else {
					return fmt.Errorf("invalid --since: %q (expected duration like 5m or RFC3339 timestamp)", since)
				}
			}
			c := client.New(resolveBase())
			return c.Events(opts)
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
	var quiet bool
	var timeout time.Duration

	cmd := &cobra.Command{
		Use:               "watch <job_id>",
		Short:             "Block until a job reaches a terminal state; exit with the job's exit code",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeJobIDs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := client.New(resolveBase())
			res, err := c.Watch(args[0], client.WatchOpts{
				Interval: interval,
				Quiet:    quiet,
				Timeout:  timeout,
			})
			if err != nil {
				return err
			}
			if !quiet {
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
	return cmd
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
			c := client.New(resolveBase())
			if asJSON {
				return c.StatsJSON()
			}
			return c.Stats()
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON for machine consumers")
	return cmd
}

// --- gc ---

func gcCmd() *cobra.Command {
	var olderThan, maxSize string
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "gc",
		Short: "Garbage-collect dead workers, old jobs, and stale tmp files",
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := client.GCOpts{DryRun: dryRun}
			if olderThan != "" {
				dur, err := parseDuration(olderThan)
				if err != nil {
					return fmt.Errorf("invalid --older-than: %w", err)
				}
				opts.OlderThan = dur
			}
			if maxSize != "" {
				n, err := client.ParseSize(maxSize)
				if err != nil {
					return fmt.Errorf("invalid --max-size: %w", err)
				}
				opts.MaxSize = n
			}
			c := client.New(resolveBase())
			return c.GC(opts)
		},
	}

	cmd.Flags().StringVar(&olderThan, "older-than", "7d", "remove jobs older than this duration (empty = disable)")
	cmd.Flags().StringVar(&maxSize, "max-size", "", "cap total size of terminal job dirs (e.g. 10G, 500M)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print what would be removed without removing")
	return cmd
}
