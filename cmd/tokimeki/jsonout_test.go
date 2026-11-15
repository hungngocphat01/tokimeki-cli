package main

import (
	"encoding/json"
	"os"
	"testing"
)

// runJSON runs the CLI the way main does and returns stdout and the exit
// status.
func runJSON(t *testing.T, base string, args ...string) (string, int) {
	t.Helper()
	t.Setenv("NO_COLOR", "1")
	outR, outW, _ := os.Pipe()
	prevOut := os.Stdout
	os.Stdout = outW
	outC := drain(outR)
	code := execute(append([]string{"--base", base}, args...))
	os.Stdout = prevOut
	outW.Close()
	return <-outC, code
}

func errorEnvelope(t *testing.T, out string) (string, string) {
	t.Helper()
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("stdout is not a JSON envelope: %v\n%s", err, out)
	}
	return env.Error.Code, env.Error.Message
}

func TestJSONErrorEnvelope(t *testing.T) {
	out, code := runJSON(t, copyFixture(t), "job", "nope", "--json")
	gotCode, msg := errorEnvelope(t, out)
	if code != 1 || gotCode != "not_found" || msg != "job nope not found" {
		t.Fatalf("exit=%d code=%q msg=%q\n%s", code, gotCode, msg, out)
	}
}

func TestJSONCancelInvalidState(t *testing.T) {
	out, code := runJSON(t, copyFixture(t), "cancel", "a0000001", "--json")
	if gotCode, _ := errorEnvelope(t, out); code != 1 || gotCode != "invalid_state" {
		t.Fatalf("exit=%d code=%q\n%s", code, gotCode, out)
	}
}

func TestJSONSchemaKeys(t *testing.T) {
	base := copyFixture(t)
	for _, tc := range []struct {
		args []string
		keys []string
	}{
		{[]string{"ps", "-a", "--json"}, []string{"job_id", "worker_id", "submitted_at_ns", "elapsed_secs", "exit_code", "effective_status"}},
		{[]string{"runners", "-a", "--json"}, []string{"worker_id", "alive", "has_info"}},
		{[]string{"stats", "--json"}, []string{"worker_id", "gpu_available", "sampled_at_ns", "stale"}},
	} {
		out, code := runJSON(t, base, tc.args...)
		var rows []map[string]any
		if err := json.Unmarshal([]byte(out), &rows); err != nil || code != 0 || len(rows) == 0 {
			t.Fatalf("%v: exit=%d err=%v\n%s", tc.args, code, err, out)
		}
		for _, key := range tc.keys {
			if _, ok := rows[0][key]; !ok {
				t.Errorf("%v: first row lacks %q: %v", tc.args, key, rows[0])
			}
		}
	}
	out, code := runJSON(t, base, "job", "a0000001", "--json")
	var job map[string]any
	if err := json.Unmarshal([]byte(out), &job); err != nil || code != 0 || job["job_id"] != "a0000001" || job["started_at_ns"] == nil {
		t.Fatalf("job --json: exit=%d err=%v\n%s", code, err, out)
	}
}

func TestJSONActions(t *testing.T) {
	base := copyFixture(t)
	out, code := runJSON(t, base, "cancel", "a0000006", "--json")
	if code != 0 || out != "{\n  \"job_id\": \"a0000006\"\n}\n" {
		t.Fatalf("cancel: exit=%d\n%s", code, out)
	}
	out, code = runJSON(t, base, "gc", "--dry-run", "--older-than", "", "--max-size", "1K", "--json")
	var rep struct {
		DryRun  bool             `json:"dry_run"`
		Removed []map[string]any `json:"removed"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil || code != 0 || !rep.DryRun || len(rep.Removed) == 0 || rep.Removed[0]["kind"] != "size_cap_job" {
		t.Fatalf("gc: exit=%d err=%v\n%s", code, err, out)
	}
	out, code = runJSON(t, base, "submit", "-c", "echo hi", "--json")
	var sub map[string]any
	if err := json.Unmarshal([]byte(out), &sub); err != nil || code != 0 || sub["job_id"] == "" || sub["position"] == nil {
		t.Fatalf("submit: exit=%d err=%v\n%s", code, err, out)
	}
	out, code = runJSON(t, base, "submit", "-r", "a0000001", "--json")
	if err := json.Unmarshal([]byte(out), &sub); err != nil || code != 0 || sub["job_id"] == "a0000001" {
		t.Fatalf("resubmit: exit=%d err=%v\n%s", code, err, out)
	}
}

func TestJSONEventsAreNDJSON(t *testing.T) {
	out, code := runJSON(t, copyFixture(t), "events", "--json")
	lines := 0
	for _, line := range splitLines(out) {
		var ev map[string]any
		if err := json.Unmarshal([]byte(line), &ev); err != nil || ev["ts_ns"] == nil {
			t.Fatalf("line %q: %v", line, err)
		}
		lines++
	}
	if code != 0 || lines == 0 {
		t.Fatalf("exit=%d lines=%d", code, lines)
	}
}

func TestJSONRejectsHumanOnlyModes(t *testing.T) {
	base := copyFixture(t)
	for _, args := range [][]string{
		{"submit", "-c", "true", "--json", "--live"},
		{"submit", "-c", "true", "--json", "--quiet"},
		{"exec", "-i", "w-gpu", "true", "--json"},
	} {
		out, code := runJSON(t, base, args...)
		if gotCode, _ := errorEnvelope(t, out); code != 1 || gotCode != "invalid_argument" {
			t.Errorf("%v: exit=%d code=%q", args, code, gotCode)
		}
	}
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			if i > start {
				out = append(out, s[start:i])
			}
			start = i + 1
		}
	}
	return out
}
