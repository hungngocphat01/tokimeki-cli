package main

import (
	"os"
	"path/filepath"

	"github.com/hungngocphat01/tokimeki/debug"
	"github.com/hungngocphat01/tokimeki/shell/session"
	"github.com/spf13/cobra"
)

// newRootCmd builds the command tree. Tests run commands through it.
func newRootCmd() *cobra.Command {
	// Use the actual invocation name (tok, tokimeki, ...) so help text shows
	// the command the user actually typed.
	progName := filepath.Base(os.Args[0])

	var verbose bool
	rootCmd := &cobra.Command{
		Use:   progName,
		Short: "TOKIMEKI Runners — filesystem-based job submission system",
		PersistentPreRun: func(*cobra.Command, []string) {
			if verbose {
				debug.SetOutput(os.Stderr)
			}
		},
	}

	rootCmd.PersistentFlags().StringVar(&baseDir, session.FlagBase, "", "base directory for state (default: ~/.tokimeki)")
	rootCmd.PersistentFlags().BoolVar(&verbose, "verbose", false, "print engine debug logs on stderr")

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
		add(tuiCmd(), "inspect"),

		add(shellCmd(), "control"),
		add(execCmd(), "control"),
		add(kpCmd(), "control"),
		add(killCmd(), "control"),
		add(cancelCmd(), "control"),

		add(gcCmd(), "maintain"),
		add(versionCmd(), "maintain"),
		shellDaemonCmd(),
	)

	return rootCmd
}
