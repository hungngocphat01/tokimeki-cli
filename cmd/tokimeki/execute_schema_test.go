package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hungngocphat01/tokimeki/client"
	"github.com/hungngocphat01/tokimeki/protocol"
)

// runFull runs the CLI through execute() with stdin, returning stdout,
// stderr and the exit code.
func runFull(t *testing.T, base, stdin string, args ...string) (string, string, int) {
	t.Helper()
	t.Setenv("NO_COLOR", "1")
	inR, inW, _ := os.Pipe()
	outR, outW, _ := os.Pipe()
	errR, errW, _ := os.Pipe()
	go func() {
		_, _ = inW.WriteString(stdin)
		inW.Close()
	}()
	prevIn, prevOut, prevErr := os.Stdin, os.Stdout, os.Stderr
	os.Stdin, os.Stdout, os.Stderr = inR, outW, errW
	outC, errC := drain(outR), drain(errR)
	argv := append([]string{"--base", base}, args...)
	prevArgs := os.Args
	os.Args = append([]string{"tok"}, argv...)
	code := execute(argv)
	os.Args = prevArgs
	os.Stdin, os.Stdout, os.Stderr = prevIn, prevOut, prevErr
	outW.Close()
	errW.Close()
	return <-outC, <-errC, code
}

func submittedID(t *testing.T, out string) string {
	t.Helper()
	var res client.SubmitResult
	if err := json.Unmarshal([]byte(out), &res); err != nil || res.JobID == "" {
		t.Fatalf("submit output %q: %v", out, err)
	}
	return res.JobID
}

func TestSubmitFromJSONStdinAndFile(t *testing.T) {
	base := t.TempDir()
	body := `{"abi": 2, "type": "python-uv", "entry": "evident.train:run", "project": "/p", "cwd": "/p",
	  "arg": {"lr": 0.01}, "group": "sweep", "desc": "lr0.01", "retries": 1}`
	out, errOut, code := runFull(t, base, body, "submit", "--from-json", "-", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s %s", code, out, errOut)
	}
	fromStdin := submittedID(t, out)

	path := filepath.Join(t.TempDir(), "req.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, code = runFull(t, base, "", "submit", "--from-json", path, "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	fromFile := submittedID(t, out)

	c := client.New(base)
	for _, id := range []string{fromStdin, fromFile} {
		meta, err := c.JobRecord(id)
		if err != nil {
			t.Fatal(err)
		}
		ex := meta.Execute
		if ex.Type != protocol.ExecPythonUV || ex.Entry != "evident.train:run" || string(ex.Arg) != `{"lr":0.01}` ||
			meta.Name != "sweep" || meta.Desc != "lr0.01" || meta.Retries != 1 {
			t.Fatalf("meta = %+v, execute %+v", meta, ex)
		}
	}
}

func TestSubmitFlagsOverrideJSON(t *testing.T) {
	base := t.TempDir()
	script := filepath.Join(t.TempDir(), "train.sh")
	if err := os.WriteFile(script, []byte("echo hi\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"abi": 2, "type": "bash", "script_file": "` + script + `", "priority": 1, "desc": "from-json", "group": "a"}`
	out, errOut, code := runFull(t, base, body, "submit", "--from-json", "-", "--priority", "5", "-g", "b", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s %s", code, out, errOut)
	}
	meta, _ := client.New(base).JobRecord(submittedID(t, out))
	if meta.Priority != 5 || meta.Name != "b" || meta.Desc != "from-json" || meta.Execute.Label != script {
		t.Fatalf("meta = %+v", meta)
	}
}

func TestSubmitFromJSONErrors(t *testing.T) {
	base := t.TempDir()
	cases := []struct {
		name, stdin string
		args        []string
		code        string
	}{
		{"unknown field", `{"abi": 2, "type": "bash", "command": "echo"}`, []string{"--from-json", "-"}, "invalid_argument"},
		{"inline script", `{"abi": 2, "type": "bash", "script": "echo hi"}`, []string{"--from-json", "-"}, "invalid_argument"},
		{"script args", `{"abi": 2, "type": "bash"}`, []string{"--from-json", "-", "x.sh"}, "invalid_argument"},
		{"bad abi", `{"abi": 1, "type": "bash", "script_file": "x.sh"}`, []string{"--from-json", "-"}, "invalid_argument"},
		{"missing file", ``, []string{"--from-json", filepath.Join(base, "nope.json")}, "not_found"},
		{"python with -c", `{"abi": 2, "type": "python-uv", "entry": "m:f"}`, []string{"--from-json", "-", "-c", "echo"}, "invalid_argument"},
	}
	for _, tc := range cases {
		out, _, code := runFull(t, base, tc.stdin, append(append([]string{"submit"}, tc.args...), "--json")...)
		if gotCode, msg := errorEnvelope(t, out); code != 1 || gotCode != tc.code {
			t.Errorf("%s: exit %d code %q msg %q", tc.name, code, gotCode, msg)
		}
	}
	if jobs, _ := client.New(base).ListJobs(client.ListJobsOpts{All: true}); len(jobs) != 0 {
		t.Fatalf("errors queued %d jobs", len(jobs))
	}
}

func TestSubmitScriptPathsUseScriptFile(t *testing.T) {
	base := t.TempDir()
	dir := t.TempDir()
	var paths []string
	for _, name := range []string{"a.sh", "b.sh"} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("echo "+name+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	out, errOut, code := runFull(t, base, "", "submit", "-q", "--desc", "d", paths[0], paths[1])
	if code != 0 {
		t.Fatalf("exit %d: %s %s", code, out, errOut)
	}
	ids := strings.Fields(out)
	c := client.New(base)
	for i, id := range ids {
		meta, _ := c.JobRecord(id)
		script, _ := c.JobScript(id)
		if meta.Execute.Label != paths[i] || script != "echo "+filepath.Base(paths[i])+"\n" || meta.Desc != "d" {
			t.Fatalf("job %d: label %q script %q desc %q", i, meta.Execute.Label, script, meta.Desc)
		}
	}
}

func writeLegacyJob(t *testing.T, base, id string) {
	t.Helper()
	paths := protocol.NewPaths(base)
	if err := os.MkdirAll(paths.JobDir(id), 0o755); err != nil {
		t.Fatal(err)
	}
	meta := `{"job_id": "` + id + `", "worker_id": "w1", "command": "bash old.sh", "status": "completed",
	  "submitted_at": "2026-01-05T10:00:00Z", "exit_code": 0}`
	if err := os.WriteFile(paths.JobMeta(id), []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyJobsWarnOnce(t *testing.T) {
	base := t.TempDir()
	writeLegacyJob(t, base, "old00001")
	writeLegacyJob(t, base, "old00002")

	out, errOut, code := runFull(t, base, "", "ps", "-a")
	if code != 0 || !strings.Contains(out, "bash old.sh") {
		t.Fatalf("exit %d out %q", code, out)
	}
	if want := "warning: 2 jobs use a deprecated meta.json schema (1); upgraded to schema 2 in memory\n"; errOut != want {
		t.Fatalf("stderr %q, want %q", errOut, want)
	}

	base = t.TempDir()
	writeLegacyJob(t, base, "old00003")
	out, errOut, code = runFull(t, base, "", "ps", "-a", "--json")
	var jobs []client.Job
	if code != 0 || json.Unmarshal([]byte(out), &jobs) != nil || len(jobs) != 1 || jobs[0].Label() != "bash old.sh" {
		t.Fatalf("exit %d stdout %q", code, out)
	}
	var line struct {
		Notice struct {
			Level, Code, Message string
			Count                int
		} `json:"notice"`
	}
	if strings.Count(errOut, "\n") != 1 || json.Unmarshal([]byte(errOut), &line) != nil ||
		line.Notice.Level != "warning" || line.Notice.Code != "legacy_schema" || line.Notice.Count != 1 {
		t.Fatalf("stderr %q", errOut)
	}
}

func TestResultAndGroupWait(t *testing.T) {
	base := copyFixture(t)
	out, _, code := runFull(t, base, "", "result", "p0000001", "--json")
	if code != 0 || out != `{"acc":0.912,"ece":0.031}`+"\n" {
		t.Fatalf("exit %d out %q", code, out)
	}
	out, _, code = runFull(t, base, "", "result", "a0000001", "--json")
	if gotCode, _ := errorEnvelope(t, out); code != 1 || gotCode != "not_found" {
		t.Fatalf("missing result: exit %d %q", code, out)
	}
	out, _, code = runFull(t, base, "", "group", "py", "--wait", "--poll", "0s", "--json")
	if gotCode, _ := errorEnvelope(t, out); code != 1 || gotCode != "invalid_argument" {
		t.Fatalf("--poll 0: exit %d %q", code, out)
	}
}
