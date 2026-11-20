package main

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/hungngocphat01/tokimeki/client"
	"github.com/hungngocphat01/tokimeki/notice"
	"github.com/hungngocphat01/tokimeki/protocol"
)

func TestSubmitCoordinatorJSON(t *testing.T) {
	base := t.TempDir()
	out, code := runJSON(t, base, "submit", "-C", "-g", "exp", "-c", "true", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	var res client.SubmitResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	meta, err := client.New(base).JobRecord(res.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Kind != protocol.KindCoordinator || meta.Name != "exp" {
		t.Fatalf("meta kind %q name %q", meta.Kind, meta.Name)
	}
}

func TestSubmitNameIsGroupAlias(t *testing.T) {
	base := t.TempDir()
	out, code := runJSON(t, base, "submit", "-n", "exp", "-c", "true", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	var res client.SubmitResult
	_ = json.Unmarshal([]byte(out), &res)
	if meta, _ := client.New(base).JobRecord(res.JobID); meta.Name != "exp" {
		t.Fatalf("name = %q", meta.Name)
	}
}

func TestSubmitGroupNameConflict(t *testing.T) {
	out, code := runJSON(t, t.TempDir(), "submit", "-g", "a", "-n", "b", "-c", "true", "--json")
	if gotCode, _ := errorEnvelope(t, out); code != 1 || gotCode != "invalid_argument" {
		t.Fatalf("exit %d code %q: %s", code, gotCode, out)
	}
}

func TestRunnerRejectsZeroThreads(t *testing.T) {
	if code := execute([]string{"--base", t.TempDir(), "runner", "--compute-threads", "0", "--manner-period", "1ms"}); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
}

func TestVerboseEnablesDebug(t *testing.T) {
	t.Cleanup(func() { notice.SetHandler(nil, false) })
	if _, code := runJSON(t, t.TempDir(), "--verbose", "ps", "--json"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !notice.DebugEnabled() {
		t.Fatal("--verbose did not enable debug output")
	}
}

func TestGroupMissingJSONEnvelope(t *testing.T) {
	out, code := runJSON(t, t.TempDir(), "group", "nope", "--json")
	if gotCode, msg := errorEnvelope(t, out); code != 1 || gotCode != "not_found" || msg != `group "nope" has no jobs` {
		t.Fatalf("exit %d code %q msg %q", code, gotCode, msg)
	}
}

func TestCancelCoordinatorJSONHasCascade(t *testing.T) {
	base := t.TempDir()
	c := client.New(base)
	coord, err := c.Submit(client.SubmitRequest{ABI: protocol.ExecABI, Type: protocol.ExecBash, Inline: "true", Coordinator: true, Group: "g"}, client.SubmitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	child, err := c.Submit(client.SubmitRequest{ABI: protocol.ExecABI, Type: protocol.ExecBash, Inline: "true", Group: "g"}, client.SubmitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	out, code := runJSON(t, base, "cancel", coord.JobID, "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	var res client.CancelResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if res.JobID != coord.JobID || res.Cascade == nil || len(res.Cascade.Cancelled) != 1 || res.Cascade.Cancelled[0] != child.JobID {
		t.Fatalf("result = %s", out)
	}
}

func TestKillJSONKeepsKeys(t *testing.T) {
	base := t.TempDir()
	c := client.New(base)
	if err := os.MkdirAll(c.Paths().JobDir("j"), 0o755); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(protocol.JobMeta{JobID: "j", Status: protocol.StatusQueued, SubmittedAt: protocol.Now()})
	if err := os.WriteFile(c.Paths().JobMeta("j"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	// A queued job has no runner: kill fails with invalid_state, keys of the
	// success shape are covered by the engine's KillResult tags.
	out, code := runJSON(t, base, "kill", "j", "--json")
	if gotCode, _ := errorEnvelope(t, out); code != 1 || gotCode != "invalid_state" {
		t.Fatalf("exit %d code %q", code, gotCode)
	}
	b, _ := json.Marshal(client.KillResult{JobID: "j", Worker: "w", Signal: 15})
	if string(b) != `{"job_id":"j","worker_id":"w","signal":15}` {
		t.Fatalf("KillResult json = %s", b)
	}
}
