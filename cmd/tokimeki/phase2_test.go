package main

import (
	"encoding/json"
	"testing"

	"github.com/hungngocphat01/tokimeki/client"
	"github.com/hungngocphat01/tokimeki/debug"
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
	t.Cleanup(func() { debug.SetOutput(nil) })
	if _, code := runJSON(t, t.TempDir(), "--verbose", "ps", "--json"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !debug.Enabled() {
		t.Fatal("--verbose did not enable debug output")
	}
}
