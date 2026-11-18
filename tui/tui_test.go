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
	"github.com/hungngocphat01/tokimeki/client"
	"github.com/hungngocphat01/tokimeki/protocol"
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
	writeJSON(paths.WorkerRunning("node1", "running1"), protocol.JobInfo{JobID: "running1", PID: 222})
	if err := os.WriteFile(paths.WorkerHeartbeat("node1"), []byte(fmt.Sprintf("%d", time.Now().UnixNano())), 0o644); err != nil {
		t.Fatal(err)
	}
	writeJSON(paths.WorkerStats("node1"), statsFile{WorkerID: "node1", JobID: "running1", CPUPercent: 143, RSSBytes: 2 << 30, SampledAt: time.Now().UTC().Format(time.RFC3339)})
	if err := os.WriteFile(paths.JobStdout("running1"), []byte("first\nsecond\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	state, err := loadSnapshot(paths)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.workers) != 1 || len(state.workers[0].jobs) != 1 || state.workers[0].jobs[0].stats == nil || state.workers[0].jobs[0].stats.CPUPercent != 143 {
		t.Fatalf("runner stats snapshot = %+v", state.workers)
	}
	jobs := orderJobs(state.jobs, false)
	if len(jobs) != 2 || jobs[0].ID != "running1" || jobs[1].ID != "queued01" {
		t.Fatalf("active jobs = %+v", jobs)
	}
	history := orderJobs(state.jobs, true)
	if len(history) != 3 || history[0].ID != "running1" || history[1].ID != "queued01" || history[2].ID != "finished" {
		t.Fatalf("ps -a order = %+v", history)
	}
	initial := model{paths: paths}
	initial.applySnapshot(state)
	if initial.history[initial.selected[historyFeature]].ID != "finished" {
		t.Fatalf("history did not open at newest submission: selected=%d", initial.selected[historyFeature])
	}
	m := model{paths: paths, jobs: jobs}
	m.refreshSelected()
	if m.jobs[0].stats == nil || m.jobs[0].stats.CPUPercent != 143 || strings.Join(m.jobs[0].logs, "|") != "first|second" {
		t.Fatalf("selected telemetry = %+v", m.jobs[0])
	}
	// A runner may have moved on to another job or stopped sampling.
	writeJSON(paths.WorkerStats("node1"), statsFile{WorkerID: "node1", JobID: "otherjob", SampledAt: time.Now().UTC().Format(time.RFC3339)})
	if got := loadStats(paths, jobs[0]); got != nil {
		t.Fatalf("accepted another job's stats: %+v", got)
	}
	writeJSON(paths.WorkerStats("node1"), statsFile{WorkerID: "node1", JobID: "running1", SampledAt: time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)})
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
	if len(stale.workers) != 0 {
		t.Fatalf("TUI runners included stopped runner: %+v", stale.workers)
	}
	if got := orderJobs(stale.jobs, false); len(got) != 1 || got[0].ID != "queued01" {
		t.Fatalf("ps view with stopped runner = %+v", got)
	}
	if got := orderJobs(stale.jobs, true); len(got) != 3 || got[0].EffectiveStatus != "stopped" {
		t.Fatalf("ps -a view with stopped runner = %+v", got)
	}
}

func TestHistorySortsBySubmittedTimeAcrossStatusesAndPriorities(t *testing.T) {
	// History keeps the engine's submission order and includes every status.
	all := []job{
		{Job: client.Job{ID: "earliest", Status: protocol.StatusQueued, Priority: 99, EffectiveStatus: protocol.StatusQueued}},
		{Job: client.Job{ID: "middle", Status: protocol.StatusRunning, EffectiveStatus: protocol.StatusRunning}},
		{Job: client.Job{ID: "latest", Status: protocol.StatusCompleted, EffectiveStatus: protocol.StatusCompleted}},
	}
	got := orderJobs(all, true)
	for i, id := range []string{"earliest", "middle", "latest"} {
		if got[i].ID != id {
			t.Fatalf("history order = %+v", got)
		}
	}
	if active := orderJobs(all, false); len(active) != 2 || active[0].ID != "middle" || active[1].ID != "earliest" {
		t.Fatalf("active order = %+v", active)
	}
}

func TestJobsDetailsShowSubmittedAndElapsedTime(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	submitted := now.Add(-2 * time.Hour).UnixNano()
	started := now.Add(-time.Hour - 2*time.Minute - 3*time.Second).UnixNano()
	queued := job{Job: client.Job{ID: "queued", SubmittedAtNs: submitted, EffectiveStatus: protocol.StatusQueued}}
	running := job{Job: client.Job{ID: "running", SubmittedAtNs: submitted, StartedAtNs: started, EffectiveStatus: protocol.StatusRunning}}
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
	valid, _ := json.Marshal(map[string]string{"ts": time.Now().UTC().Format(time.RFC3339Nano), "kind": client.EventRunnerStart, "worker_id": "node1"})
	if err := os.WriteFile(paths.EventsLog(), append([]byte("malformed\n"), append(valid, '\n')...), 0o644); err != nil {
		t.Fatal(err)
	}
	state, err := loadSnapshot(paths)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.workers) != 1 || !state.workers[0].Alive || len(state.events) != 1 {
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

func TestRunnerDetailListShowsOnlyNames(t *testing.T) {
	m := model{feature: runnersFeature, workers: []worker{
		{Runner: client.Runner{ID: "idle", HasInfo: true, Hostname: "gpu01", Alive: true}},
		{Runner: client.Runner{ID: "busy", HasInfo: true, Hostname: "gpu02", Alive: true}, jobs: []runnerJob{{CurrentJob: client.CurrentJob{JobID: "job1"}}}},
	}}
	for i, want := range []struct{ label, status string }{
		{"idle", "idle"},
		{"busy", "running"},
	} {
		label, status := m.itemLabel(i)
		if label != want.label || status != want.status {
			t.Errorf("runner %d = (%q, %q), want (%q, %q)", i, label, status, want.label, want.status)
		}
	}
	if detail := m.renderRunnerDetail(60, 20); !strings.Contains(detail, "Status: idle") {
		t.Errorf("idle runner detail = %q", detail)
	}
}

func TestRunnerViewsShowTTLAndRemaining(t *testing.T) {
	remaining := (46*time.Hour + 30*time.Minute).Seconds()
	m := model{feature: runnersFeature, workers: []worker{{Runner: client.Runner{
		ID: "timed", HasInfo: true, Hostname: "gpu01", Alive: true,
		StartedAtNs: time.Now().Add(-3*time.Hour - 30*time.Minute).UnixNano(), TTLSecs: 50 * 3600, RemainingSecs: &remaining,
	}}}}
	label, _ := m.itemLabel(0)
	detail := m.renderRunnerDetail(70, 20)
	if label != "timed" || !strings.Contains(detail, "TTL: 2d 2h") || !strings.Contains(detail, "Remaining: 1d 23h") {
		t.Fatalf("runner label = %q; detail = %q", label, detail)
	}
}

func TestRunnerStatsViewAndModeToggle(t *testing.T) {
	m := model{
		paths: protocol.NewPaths("/tmp/tokimeki"), width: 100, height: 30,
		feature: runnersFeature, focus: 1,
		workers: []worker{
			{Runner: client.Runner{ID: "gpu-a", HasInfo: true, Hostname: "node-a", Alive: true}, jobs: []runnerJob{{CurrentJob: client.CurrentJob{JobID: "job1", PID: 123},
				stats: &client.StatsSample{JobID: "job1", CPUPercent: 143, RSSBytes: 2 << 30, GPUAvailable: true, GPUUtilPercent: 67, GPUMemBytes: 4 << 30, GPUMemTotalBytes: 8 << 30, NumProcs: 3, SampledAtNs: time.Now().UnixNano()}}}},
			{Runner: client.Runner{ID: "gpu-b", HasInfo: true, Hostname: "node-b", Alive: true}},
		},
	}
	next, _ := m.Update(tea.KeyPressMsg{Code: 't'})
	m = next.(model)
	if !m.runnerStats || !strings.Contains(m.statusLine(), "t details") {
		t.Fatalf("stats mode not enabled: %+v", m)
	}
	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	m = next.(model)
	if m.focus != 1 {
		t.Fatalf("stats view should have no detail pane, focus=%d", m.focus)
	}
	view := ansi.Strip(m.View().Content)
	for _, want := range []string{"Runners · stats", "gpu-a", "node-a", "job1", "CPU 143%", "RSS 2.0 GiB", "GPU 67%", "GPU_MEM 4.0 GiB/8.0 GiB", "PROCS 3", "gpu-b", "idle · no active job"} {
		if !strings.Contains(view, want) {
			t.Errorf("stats view missing %q: %s", want, view)
		}
	}
	next, _ = m.Update(tea.KeyPressMsg{Code: 't'})
	m = next.(model)
	if m.runnerStats || !strings.Contains(ansi.Strip(m.View().Content), "Runner · gpu-a") {
		t.Fatal("detail mode not restored")
	}
}

func TestActivityShowsSubmittedScriptName(t *testing.T) {
	m := model{
		feature: activityFeature,
		history: []job{{Job: client.Job{ID: "job12345", Command: "/work/train_model.sh"}}},
		events:  []client.Event{{Kind: client.EventStart, JobID: "job12345", TSNs: time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC).UnixNano()}},
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
		{Job: client.Job{ID: "a", Status: protocol.StatusRunning, EffectiveStatus: protocol.StatusRunning}},
		{Job: client.Job{ID: "b", Status: protocol.StatusRunning, EffectiveStatus: protocol.StatusRunning}},
	}})
	press := func(code rune) {
		t.Helper()
		next, _ := m.Update(tea.KeyPressMsg{Code: code})
		m = next.(model)
	}
	press(tea.KeyRight)
	press(tea.KeyDown)
	if m.focus != 1 || m.jobs[m.selected[jobsFeature]].ID != "b" {
		t.Fatalf("focus=%d selected=%d", m.focus, m.selected[jobsFeature])
	}
	press('v')
	if m.focus != 2 || m.detailSection != 1 {
		t.Fatalf("pane switch: focus=%d section=%d", m.focus, m.detailSection)
	}
	press(tea.KeyLeft)
	m.applySnapshot(snapshot{jobs: []job{
		{Job: client.Job{ID: "b", Status: protocol.StatusRunning, EffectiveStatus: protocol.StatusRunning}},
		{Job: client.Job{ID: "c", Status: protocol.StatusQueued, EffectiveStatus: protocol.StatusQueued}},
	}})
	if m.selected[jobsFeature] != 0 || m.jobs[m.selected[jobsFeature]].ID != "b" {
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
			m.jobs = []job{{Job: client.Job{ID: "running1", Status: protocol.StatusRunning, EffectiveStatus: protocol.StatusRunning}}}
			m.history = []job{{Job: client.Job{ID: "finished", Status: protocol.StatusCompleted, EffectiveStatus: protocol.StatusCompleted}}}
			m.workers = []worker{{Runner: client.Runner{ID: "node1", HasInfo: true, Hostname: "gpu01", Alive: true}}}
			m.events = []client.Event{{Kind: client.EventStart, JobID: "running1"}}
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
			if feature == runnersFeature {
				m.runnerStats = true
				statsLines := strings.Split(m.View().Content, "\n")
				if len(statsLines) > size[1] {
					t.Fatalf("%v runner stats view has %d lines", size, len(statsLines))
				}
				for i, line := range statsLines {
					if lipgloss.Width(line) > size[0] {
						t.Fatalf("%v runner stats line %d is too wide", size, i)
					}
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
	m.jobs = []job{{Job: client.Job{ID: "queued01", Status: protocol.StatusQueued, DependsOn: []string{"a", "b", "c", "d", "e"}, EffectiveStatus: protocol.StatusQueued}}}
	before := ansi.Strip(m.View().Content)
	m.move(1)
	after := ansi.Strip(m.View().Content)
	if m.detailOffset != 1 || before == after {
		t.Fatalf("lower pane did not scroll: offset=%d", m.detailOffset)
	}
}

func TestJobActionsAndContextHelp(t *testing.T) {
	paths := protocol.NewPaths(t.TempDir())
	res, err := client.New(paths.Base).Submit(protocol.JobSpec{Script: "#!/bin/bash\necho hello\n", Command: "echo hello"}, client.SubmitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	queuedID := res.JobID
	m := model{paths: paths, focus: 1, jobs: []job{{Job: client.Job{ID: queuedID, Status: protocol.StatusQueued, EffectiveStatus: protocol.StatusQueued}}}}
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
	if m.confirm == nil || m.confirm.kind != cancelAction || m.confirm.job.ID != queuedID {
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
	selected := job{Job: client.Job{ID: "job12345", Worker: "node1", Status: protocol.StatusRunning, EffectiveStatus: protocol.StatusRunning}}
	m := model{paths: paths, focus: 1, jobs: []job{selected}}
	if !strings.Contains(m.statusLine(), "k kill") || !strings.Contains(m.statusLine(), "p killall python") {
		t.Fatalf("running help = %q", m.statusLine())
	}
	for _, dir := range []string{paths.JobDir(selected.ID), paths.WorkerInbox("node1"), paths.WorkerOutbox("node1"), paths.TmpDir()} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	meta, _ := json.Marshal(protocol.JobMeta{JobID: selected.ID, WorkerID: selected.Worker, Status: selected.Status})
	if err := os.WriteFile(paths.JobMeta(selected.ID), meta, 0o644); err != nil {
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

func TestShellKeyOnlyOpensFromSelectedRunner(t *testing.T) {
	m := model{paths: protocol.NewPaths("/tmp/tokimeki"), feature: runnersFeature, workers: []worker{
		{Runner: client.Runner{ID: "gpu01", HasInfo: true, Alive: true}},
	}}
	if m.openShell() != nil {
		t.Fatal("shell opened with the features pane focused")
	}
	m.focus = 1
	if m.openShell() == nil {
		t.Fatal("shell did not open for the selected runner")
	}
	if !strings.Contains(m.statusLine(), "s shell") {
		t.Fatalf("status line = %q", m.statusLine())
	}
	m.workers = nil
	if m.openShell() != nil {
		t.Fatal("shell opened with no runner selected")
	}
	m.feature, m.focus = jobsFeature, 1
	if strings.Contains(m.statusLine(), "s shell") {
		t.Fatalf("jobs status line offers shell: %q", m.statusLine())
	}
}

func TestShellNoticeReportsOnlyConnectionFailures(t *testing.T) {
	if got := shellNotice("gpu01", "Connecting to runner gpu01...\n"); got != "Closed shell on gpu01" {
		t.Errorf("notice = %q", got)
	}
	got := shellNotice("gpu01", "Connecting to runner gpu01...\nError: timed out\n")
	if got != "shell on gpu01: timed out" {
		t.Errorf("notice = %q", got)
	}
}

// statsFile is the on-disk shape of workers/<id>/stats.json for fixtures.
type statsFile struct {
	WorkerID   string  `json:"worker_id"`
	JobID      string  `json:"job_id"`
	CPUPercent float64 `json:"cpu_percent"`
	RSSBytes   int64   `json:"rss_bytes"`
	SampledAt  string  `json:"sampled_at"`
}

func TestTUIRunnerWithTwoJobs(t *testing.T) {
	now := time.Now().UnixNano()
	m := model{
		paths: protocol.NewPaths("/tmp/tokimeki"), width: 100, height: 40,
		feature: runnersFeature, focus: 1, runnerStats: true,
		workers: []worker{{
			Runner: client.Runner{ID: "gpu-a", HasInfo: true, Hostname: "node-a", Alive: true, ComputeThreads: 3},
			jobs: []runnerJob{
				{CurrentJob: client.CurrentJob{JobID: "coord1", Kind: protocol.KindCoordinator, PID: 11, Command: "bash sweep.sh"},
					stats: &client.StatsSample{JobID: "coord1", CPUPercent: 1, NumProcs: 1, SampledAtNs: now}},
				{CurrentJob: client.CurrentJob{JobID: "train1", Kind: protocol.KindCompute, PID: 12, Command: "python train.py"},
					stats: &client.StatsSample{JobID: "train1", CPUPercent: 250, NumProcs: 4, SampledAtNs: now}},
			},
		}},
	}
	if _, status := m.itemLabel(0); status != "running" {
		t.Fatalf("status = %q, want running", status)
	}
	view := ansi.Strip(m.View().Content)
	for _, want := range []string{"JOBS 1/3+C", "[C] coord1", "train1", "CPU 250%", "PROCS 4"} {
		if !strings.Contains(view, want) {
			t.Errorf("stats view missing %q: %s", want, view)
		}
	}
	m.runnerStats = false
	detail := ansi.Strip(m.renderRunnerDetail(80, 30))
	for _, want := range []string{"Jobs: 1/3+C", "[C] coord1  PID 11  bash sweep.sh", "train1  PID 12  python train.py"} {
		if !strings.Contains(detail, want) {
			t.Errorf("detail missing %q: %s", want, detail)
		}
	}
}
