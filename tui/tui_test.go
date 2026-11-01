package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/ngocphat/tokimeki/eventlog"
	"github.com/ngocphat/tokimeki/protocol"
	"github.com/ngocphat/tokimeki/registry"
)

func TestActiveJobsAndSelectedTelemetry(t *testing.T) {
	paths := protocol.NewPaths(t.TempDir())
	writeJSON := func(path string, value any) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeJSON(paths.JobMeta("running1"), protocol.JobMeta{JobID: "running1", WorkerID: "node1", Status: protocol.StatusRunning, SubmittedAt: "2026-01-01T00:00:00Z"})
	writeJSON(paths.JobMeta("queued01"), protocol.JobMeta{JobID: "queued01", Status: protocol.StatusQueued, SubmittedAt: "2026-01-01T00:01:00Z"})
	writeJSON(paths.JobMeta("finished"), protocol.JobMeta{JobID: "finished", Status: protocol.StatusCompleted, SubmittedAt: "2026-01-01T00:02:00Z"})
	writeJSON(paths.WorkerInfo("node1"), protocol.WorkerInfo{WorkerID: "node1", Hostname: "gpu01"})
	if err := os.WriteFile(paths.WorkerHeartbeat("node1"), []byte(fmt.Sprintf("%d", time.Now().UnixNano())), 0o644); err != nil {
		t.Fatal(err)
	}
	writeJSON(paths.WorkerStats("node1"), stats{JobID: "running1", CPUPercent: 143, RSSBytes: 2 << 30, SampledAt: time.Now().UTC().Format(time.RFC3339)})
	if err := os.WriteFile(paths.JobStdout("running1"), []byte("first\nsecond\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	state, err := loadSnapshot(paths)
	if err != nil {
		t.Fatal(err)
	}
	jobs := orderJobs(state.jobs, false)
	if len(jobs) != 2 || jobs[0].JobID != "running1" || jobs[1].JobID != "queued01" {
		t.Fatalf("active jobs = %+v", jobs)
	}
	history := orderJobs(state.jobs, true)
	if len(history) != 3 || history[0].JobID != "running1" || history[1].JobID != "queued01" || history[2].JobID != "finished" {
		t.Fatalf("ps -a order = %+v", history)
	}
	initial := model{paths: paths}
	initial.applySnapshot(state)
	if initial.history[initial.selected[historyFeature]].JobID != "finished" {
		t.Fatalf("history did not open at newest submission: selected=%d", initial.selected[historyFeature])
	}
	m := model{paths: paths, jobs: jobs}
	m.refreshSelected()
	if m.jobs[0].stats == nil || m.jobs[0].stats.CPUPercent != 143 || strings.Join(m.jobs[0].logs, "|") != "first|second" {
		t.Fatalf("selected telemetry = %+v", m.jobs[0])
	}
	// A runner may have moved on to another job or stopped sampling.
	writeJSON(paths.WorkerStats("node1"), stats{JobID: "otherjob", SampledAt: time.Now().UTC().Format(time.RFC3339)})
	if got := loadStats(paths, jobs[0]); got != nil {
		t.Fatalf("accepted another job's stats: %+v", got)
	}
	writeJSON(paths.WorkerStats("node1"), stats{JobID: "running1", SampledAt: time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)})
	if got := loadStats(paths, jobs[0]); got != nil {
		t.Fatalf("accepted stale stats: %+v", got)
	}
	if err := os.WriteFile(paths.WorkerHeartbeat("node1"), []byte(fmt.Sprintf("%d", time.Now().Add(-time.Minute).UnixNano())), 0o644); err != nil {
		t.Fatal(err)
	}
	stale, err := loadSnapshot(paths)
	if err != nil {
		t.Fatal(err)
	}
	if got := orderJobs(stale.jobs, false); len(got) != 1 || got[0].JobID != "queued01" {
		t.Fatalf("ps view with stopped runner = %+v", got)
	}
	if got := orderJobs(stale.jobs, true); len(got) != 3 || got[0].effectiveStatus != "stopped" {
		t.Fatalf("ps -a view with stopped runner = %+v", got)
	}
}

func TestHistorySortsBySubmittedTimeAcrossStatusesAndPriorities(t *testing.T) {
	started := "2026-01-03T00:00:00Z"
	all := []job{
		{JobMeta: protocol.JobMeta{JobID: "latest", Status: protocol.StatusCompleted, SubmittedAt: "2026-01-03T00:00:00Z"}, effectiveStatus: protocol.StatusCompleted},
		{JobMeta: protocol.JobMeta{JobID: "middle", Status: protocol.StatusRunning, SubmittedAt: "2026-01-02T00:00:00Z", StartedAt: &started}, effectiveStatus: protocol.StatusRunning},
		{JobMeta: protocol.JobMeta{JobID: "earliest", Status: protocol.StatusQueued, SubmittedAt: "2026-01-01T00:00:00Z", Priority: 99}, effectiveStatus: protocol.StatusQueued},
	}
	got := orderJobs(all, true)
	for i, id := range []string{"earliest", "middle", "latest"} {
		if got[i].JobID != id {
			t.Fatalf("history order = %+v", got)
		}
	}
}

func TestJobsDetailsShowSubmittedAndElapsedTime(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	submitted := now.Add(-2 * time.Hour).Format(time.RFC3339)
	started := now.Add(-time.Hour - 2*time.Minute - 3*time.Second).Format(time.RFC3339)
	queued := job{JobMeta: protocol.JobMeta{JobID: "queued", SubmittedAt: submitted}, effectiveStatus: protocol.StatusQueued}
	running := job{JobMeta: protocol.JobMeta{JobID: "running", SubmittedAt: submitted, StartedAt: &started}, effectiveStatus: protocol.StatusRunning}
	for _, tc := range []struct {
		item job
		want string
	}{
		{queued, "Elapsed: 2h 00m 00s (since submission)"},
		{running, "Elapsed: 1h 02m 03s (since start)"},
	} {
		lines := strings.Join(jobTimingLines(tc.item, now), "\n")
		if !strings.Contains(lines, "Submitted: ") || !strings.Contains(lines, tc.want) {
			t.Errorf("job timing = %q; want %q", lines, tc.want)
		}
	}
	if got := strings.Join(jobStatusLines(queued, nil, 80, true), "\n"); !strings.Contains(got, "Submitted:") {
		t.Fatalf("Jobs details omitted timing: %q", got)
	}
	if got := strings.Join(jobStatusLines(queued, nil, 80, false), "\n"); strings.Contains(got, "Submitted:") {
		t.Fatalf("History details included Jobs timing: %q", got)
	}
}

func TestRunnersActivityAndEventJump(t *testing.T) {
	paths := protocol.NewPaths(t.TempDir())
	if err := os.MkdirAll(paths.WorkerDir("node1"), 0o755); err != nil {
		t.Fatal(err)
	}
	info, _ := json.Marshal(protocol.WorkerInfo{WorkerID: "node1", Hostname: "gpu01"})
	if err := os.WriteFile(paths.WorkerInfo("node1"), info, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.WorkerHeartbeat("node1"), []byte(fmt.Sprintf("%d", time.Now().UnixNano())), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(paths.SharedDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	valid, _ := json.Marshal(eventlog.Event{TS: time.Now().UTC().Format(time.RFC3339Nano), Kind: eventlog.KindRunnerStart, WorkerID: "node1"})
	if err := os.WriteFile(paths.EventsLog(), append([]byte("malformed\n"), append(valid, '\n')...), 0o644); err != nil {
		t.Fatal(err)
	}
	state, err := loadSnapshot(paths)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.workers) != 1 || state.workers[0].status != "running" || len(state.events) != 1 {
		t.Fatalf("runner/activity snapshot = %+v", state)
	}
	m := model{paths: paths, width: 100, height: 30}
	m.applySnapshot(state)
	m.move(1) // History
	m.move(1) // Runners
	if m.feature != runnersFeature || !strings.Contains(m.View().Content, "node1") {
		t.Fatalf("runners view = %q", m.View().Content)
	}
	m.move(1) // Activity
	m.focus = 1
	if !m.jumpFromEvent() || m.feature != runnersFeature || m.focus != 1 {
		t.Fatalf("event did not jump to runner: feature=%d focus=%d", m.feature, m.focus)
	}
}

func TestRunnerMarkersDistinguishIdleBusyAndStopped(t *testing.T) {
	m := model{feature: runnersFeature, workers: []worker{
		{info: protocol.WorkerInfo{WorkerID: "idle"}, status: "running"},
		{info: protocol.WorkerInfo{WorkerID: "busy"}, status: "running", current: &protocol.JobInfo{JobID: "job1"}},
		{info: protocol.WorkerInfo{WorkerID: "stopped"}, status: "stopped"},
	}}
	for i, want := range []struct{ label, status string }{
		{"◇ idle", "idle"},
		{"▶ busy", "running"},
		{"○ stopped", "stopped"},
	} {
		label, status := m.itemLabel(i)
		if !strings.HasPrefix(label, want.label) || status != want.status {
			t.Errorf("runner %d = (%q, %q), want (%q, %q)", i, label, status, want.label, want.status)
		}
	}
	if detail := m.renderRunnerDetail(60, 20); !strings.Contains(detail, "Status: idle") {
		t.Errorf("idle runner detail = %q", detail)
	}
}

func TestActivityShowsSubmittedScriptName(t *testing.T) {
	m := model{
		feature: activityFeature,
		history: []job{{JobMeta: protocol.JobMeta{JobID: "job12345", Command: "/work/train_model.sh"}}},
		events:  []eventlog.Event{{Kind: eventlog.KindStart, JobID: "job12345", TS: "2026-10-26T12:00:00Z"}},
	}
	label, _ := m.itemLabel(0)
	if !strings.Contains(label, "job12345  train_model.sh") {
		t.Fatalf("activity label = %q", label)
	}
	if strings.Contains(label, "/work/") {
		t.Fatalf("activity label exposed full script path: %q", label)
	}
}

func TestPaneUsesCompactSeparators(t *testing.T) {
	lines := strings.Split(ansi.Strip(pane("Activity", []string{"job row"}, 20, 3, true)), "\n")
	if len(lines) != 3 {
		t.Fatalf("pane height = %d", len(lines))
	}
	for _, line := range lines {
		if lipgloss.Width(line) != 20 || strings.ContainsAny(line, "╭╮╰╯") {
			t.Fatalf("pane line = %q", line)
		}
	}
	if !strings.Contains(lines[1], "job row") {
		t.Fatalf("pane content = %q", lines[1])
	}
}

func TestNavigationKeepsJobSelectionAcrossRefresh(t *testing.T) {
	paths := protocol.NewPaths(t.TempDir())
	m := model{paths: paths}
	m.applySnapshot(snapshot{jobs: []job{
		{JobMeta: protocol.JobMeta{JobID: "a", Status: protocol.StatusRunning}, effectiveStatus: protocol.StatusRunning},
		{JobMeta: protocol.JobMeta{JobID: "b", Status: protocol.StatusRunning}, effectiveStatus: protocol.StatusRunning},
	}})
	press := func(code rune) {
		t.Helper()
		next, _ := m.Update(tea.KeyPressMsg{Code: code})
		m = next.(model)
	}
	press(tea.KeyRight)
	press(tea.KeyDown)
	if m.focus != 1 || m.jobs[m.selected[jobsFeature]].JobID != "b" {
		t.Fatalf("focus=%d selected=%d", m.focus, m.selected[jobsFeature])
	}
	press('v')
	if m.focus != 2 || m.detailSection != 1 {
		t.Fatalf("pane switch: focus=%d section=%d", m.focus, m.detailSection)
	}
	press(tea.KeyLeft)
	m.applySnapshot(snapshot{jobs: []job{
		{JobMeta: protocol.JobMeta{JobID: "b", Status: protocol.StatusRunning}, effectiveStatus: protocol.StatusRunning},
		{JobMeta: protocol.JobMeta{JobID: "c", Status: protocol.StatusQueued}, effectiveStatus: protocol.StatusQueued},
	}})
	if m.selected[jobsFeature] != 0 || m.jobs[m.selected[jobsFeature]].JobID != "b" {
		t.Fatalf("selection lost after refresh: %+v", m)
	}
	press(tea.KeyRight)
	press(tea.KeyDown)
	if m.focus != 2 || m.detailOffset != 1 {
		t.Fatalf("detail navigation: focus=%d offset=%d", m.focus, m.detailOffset)
	}
	m.applySnapshot(snapshot{})
	if m.focus != 1 || m.detailOffset != 0 {
		t.Fatalf("empty jobs: focus=%d offset=%d", m.focus, m.detailOffset)
	}
}

func TestPaneFitsTerminal(t *testing.T) {
	for _, size := range [][2]int{{100, 30}, {82, 16}} {
		for feature := feature(0); feature < featureCount; feature++ {
			m := model{paths: protocol.NewPaths("/tmp/tokimeki"), width: size[0], height: size[1], feature: feature}
			m.jobs = []job{{JobMeta: protocol.JobMeta{JobID: "running1", Status: protocol.StatusRunning}, effectiveStatus: protocol.StatusRunning}}
			m.history = []job{{JobMeta: protocol.JobMeta{JobID: "finished", Status: protocol.StatusCompleted}, effectiveStatus: protocol.StatusCompleted}}
			m.workers = []worker{{info: protocol.WorkerInfo{WorkerID: "node1", Hostname: "gpu01"}, status: "running"}}
			m.events = []eventlog.Event{{Kind: eventlog.KindStart, JobID: "running1"}}
			view := m.View().Content
			lines := strings.Split(view, "\n")
			if len(lines) > size[1] {
				t.Fatalf("%v %s view has %d lines", size, featureNames[feature], len(lines))
			}
			for i, line := range lines {
				if lipgloss.Width(line) > size[0] {
					t.Fatalf("%v %s line %d is %d cells wide", size, featureNames[feature], i, lipgloss.Width(line))
				}
			}
		}
	}
}

func TestStatusColorsAndLowerPaneScrolling(t *testing.T) {
	paths := protocol.NewPaths(t.TempDir())
	runningColor := lipgloss.NewStyle().Foreground(statusColor(protocol.StatusRunning)).Render("x")
	queuedColor := lipgloss.NewStyle().Foreground(statusColor(protocol.StatusQueued)).Render("x")
	if runningColor == queuedColor || !strings.Contains(runningColor, "\x1b[") || !strings.Contains(queuedColor, "\x1b[") {
		t.Fatalf("running=%q queued=%q", runningColor, queuedColor)
	}
	m := model{paths: paths, feature: jobsFeature, focus: 2, detailSection: 1, width: 82, height: 16}
	m.jobs = []job{{JobMeta: protocol.JobMeta{JobID: "queued01", Status: protocol.StatusQueued, DependsOn: []string{"a", "b", "c", "d", "e"}}, effectiveStatus: protocol.StatusQueued}}
	before := ansi.Strip(m.View().Content)
	m.move(1)
	after := ansi.Strip(m.View().Content)
	if m.detailOffset != 1 || before == after {
		t.Fatalf("lower pane did not scroll: offset=%d", m.detailOffset)
	}
}

func TestJobActionsAndContextHelp(t *testing.T) {
	paths := protocol.NewPaths(t.TempDir())
	queuedID, _, err := registry.New(paths).CreateQueuedJob("", "#!/bin/bash\necho hello\n", "echo hello")
	if err != nil {
		t.Fatal(err)
	}
	m := model{paths: paths, focus: 1, jobs: []job{{JobMeta: protocol.JobMeta{JobID: queuedID, Status: protocol.StatusQueued}, effectiveStatus: protocol.StatusQueued}}}
	press := func(code rune) tea.Cmd {
		t.Helper()
		next, cmd := m.Update(tea.KeyPressMsg{Code: code})
		m = next.(model)
		return cmd
	}
	if !strings.Contains(m.statusLine(), "c cancel") || strings.Contains(m.statusLine(), "k kill") {
		t.Fatalf("queued help = %q", m.statusLine())
	}
	press('s')
	if !m.scriptOpen || len(m.script) == 0 || !strings.Contains(strings.Join(m.script, ""), "\x1b[") {
		t.Fatalf("script view lacks highlighted content: %+v", m.script)
	}
	press('s')
	press('c')
	if m.confirm == nil || m.confirm.kind != cancelAction || m.confirm.job.JobID != queuedID {
		t.Fatalf("cancel confirmation = %+v", m.confirm)
	}
	cmd := press('y')
	result, ok := cmd().(actionResult)
	if !ok || result.err != nil {
		t.Fatalf("cancel result = %+v", result)
	}
	if _, err := os.Stat(paths.JobDir(queuedID)); !os.IsNotExist(err) {
		t.Fatalf("queued job still exists: %v", err)
	}
}

func TestRunningActionsUseRunnerRequests(t *testing.T) {
	for _, tc := range []struct {
		kind     action
		typeName string
		command  string
		signal   int
	}{
		{kind: killAction, typeName: protocol.TypeKill, signal: 15},
		{kind: pythonAction, typeName: protocol.TypeExec, command: "killall python"},
	} {
		t.Run(string(tc.kind), func(t *testing.T) {
			testRunningAction(t, tc.kind, tc.typeName, tc.command, tc.signal)
		})
	}
}

func testRunningAction(t *testing.T, kind action, typeName, command string, signal int) {
	paths := protocol.NewPaths(t.TempDir())
	selected := job{JobMeta: protocol.JobMeta{JobID: "job12345", WorkerID: "node1", Status: protocol.StatusRunning}}
	selected.effectiveStatus = protocol.StatusRunning
	m := model{paths: paths, focus: 1, jobs: []job{selected}}
	if !strings.Contains(m.statusLine(), "k kill") || !strings.Contains(m.statusLine(), "p killall python") {
		t.Fatalf("running help = %q", m.statusLine())
	}
	for _, dir := range []string{paths.JobDir(selected.JobID), paths.WorkerInbox("node1"), paths.WorkerOutbox("node1"), paths.TmpDir()} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	meta, _ := json.Marshal(selected.JobMeta)
	if err := os.WriteFile(paths.JobMeta(selected.JobID), meta, 0o644); err != nil {
		t.Fatal(err)
	}
	request := make(chan protocol.Request, 1)
	go func() {
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			entries, _ := os.ReadDir(paths.WorkerInbox("node1"))
			if len(entries) == 0 {
				time.Sleep(10 * time.Millisecond)
				continue
			}
			data, _ := os.ReadFile(filepath.Join(paths.WorkerInbox("node1"), entries[0].Name()))
			var req protocol.Request
			if json.Unmarshal(data, &req) != nil {
				return
			}
			response, _ := json.Marshal(protocol.Response{ReqID: req.ReqID, OK: true})
			_ = os.WriteFile(filepath.Join(paths.WorkerOutbox("node1"), fmt.Sprintf("%s.resp", req.ReqID)), response, 0o644)
			request <- req
			return
		}
	}()
	result := runAction(paths, selected, kind)
	if result.err != nil {
		t.Fatal(result.err)
	}
	select {
	case req := <-request:
		if req.Type != typeName || req.Payload.Command != command || req.Payload.Signal != signal {
			t.Fatalf("request = %+v", req)
		}
	default:
		t.Fatal("no runner request captured")
	}
}
