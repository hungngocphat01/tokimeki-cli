package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
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
	result := actionResult{action: kind, jobID: selected.JobID}
	if kind == killAction || kind == pythonAction {
		data, err := os.ReadFile(paths.JobMeta(selected.JobID))
		if err != nil {
			result.err = fmt.Errorf("read job: %w", err)
			return result
		}
		var current protocol.JobMeta
		if err := json.Unmarshal(data, &current); err != nil {
			result.err = fmt.Errorf("read job status: %w", err)
			return result
		}
		if current.Status != protocol.StatusRunning || current.WorkerID != selected.WorkerID {
			result.err = fmt.Errorf("job is no longer running on %s", selected.WorkerID)
			return result
		}
	}
	c := client.New(paths.Base)
	switch kind {
	case killAction:
		if selected.Status != protocol.StatusRunning || selected.WorkerID == "" {
			result.err = fmt.Errorf("job is no longer running")
			break
		}
		result.err = c.Kill(selected.WorkerID, selected.JobID, 15, 30*time.Second)
	case pythonAction:
		if selected.Status != protocol.StatusRunning || selected.WorkerID == "" {
			result.err = fmt.Errorf("job is no longer running")
			break
		}
		res, err := c.Exec(selected.WorkerID, "killall python", 30*time.Second)
		result.err = err
		if err == nil && res.ExitCode != 0 {
			result.err = fmt.Errorf("killall python exited with code %d", res.ExitCode)
		}
	case cancelAction:
		result.err = c.Cancel(selected.JobID)
	}
	return result
}

func scriptLines(paths *protocol.Paths, jobID string) ([]string, error) {
	f, err := os.Open(paths.JobScript(jobID))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	const maxBytes = 1 << 20
	data, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil {
		return nil, err
	}
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
	}, string(data))
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
