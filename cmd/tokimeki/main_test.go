package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/hungngocphat01/tokimeki/client"
	"github.com/hungngocphat01/tokimeki/protocol"
	"github.com/hungngocphat01/tokimeki/shell/session"
)

// The runner builds this command line in the engine; the CLI must accept it.
func TestShellDaemonAcceptsRunnerArgs(t *testing.T) {
	cmd := shellDaemonCmd()
	args := session.DaemonArgs("/base", "w1", "abc")
	if cmd.Use != args[0] {
		t.Fatalf("command = %q, runner starts %q", cmd.Use, args[0])
	}
	cmd.Flags().String(session.FlagBase, "", "") // a persistent root flag in main
	if err := cmd.ParseFlags(args[1:]); err != nil {
		t.Fatal(err)
	}
	if err := cmd.ValidateRequiredFlags(); err != nil {
		t.Fatal(err)
	}
}

func TestKPDispatchesKillallPythonToNamedRunner(t *testing.T) {
	oldBase := baseDir
	baseDir = t.TempDir()
	t.Cleanup(func() { baseDir = oldBase })
	paths := protocol.NewPaths(baseDir)
	for _, dir := range []string{paths.TmpDir(), paths.WorkerInbox("node1"), paths.WorkerOutbox("node1")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	requestCh := make(chan protocol.Request, 1)
	errorCh := make(chan error, 1)
	go func() {
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			entries, err := os.ReadDir(paths.WorkerInbox("node1"))
			if err != nil {
				errorCh <- err
				return
			}
			if len(entries) == 0 {
				time.Sleep(10 * time.Millisecond)
				continue
			}
			data, err := os.ReadFile(filepath.Join(paths.WorkerInbox("node1"), entries[0].Name()))
			if err != nil {
				errorCh <- err
				return
			}
			var req protocol.Request
			if err := json.Unmarshal(data, &req); err != nil {
				errorCh <- err
				return
			}
			response, _ := json.Marshal(protocol.Response{ReqID: req.ReqID, OK: true})
			if err := os.WriteFile(filepath.Join(paths.WorkerOutbox("node1"), req.ReqID+".resp"), response, 0o644); err != nil {
				errorCh <- err
				return
			}
			requestCh <- req
			return
		}
		errorCh <- fmt.Errorf("no request received")
	}()
	cmd := kpCmd()
	cmd.SetArgs([]string{"node1", "--timeout", "2s"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-errorCh:
		t.Fatal(err)
	case req := <-requestCh:
		if req.Type != protocol.TypeExec || req.Payload.Command != "killall python" {
			t.Fatalf("request = %+v", req)
		}
	}
}

func TestLiveSummary(t *testing.T) {
	started := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	job := client.Job{Status: protocol.StatusCompleted, SubmittedAtNs: started.Add(-time.Hour).UnixNano(),
		StartedAtNs: started.UnixNano(), FinishedAtNs: started.Add(61*time.Minute + 30*time.Second).UnixNano()}
	message, code := liveSummary(job, false)
	if message != "Job finished successfully in 61m30s" || code != 0 {
		t.Fatalf("success: %q, %d", message, code)
	}
	failedCode := 7
	job.Status, job.ExitCode = protocol.StatusFailed, &failedCode
	message, code = liveSummary(job, true)
	if message != "\x1b[31mJob failed in 61m30s\x1b[0m" || code != 7 {
		t.Fatalf("failure: %q, %d", message, code)
	}
	job.StartedAtNs = 0 // never started: measured from submission
	if message, _ = liveSummary(job, false); message != "Job failed in 121m30s" {
		t.Fatalf("unstarted: %q", message)
	}
}

func TestParseTTL(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  time.Duration
	}{
		{"7d", 7 * 24 * time.Hour},
		{"1h", time.Hour},
		{"6d23h", 6*24*time.Hour + 23*time.Hour},
		{"25h", 25 * time.Hour},
	} {
		got, err := parseTTL(tc.input)
		if err != nil || got != tc.want {
			t.Errorf("parseTTL(%q) = %s, %v; want %s", tc.input, got, err, tc.want)
		}
	}
	for _, input := range []string{"", "0h", "1d30m", "1d-2h", "-1d25h", "1d1d", "word"} {
		if _, err := parseTTL(input); err == nil {
			t.Errorf("parseTTL(%q) unexpectedly succeeded", input)
		}
	}
}

func TestSubmitTargets(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want []string
	}{
		{[]string{"tokimeki", "submit", "a.sh", "b.sh"}, []string{"", ""}},
		{[]string{"tokimeki", "submit", "-w", "node1", "a.sh", "b.sh"}, []string{"node1", ""}},
		{[]string{"tokimeki", "submit", "-w", "node1", "a.sh", "-w", "node2", "b.sh"}, []string{"node1", "node2"}},
		{[]string{"tokimeki", "submit", "-w", "node1", "a.sh", "-w", "node1", "b.sh"}, []string{"node1", "node1"}},
		{[]string{"tokimeki", "submit", "--name", "trial", "--worker=node1", "a.sh", "--priority", "3", "b.sh"}, []string{"node1", ""}},
	} {
		got, err := submitTargets(tc.args, submitCmd(), len(tc.want))
		if err != nil {
			t.Fatalf("%v: %v", tc.args, err)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%v: got %v, want %v", tc.args, got, tc.want)
		}
	}
}

func TestNormalizeLogIndexArgs(t *testing.T) {
	in := []string{"tokimeki", "logs", "-2", "-a"}
	want := []string{"tokimeki", "logs", "-a", "--", "-2"}
	if got := normalizeLogIndexArgs(in); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestParseConfigSets(t *testing.T) {
	got, err := parseConfigSets([]string{"compute-threads", "manner-period=2h", "x"}, []string{"2", "y"})
	want := map[string]string{"compute-threads": "2", "manner-period": "2h", "x": "y"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("parseConfigSets = %v, %v", got, err)
	}
	for _, tc := range []struct{ sets, args []string }{
		{[]string{"compute-threads"}, nil},                        // no value
		{nil, []string{"2"}},                                      // stray argument
		{[]string{"compute-threads=1", "compute-threads=2"}, nil}, // twice
	} {
		if _, err := parseConfigSets(tc.sets, tc.args); client.CodeOf(err) != client.CodeInvalidArgument {
			t.Errorf("parseConfigSets(%v, %v) err = %v", tc.sets, tc.args, err)
		}
	}
}
