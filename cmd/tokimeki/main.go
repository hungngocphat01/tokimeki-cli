// Package main provides the tokimeki CLI binary.
package main

import (
	"crypto/rand"
	"fmt"
	"os"
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
	rootCmd := &cobra.Command{
		Use:   "tokimeki",
		Short: "TOKIMEKI Runners — filesystem-based job submission system",
	}

	rootCmd.PersistentFlags().StringVar(&baseDir, "base", "", "base directory for state (default: ~/.tokimeki)")

	rootCmd.AddCommand(
		runnerCmd(),
		runnersCmd(),
		psCmd(),
		lsCmd(),
		submitCmd(),
		execCmd(),
		killCmd(),
		cancelCmd(),
		logsCmd(),
		jobCmd(),
		gcCmd(),
		eventsCmd(),
		topCmd(),
		versionCmd(),
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
				return c.PSJSON(filterWorker, showAll)
			}
			return c.PS(filterWorker, showAll)
		},
	}
	cmd.Flags().BoolVarP(&showAll, "all", "a", false, "show all jobs including finished")
	cmd.Flags().StringVarP(&filterWorker, "worker", "w", "", "filter jobs by worker ID")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of a table")
	return cmd
}

// --- ls ---

func lsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ls",
		Short: "List jobs, then runners",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := client.New(resolveBase())
			if err := c.PS("", false); err != nil {
				return err
			}
			fmt.Print("\n\n")
			return c.Workers(false)
		},
	}
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

	cmd := &cobra.Command{
		Use:   "submit [script|job_id]",
		Short: "Submit a job to the global queue",
		Long: `Submit work to the global queue.
Use either:
- a script path argument
- --command/-c for an inline command
- -r/--resubmit with a job ID (requires running daemon and --worker).`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := client.New(resolveBase())

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
			}

			if inlineCommand != "" {
				if len(args) != 0 {
					return fmt.Errorf("inline submit requires: submit -c <command>")
				}
				spec.Command = inlineCommand
				return c.DirectSubmitCommandSpec(spec, timeout)
			}

			if len(args) != 1 {
				return fmt.Errorf("script submit requires: submit <script_path>")
			}
			return c.DirectSubmitFileSpec(spec, args[0], timeout)
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
	return cmd
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

// --- kill ---

func killCmd() *cobra.Command {
	var sig int
	var timeout time.Duration

	cmd := &cobra.Command{
		Use:   "kill <worker_id> <job_id>",
		Short: "Send a signal to a running job",
		Args:  cobra.ExactArgs(2),
		ValidArgsFunction: func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			if len(args) == 0 {
				return completeWorkerIDs(nil, args, toComplete)
			}
			if len(args) == 1 {
				return completeJobIDs(nil, args, toComplete)
			}
			return nil, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			c := client.New(resolveBase())
			return c.Kill(args[0], args[1], sig, timeout)
		},
	}

	cmd.Flags().IntVar(&sig, "signal", 15, "signal number to send")
	cmd.Flags().DurationVar(&timeout, "timeout", 30*time.Second, "response timeout")
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
	var follow, stdoutOnly, stderrOnly bool
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
			return c.Logs(args[0], client.LogsOpts{
				Stdout: stdoutOnly || (!stdoutOnly && !stderrOnly),
				Stderr: stderrOnly || (!stdoutOnly && !stderrOnly),
				Tail:   tail,
				Follow: follow,
			})
		},
	}

	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "follow log output")
	cmd.Flags().BoolVar(&stdoutOnly, "stdout", false, "only print stdout")
	cmd.Flags().BoolVar(&stderrOnly, "stderr", false, "only print stderr")
	cmd.Flags().IntVarP(&tail, "tail", "n", 0, "show only the last N lines (0 = all)")
	cmd.ValidArgsFunction = completeJobIDs
	return cmd
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
	var interval time.Duration
	var once bool
	cmd := &cobra.Command{
		Use:   "top",
		Short: "Live read-only dashboard of runners and jobs",
		RunE: func(cmd *cobra.Command, args []string) error {
			c := client.New(resolveBase())
			return c.Top(client.TopOpts{Interval: interval, Once: once})
		},
	}
	cmd.Flags().DurationVarP(&interval, "interval", "i", time.Second, "refresh interval")
	cmd.Flags().BoolVar(&once, "once", false, "render one frame and exit")
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
