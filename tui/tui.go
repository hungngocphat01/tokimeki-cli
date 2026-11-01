// Package tui provides the interactive Tokimeki dashboard.
package tui

import (
	"fmt"
	"sort"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/ngocphat/tokimeki/eventlog"
	"github.com/ngocphat/tokimeki/protocol"
)

const refreshInterval = time.Second

type feature int

const (
	jobsFeature feature = iota
	historyFeature
	runnersFeature
	activityFeature
	featureCount
)

var featureNames = [...]string{"Jobs", "History", "Runners", "Activity"}

type refreshMsg struct{}
type pendingAction struct {
	kind action
	job  job
}
type snapshotMsg struct {
	state snapshot
	err   error
}

type model struct {
	paths         *protocol.Paths
	jobs, history []job
	workers       []worker
	events        []eventlog.Event
	feature       feature
	selected      [featureCount]int
	focus         int // 0: features, 1: items, 2: details
	detailSection int // 0: upper pane, 1: lower pane
	detailOffset  int
	scriptOpen    bool
	script        []string
	confirm       *pendingAction
	busy          bool
	notice        string
	width, height int
	err           error
}

// Run starts the dashboard using the normal --base/TOKIMEKI_HOME resolution.
func Run(base string) error {
	_, err := tea.NewProgram(model{paths: protocol.NewPaths(base)}).Run()
	return err
}
func (m model) Init() tea.Cmd { return tea.Batch(refresh(), load(m.paths)) }
func refresh() tea.Cmd {
	return tea.Tick(refreshInterval, func(time.Time) tea.Msg { return refreshMsg{} })
}
func load(paths *protocol.Paths) tea.Cmd {
	return func() tea.Msg {
		state, err := loadSnapshot(paths)
		return snapshotMsg{state, err}
	}
}

func (m model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case refreshMsg:
		return m, tea.Batch(refresh(), load(m.paths))
	case snapshotMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.err = nil
		m.applySnapshot(msg.state)
	case actionResult:
		m.busy = false
		m.notice = actionNotice(msg)
		return m, load(m.paths)
	case tea.KeyPressMsg:
		if m.confirm != nil {
			return m.handleConfirmation(msg)
		}
		return m.handleKey(msg)
	}
	return m, nil
}
func (m model) handleConfirmation(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "y", "enter":
		pending := *m.confirm
		m.confirm = nil
		m.busy = true
		return m, func() tea.Msg { return runAction(m.paths, pending.job, pending.kind) }
	case "n", "esc", "q":
		m.confirm = nil
	}
	return m, nil
}
func (m model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "left", "h", "esc":
		if m.scriptOpen {
			m.scriptOpen = false
		} else if m.focus > 0 {
			m.focus--
		}
	case "right", "l", "tab":
		if m.focus < 2 && (m.focus == 0 || m.itemCount() > 0) {
			m.focus++
		}
	case "enter":
		if m.focus == 1 && m.feature == activityFeature && m.jumpFromEvent() {
			break
		}
		if m.focus < 2 && (m.focus == 0 || m.itemCount() > 0) {
			m.focus++
		}
	case "up":
		m.move(-1)
	case "down":
		m.move(1)
	case "pgup":
		if m.focus == 2 {
			m.scrollDetail(-max(1, (m.height-4)*2/3))
		}
	case "pgdown":
		if m.focus == 2 {
			m.scrollDetail(max(1, (m.height-4)*2/3))
		}
	case "end":
		m.detailOffset = 0
	case "v":
		if m.focus > 0 && m.itemCount() > 0 && m.feature != activityFeature {
			m.focus = 2
			m.detailSection = 1 - m.detailSection
			m.detailOffset = 0
		}
	case "k", "p", "c":
		m.selectAction(msg.String())
	case "s":
		m.toggleScript()
	case "r":
		return m, load(m.paths)
	}
	return m, nil
}
func (m *model) move(delta int) {
	if m.focus == 0 {
		m.feature = feature(max(0, min(int(featureCount)-1, int(m.feature)+delta)))
		m.detailOffset = 0
		m.detailSection = 0
		m.scriptOpen = false
		m.refreshSelected()
	} else if m.focus == 1 {
		m.selected[m.feature] = max(0, min(m.itemCount()-1, m.selected[m.feature]+delta))
		m.detailOffset = 0
		m.detailSection = 0
		m.scriptOpen = false
		m.refreshSelected()
	} else {
		m.scrollDetail(delta)
	}
}

func (m *model) scrollDetail(delta int) {
	if m.feature == runnersFeature || (m.detailSection == 1 && m.feature != activityFeature) {
		m.detailOffset = max(0, m.detailOffset+delta)
	} else {
		m.detailOffset = max(0, m.detailOffset-delta)
	}
}

func (m *model) applySnapshot(state snapshot) {
	keys := [featureCount]string{}
	for f := feature(0); f < featureCount; f++ {
		keys[f] = m.itemKey(f)
	}
	m.jobs = orderJobs(state.jobs, false)
	m.history = orderJobs(state.jobs, true)
	m.workers, m.events = state.workers, state.events
	for f := feature(0); f < featureCount; f++ {
		m.selected[f] = 0
		if keys[f] == "" {
			if f == historyFeature && len(m.history) > 0 {
				m.selected[f] = len(m.history) - 1
			}
			continue
		}
		found := false
		for i := 0; i < m.countFor(f); i++ {
			m.selected[f] = i
			if m.itemKey(f) == keys[f] {
				found = true
				break
			}
		}
		if !found {
			m.selected[f] = 0
			if f == historyFeature && len(m.history) > 0 {
				m.selected[f] = len(m.history) - 1
			}
			if f == m.feature {
				m.detailOffset = 0
				m.detailSection = 0
				m.scriptOpen = false
			}
		}
	}
	if m.itemCount() == 0 {
		m.focus = min(m.focus, 1)
		m.detailOffset = 0
		m.detailSection = 0
		m.scriptOpen = false
	}
	m.refreshSelected()
}
func orderJobs(all []job, showAll bool) []job {
	ordered := make([]job, 0, len(all))
	for _, item := range all {
		if showAll || item.effectiveStatus == protocol.StatusRunning || item.effectiveStatus == protocol.StatusQueued {
			ordered = append(ordered, item)
		}
	}
	sort.Slice(ordered, func(i, j int) bool {
		left, right := ordered[i], ordered[j]
		if showAll {
			if left.SubmittedAt != right.SubmittedAt {
				return left.SubmittedAt < right.SubmittedAt
			}
			return left.JobID < right.JobID
		}
		li, ri := jobRank(left.effectiveStatus), jobRank(right.effectiveStatus)
		if li != ri {
			return li < ri
		}
		if left.Priority != right.Priority {
			return left.Priority > right.Priority
		}
		leftTime, rightTime := jobSortTime(left), jobSortTime(right)
		if leftTime != rightTime {
			return leftTime < rightTime
		}
		return left.JobID < right.JobID
	})
	return ordered
}
func jobSortTime(item job) string {
	if item.StartedAt != nil && *item.StartedAt != "" {
		return *item.StartedAt
	}
	return item.SubmittedAt
}
func jobRank(status string) int {
	if status == protocol.StatusRunning {
		return 0
	}
	return 1
}
func (m model) countFor(f feature) int {
	switch f {
	case jobsFeature:
		return len(m.jobs)
	case historyFeature:
		return len(m.history)
	case runnersFeature:
		return len(m.workers)
	case activityFeature:
		return len(m.events)
	}
	return 0
}
func (m model) itemCount() int { return m.countFor(m.feature) }
func (m model) currentJob() *job {
	index := m.selected[m.feature]
	switch m.feature {
	case jobsFeature:
		if index >= 0 && index < len(m.jobs) {
			return &m.jobs[index]
		}
	case historyFeature:
		if index >= 0 && index < len(m.history) {
			return &m.history[index]
		}
	}
	return nil
}
func (m model) currentWorker() *worker {
	index := m.selected[runnersFeature]
	if m.feature == runnersFeature && index >= 0 && index < len(m.workers) {
		return &m.workers[index]
	}
	return nil
}
func (m model) currentEvent() *eventlog.Event {
	index := m.selected[activityFeature]
	if m.feature == activityFeature && index >= 0 && index < len(m.events) {
		return &m.events[index]
	}
	return nil
}
func (m model) itemKey(f feature) string {
	i := m.selected[f]
	switch f {
	case jobsFeature:
		if i < len(m.jobs) {
			return m.jobs[i].JobID
		}
	case historyFeature:
		if i < len(m.history) {
			return m.history[i].JobID
		}
	case runnersFeature:
		if i < len(m.workers) {
			return m.workers[i].info.WorkerID
		}
	case activityFeature:
		if i < len(m.events) {
			e := m.events[i]
			return fmt.Sprintf("%s:%s:%s:%s", e.TS, e.Kind, e.JobID, e.WorkerID)
		}
	}
	return ""
}
func (m *model) refreshSelected() {
	if selected := m.currentJob(); selected != nil {
		selected.stats = loadStats(m.paths, *selected)
		selected.logs = loadLogs(m.paths, *selected, 300)
	}
	if selected := m.currentWorker(); selected != nil && selected.status == "running" && selected.current != nil {
		item := job{JobMeta: protocol.JobMeta{JobID: selected.current.JobID, WorkerID: selected.info.WorkerID, Status: protocol.StatusRunning}}
		selected.stats = loadStats(m.paths, item)
	}
}
func (m *model) selectAction(key string) {
	selected := m.currentJob()
	if m.busy || m.focus == 0 || selected == nil {
		return
	}
	switch key {
	case "k":
		if selected.effectiveStatus == protocol.StatusRunning {
			m.confirm = &pendingAction{killAction, *selected}
		}
	case "p":
		if selected.effectiveStatus == protocol.StatusRunning && selected.WorkerID != "" {
			m.confirm = &pendingAction{pythonAction, *selected}
		}
	case "c":
		if selected.effectiveStatus == protocol.StatusQueued {
			m.confirm = &pendingAction{cancelAction, *selected}
		}
	}
}
func (m *model) toggleScript() {
	selected := m.currentJob()
	if m.focus == 0 || selected == nil {
		return
	}
	if m.scriptOpen {
		m.scriptOpen = false
		return
	}
	lines, err := scriptLines(m.paths, selected.JobID)
	if err != nil {
		m.notice = fmt.Sprintf("script %s: %v", selected.JobID, err)
		return
	}
	m.script, m.scriptOpen, m.detailOffset, m.focus = lines, true, 0, 2
	m.detailSection = 0
}
func (m *model) jumpFromEvent() bool {
	e := m.currentEvent()
	if e == nil {
		return false
	}
	if e.JobID != "" {
		for _, f := range []feature{jobsFeature, historyFeature} {
			items := m.jobs
			if f == historyFeature {
				items = m.history
			}
			for i, item := range items {
				if item.JobID == e.JobID {
					m.feature, m.selected[f], m.focus, m.detailOffset = f, i, 1, 0
					m.refreshSelected()
					m.detailSection = 0
					return true
				}
			}
		}
	}
	for i, item := range m.workers {
		if e.WorkerID != "" && item.info.WorkerID == e.WorkerID {
			m.feature, m.selected[runnersFeature], m.focus, m.detailOffset = runnersFeature, i, 1, 0
			m.detailSection = 0
			return true
		}
	}
	return false
}
func actionNotice(result actionResult) string {
	label := string(result.action)
	if result.action == killAction {
		label = "SIGTERM"
	} else if result.action == pythonAction {
		label = "killall python"
	}
	if result.err != nil {
		return fmt.Sprintf("%s for %s: %v", label, result.jobID, result.err)
	}
	if result.action == cancelAction {
		return fmt.Sprintf("Canceled queued job %s", result.jobID)
	}
	return fmt.Sprintf("%s requested for %s", label, result.jobID)
}
func (m model) statusLine() string {
	if m.confirm != nil {
		return "y confirm  n/Esc cancel"
	}
	if m.focus == 0 {
		return "↑/↓ feature  →/Enter open  r refresh  q quit"
	}
	switch m.feature {
	case jobsFeature, historyFeature:
		selected := m.currentJob()
		if selected == nil {
			return "← features  ↑/↓ select  r refresh  q quit"
		}
		if m.scriptOpen {
			return "s/Esc logs  v pane  ↑/↓ scroll  PgUp/PgDn page  q quit"
		}
		if selected.effectiveStatus == protocol.StatusQueued {
			return "c cancel  s script  v pane  ←/→ focus  ↑/↓ select/scroll  q quit"
		}
		if selected.effectiveStatus == protocol.StatusRunning {
			return "k kill  p killall python  s script  v pane  ←/→ focus  ↑/↓ select/scroll  q quit"
		}
		return "s script  v pane  ←/→ focus  ↑/↓ select/scroll  q quit"
	case activityFeature:
		if selected := m.currentEvent(); selected == nil || (selected.JobID == "" && selected.WorkerID == "") {
			return "←/→ focus  ↑/↓ select/scroll  q quit"
		}
		return "Enter open related job/runner  ←/→ focus  ↑/↓ select/scroll  q quit"
	case runnersFeature:
		return "v pane  ←/→ focus  ↑/↓ select/scroll  r refresh  q quit"
	}
	return "q quit"
}
func (m model) noticeLine() string {
	if m.confirm != nil {
		switch m.confirm.kind {
		case killAction:
			return "Send SIGTERM to job " + m.confirm.job.JobID + "?"
		case pythonAction:
			return "Run killall python on runner " + m.confirm.job.WorkerID + "? This affects all Python processes there."
		case cancelAction:
			return "Cancel queued job " + m.confirm.job.JobID + "?"
		}
	}
	if m.busy {
		return "Waiting for runner response…"
	}
	if m.err != nil {
		return fmt.Sprintf("Read error: %v", m.err)
	}
	return m.notice
}
