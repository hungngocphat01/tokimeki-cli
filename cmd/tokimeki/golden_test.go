package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

var updateGolden = flag.Bool("update", false, "rewrite testdata/golden files")

// goldenCases pin the CLI's user-visible output against a static state
// directory (testdata/fixture). Values that depend on the current time are
// masked per case.
var goldenCases = []struct {
	name string
	args []string
	mask []*regexp.Regexp
}{
	{name: "ps", args: []string{"ps"}},
	{name: "ps_all", args: []string{"ps", "-a"}},
	{name: "ps_worker", args: []string{"ps", "-a", "w-gpu"}},
	{name: "ps_name", args: []string{"ps", "-a", "--name", "TRAIN"}},
	{name: "ps_json", args: []string{"ps", "--json"}},
	{name: "ps_all_json", args: []string{"ps", "-a", "--json"}},
	{name: "runners", args: []string{"runners"}},
	{name: "runners_all", args: []string{"runners", "-a"}},
	{name: "runners_all_json", args: []string{"runners", "-a", "--json"}},
	{name: "ls", args: []string{"ls"}},
	{name: "job", args: []string{"job", "a0000001"}},
	{name: "job_json", args: []string{"job", "a0000001", "--json"}},
	{name: "job_missing", args: []string{"job", "nope"}},
	{name: "logs", args: []string{"logs", "a0000001"}},
	{name: "logs_tail", args: []string{"logs", "-n", "2", "a0000001"}},
	{name: "logs_stderr", args: []string{"logs", "--stderr", "a0000001"}},
	{name: "logs_index", args: []string{"logs", "-11"}},
	{name: "logs_missing", args: []string{"logs", "nope"}},
	{name: "events", args: []string{"events"}},
	{name: "events_json", args: []string{"events", "--json"}},
	{name: "events_job", args: []string{"events", "--job", "a0000001"}},
	{name: "events_since", args: []string{"events", "--since", "2026-01-05T11:24:00Z"}},
	{name: "stats", args: []string{"stats"}, mask: []*regexp.Regexp{regexp.MustCompile(`\d+h$`)}},
	{name: "stats_json", args: []string{"stats", "--json"}},
	{name: "gc_dry_run", args: []string{"gc", "--dry-run"}},
	{name: "gc_size_cap", args: []string{"gc", "--dry-run", "--older-than", "", "--max-size", "1K"}},
	{name: "top_plain", args: []string{"top", "--plain"}, mask: []*regexp.Regexp{regexp.MustCompile(`^tokimeki — \d\d:\d\d:\d\d`)}},
	{name: "cancel_finished", args: []string{"cancel", "a0000001"}},
	{name: "kill_finished", args: []string{"kill", "a0000001"}},
	{name: "kill_queued", args: []string{"kill", "a0000006"}},
	{name: "group_exp", args: []string{"group", "exp"}},
	{name: "group_exp_json", args: []string{"group", "exp", "--json"}},
	{name: "group_missing", args: []string{"group", "nope"}},
	{name: "ps_group", args: []string{"ps", "-a", "--name", "exp"}},
}

func TestGolden(t *testing.T) {
	for _, tc := range goldenCases {
		t.Run(tc.name, func(t *testing.T) {
			base := copyFixture(t)
			stdout, stderr, err := runCLI(t, base, tc.args...)
			got := normalizeGolden(tc.name, stdout, base) +
				"\n--stderr--\n" + normalizeGolden(tc.name, stderr, base) +
				"\n--err--\n" + normalizeGolden(tc.name, fmt.Sprint(err), base) + "\n"
			for _, re := range tc.mask {
				got = maskLines(got, re)
			}
			path := filepath.Join("testdata", "golden", tc.name+".txt")
			if *updateGolden {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden (run with -update to create): %v", err)
			}
			if got != string(want) {
				t.Errorf("output mismatch for %v\n--- want\n%s\n--- got\n%s", tc.args, want, got)
			}
		})
	}
}

// runCLI runs one command in-process against base with stdout/stderr
// captured. Output is deterministic: UTC timezone, no color, UTF-8 glyphs.
func runCLI(t *testing.T, base string, args ...string) (string, string, error) {
	t.Helper()
	t.Setenv("NO_COLOR", "1")
	t.Setenv("LC_ALL", "en_US.UTF-8")
	t.Setenv("TOKIMEKI_HOME", "")
	prevLocal := time.Local
	time.Local = time.UTC
	defer func() { time.Local = prevLocal }()

	prevArgs := os.Args
	argv := normalizeLogIndexArgs(append([]string{"tok"}, args...))
	os.Args = argv
	defer func() { os.Args = prevArgs }()

	outR, outW, _ := os.Pipe()
	errR, errW, _ := os.Pipe()
	prevOut, prevErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outW, errW
	outC, errC := drain(outR), drain(errR)

	cmd := newRootCmd()
	cmd.SetArgs(append([]string{"--base", base}, argv[1:]...))
	err := cmd.Execute()

	os.Stdout, os.Stderr = prevOut, prevErr
	outW.Close()
	errW.Close()
	return <-outC, <-errC, err
}

func drain(r *os.File) <-chan string {
	c := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		r.Close()
		c <- buf.String()
	}()
	return c
}

func copyFixture(t *testing.T) string {
	t.Helper()
	src := filepath.Join("testdata", "fixture")
	dst := t.TempDir()
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if d.Name() == ".keep" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		info, _ := d.Info()
		return os.WriteFile(target, data, info.Mode().Perm())
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

func normalizeGolden(_ string, s, base string) string {
	return strings.ReplaceAll(s, base, "<BASE>")
}

func maskLines(s string, re *regexp.Regexp) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = re.ReplaceAllString(line, "<masked>")
	}
	return strings.Join(lines, "\n")
}
