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
	"github.com/ngocphat/tokimeki/runner"
	"github.com/spf13/cobra"
)

// baseDir is bound to the --base persistent flag.
var baseDir string

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
		submitCmd(),
		execCmd(),
		killCmd(),
		cancelCmd(),
		logsCmd(),
		jobCmd(),
		gcCmd(),
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

	return cmd
}

// --- runners ---

func runnersCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "runners",
		Short: "List all registered runners",
		RunE: func(cmd *cobra.Command, args []string) error {
			c := client.New(resolveBase())
			return c.Workers()
		},
	}
}

// --- ps ---

func psCmd() *cobra.Command {
	var filterWorker string
	var showAll bool

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
			return c.PS(filterWorker, showAll)
		},
	}
	cmd.Flags().BoolVarP(&showAll, "all", "a", false, "show all jobs including finished")
	cmd.Flags().StringVarP(&filterWorker, "worker", "w", "", "filter jobs by worker ID")
	return cmd
}

// --- submit ---

func submitCmd() *cobra.Command {
	var timeout time.Duration
	var resubmit bool
	var inlineCommand string
	var workerID string

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

			if inlineCommand != "" {
				if len(args) != 0 {
					return fmt.Errorf("inline submit requires: submit -c <command>")
				}
				return c.DirectSubmitCommand(workerID, inlineCommand, timeout)
			}

			if len(args) != 1 {
				return fmt.Errorf("script submit requires: submit <script_path>")
			}
			return c.DirectSubmitFile(workerID, args[0], timeout)
		},
	}

	cmd.Flags().DurationVar(&timeout, "timeout", 30*time.Second, "response timeout")
	cmd.Flags().StringVarP(&workerID, "worker", "w", "", "target worker ID (optional)")
	cmd.Flags().StringVarP(&inlineCommand, "command", "c", "", "inline command to submit to the global queue")
	cmd.Flags().BoolVarP(&resubmit, "resubmit", "r", false, "resubmit an existing job by ID (requires running daemon)")
	return cmd
}

// --- exec ---

func execCmd() *cobra.Command {
	var timeout time.Duration

	cmd := &cobra.Command{
		Use:   "exec <worker_id> <command...>",
		Short: "Run a command immediately on a target runner and stream output",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := client.New(resolveBase())
			workerID := args[0]
			command := strings.Join(args[1:], " ")
			return c.Exec(workerID, command, timeout)
		},
	}

	cmd.Flags().DurationVar(&timeout, "timeout", 30*time.Second, "response timeout")
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
		Use:   "cancel <job_id>",
		Short: "Cancel a queued job",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := client.New(resolveBase())
			return c.Cancel(args[0])
		},
	}
}

// --- logs ---

func logsCmd() *cobra.Command {
	var follow bool

	cmd := &cobra.Command{
		Use:   "logs <job_id>",
		Short: "Print job logs",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := client.New(resolveBase())
			return c.Logs(args[0], follow)
		},
	}

	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "follow log output")
	return cmd
}

// --- job ---

func jobCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "job <job_id>",
		Short: "Show detailed job information",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := client.New(resolveBase())
			return c.Job(args[0])
		},
	}
}

// --- gc ---

func gcCmd() *cobra.Command {
	var olderThan string

	cmd := &cobra.Command{
		Use:   "gc",
		Short: "Garbage-collect dead workers, old jobs, and stale tmp files",
		RunE: func(cmd *cobra.Command, args []string) error {
			dur, err := parseDuration(olderThan)
			if err != nil {
				return fmt.Errorf("invalid --older-than: %w", err)
			}
			c := client.New(resolveBase())
			return c.GC(dur)
		},
	}

	cmd.Flags().StringVar(&olderThan, "older-than", "7d", "remove jobs older than this duration")
	return cmd
}
