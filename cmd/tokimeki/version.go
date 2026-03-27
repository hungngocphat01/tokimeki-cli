package main

import (
	"fmt"
	"runtime/debug"
	"strings"

	"github.com/spf13/cobra"
)

// Populated at build time via -ldflags.
var (
	versionCommit    = "unknown"
	versionBranch    = "unknown"
	versionBuildTime = "unknown"
)

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print build/version information",
		Run: func(cmd *cobra.Command, args []string) {
			commit, branch, buildTime := resolvedBuildInfo()
			fmt.Printf("tokimeki\n")
			fmt.Printf("  commit: %s\n", commit)
			fmt.Printf("  branch: %s\n", branch)
			fmt.Printf("  build_time: %s\n", buildTime)
		},
	}
}

func resolvedBuildInfo() (commit, branch, buildTime string) {
	commit = fallback(versionCommit, "unknown")
	branch = fallback(versionBranch, "unknown")
	buildTime = fallback(versionBuildTime, "unknown")

	if info, ok := debug.ReadBuildInfo(); ok {
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				if commit == "unknown" && s.Value != "" {
					commit = s.Value
				}
			case "vcs.time":
				if buildTime == "unknown" && s.Value != "" {
					buildTime = s.Value
				}
			case "vcs.branch":
				if branch == "unknown" && s.Value != "" {
					branch = s.Value
				}
			}
		}
	}

	commit = shortCommit(commit)
	return commit, branch, buildTime
}

func fallback(v, d string) string {
	if strings.TrimSpace(v) == "" {
		return d
	}
	return v
}

func shortCommit(commit string) string {
	if len(commit) > 12 {
		return commit[:12]
	}
	return commit
}
