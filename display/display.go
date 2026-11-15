// Package display holds the CLI's presentation helpers: status glyphs and
// colors, terminal detection, and string fitting. Unit formatting lives in
// units.go. Nothing here reads tokimeki state.
package display

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/hungngocphat01/tokimeki/client"
	"github.com/hungngocphat01/tokimeki/protocol"
)

// ANSI escape sequences.
const (
	Reset     = "\x1b[0m"
	LightGray = "\x1b[90m"
	Cyan      = "\x1b[36m"
	Yellow    = "\x1b[33m"
	Green     = "\x1b[32m"
	Red       = "\x1b[31m"
	Magenta   = "\x1b[35m"
	Bold      = "\x1b[1m"
)

// StdoutIsTTY reports whether stdout is a terminal and NO_COLOR is unset.
func StdoutIsTTY() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	info, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}

// UnicodeSupported reports whether the locale advertises UTF-8. It gates
// decorative glyphs, falling back to ASCII on terminals that would otherwise
// render mojibake.
func UnicodeSupported() bool {
	for _, env := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		v := strings.ToUpper(os.Getenv(env))
		if strings.Contains(v, "UTF-8") || strings.Contains(v, "UTF8") {
			return true
		}
	}
	return false
}

// VisibleLen returns the printable width of s, ignoring ANSI escape sequences.
func VisibleLen(s string) int {
	n, in := 0, false
	for _, r := range s {
		if r == 0x1b {
			in = true
			continue
		}
		if in {
			if r == 'm' {
				in = false
			}
			continue
		}
		n++
	}
	return n
}

// StatusGlyph returns a 1-cell icon for a status, or "" when Unicode isn't
// usable. Glyphs chosen to be reliably narrow across common terminal fonts.
func StatusGlyph(status string) string {
	if !UnicodeSupported() {
		return ""
	}
	switch status {
	case protocol.StatusRunning:
		return "▶"
	case protocol.StatusQueued:
		return "◌"
	case protocol.StatusCompleted:
		return "✓"
	case protocol.StatusFailed:
		return "✗"
	case protocol.StatusKilled:
		return "†"
	case protocol.StatusCrashed:
		return "‡"
	case client.StatusStopped:
		return "○"
	}
	return ""
}

// StatusDisplay returns a presentation form like "▶ running" (Unicode
// locales) or just "running" (ASCII fallback). Compact mode returns a single
// glyph or letter.
func StatusDisplay(status string, compact bool) string {
	g := StatusGlyph(status)
	if compact {
		if g != "" {
			return g
		}
		if status != "" {
			return strings.ToUpper(status[:1])
		}
		return "?"
	}
	if g != "" {
		return g + " " + status
	}
	return status
}

// StatusGroup maps a status to the one-letter group ps colors by.
func StatusGroup(status string) string {
	switch status {
	case protocol.StatusRunning:
		return "R"
	case protocol.StatusQueued:
		return "Q"
	case protocol.StatusKilled, protocol.StatusCrashed:
		return "D"
	case client.StatusStopped:
		return "S"
	case protocol.StatusCompleted:
		return "C"
	case protocol.StatusFailed:
		return "F"
	case "":
		return "?"
	default:
		return strings.ToUpper(status[:1])
	}
}

// GroupColor returns the ANSI prefix for a status group, or "" for none.
func GroupColor(group string) string {
	switch group {
	case "R":
		return Cyan
	case "Q":
		return Yellow
	case "C":
		return Green
	case "F":
		return Red
	case "D": // killed/crashed
		return Magenta
	case "S": // stopped
		return LightGray
	}
	return ""
}

// StatusColor colors terminal statuses (top's recent panel).
func StatusColor(status string) string {
	switch status {
	case protocol.StatusCompleted:
		return Green
	case protocol.StatusFailed:
		return Red
	case protocol.StatusKilled, protocol.StatusCrashed:
		return Magenta
	}
	return ""
}

// SmartCommand collapses long absolute paths inside common commands so the
// table column shows the script *name* instead of "bash /home/.../truncated…".
// Patterns handled:
//   - whole command is a path:           /a/b/c/run.sh           → run.sh
//   - interpreter + path:                bash /a/b/c/train.sh    → bash train.sh
//   - interpreter + path + args:         python /a/b/eval.py -e5 → python eval.py -e5
//   - flags before path:                 python -u /a/b/eval.py  → python -u eval.py
//
// Returns the original command unchanged when no path is detected.
func SmartCommand(command string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		return command
	}
	if looksLikeScriptPath(command) {
		return filepath.Base(command)
	}
	fields := strings.Fields(command)
	if len(fields) < 2 {
		return command
	}
	// Only collapse when the first token is a known scripting-language launcher.
	// Avoids mangling commands like `myapp /etc/config.toml` where the path
	// is data, not a script.
	if !knownInterpreters[filepath.Base(fields[0])] {
		return command
	}
	for i := 1; i < len(fields); i++ {
		if strings.HasPrefix(fields[i], "-") {
			continue // skip interpreter flags (-u, -m, -c, …)
		}
		if looksLikeScriptPath(fields[i]) {
			fields[i] = filepath.Base(fields[i])
		}
		break // only collapse the script arg; remaining args are user params
	}
	return strings.Join(fields, " ")
}

// knownInterpreters lists launchers we can confidently rewrite. Conservative
// on purpose — adding a tool that takes a config-file argument would break
// the rewrite. Add new entries only after confirming the second positional
// arg is the script.
var knownInterpreters = map[string]bool{
	"bash": true, "sh": true, "zsh": true, "fish": true, "dash": true,
	"python": true, "python2": true, "python3": true,
	"node": true, "deno": true, "bun": true,
	"ruby": true, "perl": true, "lua": true, "Rscript": true,
}

func looksLikeScriptPath(command string) bool {
	if command == "" {
		return false
	}
	if strings.ContainsAny(command, " \t\n") {
		return false
	}
	ext := filepath.Ext(command)
	return strings.Contains(command, string(os.PathSeparator)) || ext == ".sh" || ext == ".bash"
}

// Truncate cuts s to n bytes, ending in "..." when cut.
func Truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}
