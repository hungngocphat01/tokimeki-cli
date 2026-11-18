package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/hungngocphat01/tokimeki/client"
	"github.com/spf13/cobra"
)

// execute runs the CLI with args (without the program name) and returns the
// process exit status. With --json, a failure prints only the error envelope
// on stdout instead of Cobra's "Error:" line and usage.
func execute(args []string) int {
	root := newRootCmd()
	root.SetArgs(args)
	jsonMode := hasJSONFlag(args)
	if jsonMode {
		silence(root)
	}
	if _, err := root.ExecuteC(); err != nil {
		if jsonMode && !errors.Is(err, errSilentExit) {
			writeJSONError(err)
		}
		return 1
	}
	return 0
}

// errSilentExit makes a command exit 1 after it already printed its result.
var errSilentExit = errors.New("exit 1")

func hasJSONFlag(args []string) bool {
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		if arg == "--json" || arg == "--json=true" {
			return true
		}
	}
	return false
}

func silence(cmd *cobra.Command) {
	cmd.SilenceErrors, cmd.SilenceUsage = true, true
	for _, sub := range cmd.Commands() {
		silence(sub)
	}
}

// writeJSON prints v as indented JSON on stdout.
func writeJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// writeNDJSON prints v as one compact JSON line on stdout (for streams).
func writeNDJSON(v any) error {
	return json.NewEncoder(os.Stdout).Encode(v)
}

// writeJSONError prints the --json failure envelope:
// {"error":{"code":"<client.Code>","message":"..."}}.
// A failed multi-script submit adds "submitted": the jobs queued before the
// failure, so a caller does not resubmit them.
func writeJSONError(err error) {
	env := map[string]any{"error": map[string]string{
		"code":    string(client.CodeOf(err)),
		"message": err.Error(),
	}}
	var batch *batchError
	if errors.As(err, &batch) && len(batch.submitted) > 0 {
		env["submitted"] = batch.submitted
	}
	_ = writeJSON(env)
}

// batchError is a submit failure after some jobs were already queued.
type batchError struct {
	err       error
	submitted []client.SubmitResult
}

func (e *batchError) Error() string { return e.err.Error() }
func (e *batchError) Unwrap() error { return e.err }

// argError reports invalid command-line usage (code invalid_argument).
func argError(format string, args ...any) error {
	return &client.Error{Code: client.CodeInvalidArgument, Msg: fmt.Sprintf(format, args...)}
}
