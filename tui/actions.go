package tui

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/hungngocphat01/tokimeki/client"
	"github.com/hungngocphat01/tokimeki/protocol"
)

type action string

const (
	killAction   action = "kill"
	pythonAction action = "python"
	cancelAction action = "cancel"
)

type actionResult struct {
	action action
	jobID  string
	err    error
}

func runAction(paths *protocol.Paths, selected job, kind action) actionResult {
	result := actionResult{action: kind, jobID: selected.ID}
	c := client.New(paths.Base)
	if kind == killAction || kind == pythonAction {
		current, err := c.JobRecord(selected.ID)
		if err != nil {
			result.err = fmt.Errorf("read job: %w", err)
			return result
		}
		if current.Status != protocol.StatusRunning || current.WorkerID != selected.Worker {
			result.err = fmt.Errorf("job is no longer running on %s", selected.Worker)
			return result
		}
	}
	switch kind {
	case killAction:
		if selected.Status != protocol.StatusRunning || selected.Worker == "" {
			result.err = fmt.Errorf("job is no longer running")
			break
		}
		// KillJob also stops the group when the job is a coordinator.
		_, result.err = c.KillJob(selected.ID, 15, 30*time.Second)
	case pythonAction:
		if selected.Status != protocol.StatusRunning || selected.Worker == "" {
			result.err = fmt.Errorf("job is no longer running")
			break
		}
		res, err := c.Exec(selected.Worker, "killall python", 30*time.Second)
		result.err = err
		if err == nil && res.ExitCode != 0 {
			result.err = fmt.Errorf("killall python exited with code %d", res.ExitCode)
		}
	case cancelAction:
		_, result.err = c.Cancel(selected.ID)
	}
	return result
}

func scriptLines(paths *protocol.Paths, jobID string) ([]string, error) {
	data, err := client.New(paths.Base).JobScript(jobID)
	if err != nil {
		return nil, err
	}
	const maxBytes = 1 << 20
	if len(data) > maxBytes {
		return nil, fmt.Errorf("script exceeds 1 MiB preview limit")
	}
	// Remove terminal control characters from persisted script content before
	// passing it to the ANSI syntax formatter.
	clean := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, data)
	lexer := lexers.Get("bash")
	tokens, err := lexer.Tokenise(nil, clean)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := formatters.Get("terminal256").Format(&out, styles.Get("monokai"), tokens); err != nil {
		return nil, err
	}
	return strings.Split(strings.TrimRight(out.String(), "\n"), "\n"), nil
}
