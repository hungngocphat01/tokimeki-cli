package main

import (
	"fmt"
	"runtime/debug"
	"strings"

	"github.com/spf13/cobra"

	engineversion "github.com/hungngocphat01/tokimeki/version"
)

// Populated at build time via -ldflags.
var (
	versionCLI       = "unknown"
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
			fmt.Printf("  version: %s\n", resolvedCLIVersion())
			fmt.Printf("  engine: %s\n", engineversion.Engine())
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

// resolvedCLIVersion prefers the -ldflags stamp (git describe), then the
// module version when installed via `go install ...@vX.Y.Z`.
func resolvedCLIVersion() string {
	v := fallback(versionCLI, "unknown")
	if v != "unknown" {
		return v
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
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
