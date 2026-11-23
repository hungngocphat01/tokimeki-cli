package tui

import (
	"fmt"
	"image/color"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/hungngocphat01/tokimeki/client"
	"github.com/hungngocphat01/tokimeki/protocol"
	"github.com/ngocphat/tokimeki-cli/display"
)

var (
	accent = lipgloss.Color("6")
	muted  = lipgloss.Color("8")
	bright = lipgloss.Color("15")
)

func (m model) View() tea.View {
	content := "tokimeki tui needs a terminal at least 82 columns × 16 rows. Resize or press q."
	if m.width >= 82 && m.height >= 16 {
		content = m.render()
	}
	view := tea.NewView(content)
	view.AltScreen = true
	return view
}
func (m model) render() string {
	mainHeight := m.height - 4
	featureWidth := 18
	itemWidth := min(38, max(28, m.width/3))
	if m.feature == activityFeature {
		itemWidth = min(70, max(40, m.width/2))
	}
	detailWidth := m.width - featureWidth - itemWidth
	features := pane("Features", m.featureLines(featureWidth-1), featureWidth, mainHeight, m.focus == 0)
	var body string
	if m.feature == runnersFeature && m.runnerStats {
		statsWidth := m.width - featureWidth
		statsPane := pane(fmt.Sprintf("Runners · stats (%d)", len(m.workers)), m.runnerStatsLines(statsWidth-1, mainHeight-1), statsWidth, mainHeight, m.focus == 1)
		body = lipgloss.JoinHorizontal(lipgloss.Top, features, statsPane)
	} else {
		itemTitle := fmt.Sprintf("%s (%d)", featureNames[m.feature], m.itemCount())
		if m.feature == historyFeature {
			itemTitle += " · submitted ↑"
		} else if m.feature == runnersFeature {
			itemTitle += " · details"
		}
		items := pane(itemTitle, m.itemLines(itemWidth-1, mainHeight-1), itemWidth, mainHeight, m.focus == 1)
		detail := m.renderDetail(detailWidth, mainHeight)
		body = lipgloss.JoinHorizontal(lipgloss.Top, features, items, detail)
	}
	header := lipgloss.NewStyle().Foreground(accent).Bold(true).Render("TOKIMEKI JOB SYSTEM") + lipgloss.NewStyle().Foreground(muted).Render("  "+m.paths.Base)
	notice := lipgloss.NewStyle().Foreground(bright).Render(fit(m.noticeLine(), m.width))
	help := lipgloss.NewStyle().Foreground(muted).Render(fit(m.statusLine(), m.width))
	return fit(header, m.width) + "\n" + body + "\n" + notice + "\n" + help
}
func (m model) featureLines(width int) []string {
	lines := []string{""}
	for f, name := range featureNames {
		marker := " "
		if feature(f) == m.feature {
			marker = "▶"
		}
		line := fmt.Sprintf(" %s %-8s %d", marker, name, m.countFor(feature(f)))
		if feature(f) == m.feature {
			line = lipgloss.NewStyle().Foreground(accent).Bold(true).Render(fit(line, width))
		}
		lines = append(lines, line)
	}
	return lines
}
func (m model) itemLines(width, height int) []string {
	if m.itemCount() == 0 {
		return []string{"", "  No " + strings.ToLower(featureNames[m.feature]) + " found"}
	}
	start := max(0, m.selected[m.feature]-height+1)
	lines := make([]string, 0, height)
	for i := start; i < m.itemCount() && len(lines) < height; i++ {
		label, status := m.itemLabel(i)
		marker := " "
		if i == m.selected[m.feature] {
			marker = "›"
		}
		line := fit(marker+" "+cleanText(label), width)
		style := lipgloss.NewStyle().Foreground(statusColor(status))
		if i == m.selected[m.feature] {
			style = style.Bold(true).Underline(true)
		}
		lines = append(lines, style.Render(line))
	}
	return lines
}
func (m model) itemLabel(index int) (string, string) {
	switch m.feature {
	case jobsFeature, historyFeature:
		items := m.jobs
		if m.feature == historyFeature {
			items = m.history
		}
		item := items[index]
		name := item.Name
		if name == "" {
			name = item.Label()
		}
		return fmt.Sprintf("%s %s %s", display.StatusDisplay(item.EffectiveStatus, true), item.ID, name), item.EffectiveStatus
	case runnersFeature:
		item := m.workers[index]
		return item.ID, item.displayStatus()
	case activityFeature:
		item := m.events[index]
		clock := "?"
		if item.TSNs != 0 {
			clock = time.Unix(0, item.TSNs).Local().Format("15:04:05")
		}
		subject := item.JobID
		if subject == "" {
			subject = item.WorkerID
		}
		label := fmt.Sprintf("%s %-12s %s", clock, item.Kind, subject)
		if item.JobID != "" {
			for _, job := range m.history {
				if job.ID == item.JobID {
					if name := display.SmartCommand(job.Label()); name != "" {
						label += "  " + name
					}
					break
				}
			}
		}
		return label, eventStatus(item)
	}
	return "", ""
}

func (m model) runnerStatsLines(width, height int) []string {
	if len(m.workers) == 0 {
		return []string{"", "  No active runners"}
	}
	visible := max(1, height/4)
	start := max(0, m.selected[runnersFeature]-visible+1)
	lines := make([]string, 0, height)
	for i := start; i < len(m.workers) && len(lines) < height; i++ {
		item := m.workers[i]
		marker := " "
		if i == m.selected[runnersFeature] {
			marker = "›"
		}
		label := fmt.Sprintf(" %s %s  HOST %s  BUSY %s", marker, item.ID, item.Hostname, item.slots())
		if i == m.selected[runnersFeature] {
			label = lipgloss.NewStyle().Foreground(accent).Bold(true).Render(fit(label, width))
		}
		lines = append(lines, label)
		if len(item.jobs) == 0 {
			lines = append(lines, "    idle · no active job", "")
			continue
		}
		note := ""
		for _, j := range item.jobs {
			lines = append(lines, fmt.Sprintf("    %s  PID %d", j.label(), j.PID))
			s := j.stats
			if s == nil {
				lines = append(lines, "      Waiting for a fresh stats sample")
				continue
			}
			gpu := fmt.Sprintf("%d%%", s.GPUUtilPercent)
			gpuMem := formatBytes(s.GPUMemBytes)
			if noGPU(s) {
				gpu, gpuMem = "n/a", "n/a"
			} else if s.GPUMemTotalBytes > 0 {
				gpuMem += "/" + formatBytes(s.GPUMemTotalBytes)
			}
			age := "?"
			if s.SampledAtNs != 0 {
				age = fmt.Sprintf("%ds", max(0, int(time.Since(time.Unix(0, s.SampledAtNs)).Seconds())))
			}
			lines = append(lines,
				fmt.Sprintf("      CPU %.0f%%   RSS %s   GPU %s", s.CPUPercent, formatBytes(s.RSSBytes), gpu),
				fmt.Sprintf("      GPU_MEM %s   PROCS %d   AGE %s", gpuMem, s.NumProcs, age),
			)
			if note == "" && s.Note != "" {
				note = "    " + cleanText(s.Note)
			}
		}
		lines = append(lines, note)
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	return lines
}
func statusColor(status string) color.Color {
	switch status {
	case protocol.StatusRunning:
		return lipgloss.Color("6") // ps cyan
	case protocol.StatusQueued:
		return lipgloss.Color("3") // ps yellow
	case protocol.StatusCompleted:
		return lipgloss.Color("2") // ps green
	case "idle":
		return lipgloss.Color("2")
	case protocol.StatusFailed:
		return lipgloss.Color("1") // ps red
	case protocol.StatusKilled, protocol.StatusCrashed:
		return lipgloss.Color("5") // ps magenta
	default:
		return muted // stopped/unknown
	}
}
func eventStatus(item client.Event) string {
	switch item.Kind {
	case client.EventSubmit, client.EventClaim, client.EventCancel:
		return protocol.StatusQueued
	case client.EventStart:
		return protocol.StatusRunning
	case client.EventFinish:
		if item.Status != "" {
			return item.Status
		}
		if item.ExitCode != nil && *item.ExitCode == 0 {
			return protocol.StatusCompleted
		}
		return protocol.StatusFailed
	case client.EventKilled:
		return protocol.StatusKilled
	case client.EventCrashed:
		return protocol.StatusCrashed
	case client.EventRunnerStart, client.EventRunnerReload:
		return protocol.StatusCompleted
	}
	return client.StatusStopped
}
func (m model) renderDetail(width, height int) string {
	switch m.feature {
	case jobsFeature, historyFeature:
		return m.renderJobDetail(width, height)
	case runnersFeature:
		return m.renderRunnerDetail(width, height)
	case activityFeature:
		return m.renderActivityDetail(width, height)
	}
	return pane("Details", nil, width, height, m.focus == 2)
}
func splitHeight(height int) (int, int) {
	upper := max(6, height*2/3)
	lower := height - upper
	if lower < 6 {
		lower = 6
		upper = height - lower
	}
	return upper, lower
}
func (m model) renderJobDetail(width, height int) string {
	selected := m.currentJob()
	upperHeight, lowerHeight := splitHeight(height)
	title := "Logs"
	if selected != nil {
		title += " · " + selected.ID
	}
	if m.scriptOpen && selected != nil {
		title = "Script · " + selected.ID
	}
	upper := pane(title, m.jobDetailLines(upperHeight-1), width, upperHeight, m.focus == 2 && m.detailSection == 0)
	lowerTitle := "Resources / status"
	lowerLines := []string{"  Select a job"}
	if selected != nil {
		lowerLines = jobStatusLines(*selected, m.history, width-1, m.feature == jobsFeature)
	}
	lower := pane(lowerTitle, headWindow(lowerLines, lowerHeight-1, m.lowerOffset()), width, lowerHeight, m.focus == 2 && m.detailSection == 1)
	return upper + "\n" + lower
}
func (m model) jobDetailLines(height int) []string {
	selected := m.currentJob()
	if selected == nil {
		return []string{"", "  Select a job to view details"}
	}
	if m.scriptOpen {
		if len(m.script) == 0 {
			return []string{"  Empty script"}
		}
		return tailWindow(m.script, height, m.upperOffset())
	}
	if len(selected.logs) == 0 {
		return []string{"", "  No log output"}
	}
	return tailWindow(selected.logs, height, m.upperOffset())
}
func (m model) upperOffset() int {
	if m.detailSection == 0 {
		return m.detailOffset
	}
	return 0
}
func (m model) lowerOffset() int {
	if m.detailSection == 1 {
		return m.detailOffset
	}
	return 0
}
func tailWindow(lines []string, height, offset int) []string {
	offset = min(offset, max(0, len(lines)-height))
	end := len(lines) - offset
	return lines[max(0, end-height):end]
}
func headWindow(lines []string, height, offset int) []string {
	start := min(offset, max(0, len(lines)-height))
	return lines[start:min(len(lines), start+height)]
}
func jobStatusLines(item job, all []job, width int, showTiming bool) []string {
	lines := []string{fmt.Sprintf(" %s · %s", display.StatusDisplay(item.EffectiveStatus, false), item.ID)}
	if showTiming {
		lines = append(lines, jobTimingLines(item, time.Now())...)
	}
	if item.Name != "" {
		lines = append(lines, " Name: "+cleanText(item.Name))
	}
	if item.EffectiveStatus == protocol.StatusRunning {
		lines = append(lines, metricLines(&item, width)...)
	} else if item.EffectiveStatus == protocol.StatusQueued {
		pos := "unknown"
		if item.QueuePosition > 0 {
			pos = fmt.Sprintf("%d (stored order)", item.QueuePosition)
		}
		target := item.Worker
		if target == "" {
			target = "any runner"
		}
		lines = append(lines, " Queue: "+pos, " Target: "+target, fmt.Sprintf(" Priority: %d", item.Priority))
		if item.NotBeforeNs != 0 {
			lines = append(lines, " Not before: "+display.RFC3339(item.NotBeforeNs))
		}
		if len(item.DependsOn) > 0 {
			for _, id := range item.DependsOn {
				status := "unknown"
				for _, dep := range all {
					if dep.ID == id {
						status = dep.EffectiveStatus
						break
					}
				}
				lines = append(lines, fmt.Sprintf(" After: %s (%s)", id, status))
			}
		}
	} else {
		if item.ExitCode != nil {
			lines = append(lines, fmt.Sprintf(" Exit: %d", *item.ExitCode))
		}
		if item.FinishedAtNs != 0 {
			lines = append(lines, " Finished: "+display.RFC3339(item.FinishedAtNs))
		}
	}
	if item.Worker != "" && item.EffectiveStatus != protocol.StatusQueued {
		lines = append(lines, " Runner: "+item.Worker)
	}
	if item.Attempts > 0 || item.Retries > 0 {
		lines = append(lines, fmt.Sprintf(" Attempt: %d / %d retries", item.Attempts, item.Retries))
	}
	if item.OrigJob != "" {
		lines = append(lines, " Original: "+item.OrigJob)
	}
	return lines
}
func jobTimingLines(item job, now time.Time) []string {
	submitted := "unknown"
	if item.SubmittedAtNs != 0 {
		submitted = time.Unix(0, item.SubmittedAtNs).Local().Format("2006-01-02 15:04:05 MST")
	}
	lines := []string{" Submitted: " + submitted}
	start := item.SubmittedAtNs
	context := "since submission"
	if item.EffectiveStatus == protocol.StatusRunning {
		start = item.StartedAtNs
		context = "since start"
	}
	elapsed := "unknown"
	if at := time.Unix(0, start); start != 0 && !at.After(now) {
		elapsed = formatElapsed(now.Sub(at))
	}
	return append(lines, fmt.Sprintf(" Elapsed: %s (%s)", elapsed, context))
}
func formatElapsed(d time.Duration) string {
	d = max(0, d.Truncate(time.Second))
	days := d / (24 * time.Hour)
	d %= 24 * time.Hour
	hours := d / time.Hour
	d %= time.Hour
	minutes := d / time.Minute
	seconds := (d % time.Minute) / time.Second
	if days > 0 {
		return fmt.Sprintf("%dd %02dh %02dm %02ds", days, hours, minutes, seconds)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %02dm %02ds", hours, minutes, seconds)
	}
	if minutes > 0 {
		return fmt.Sprintf("%dm %02ds", minutes, seconds)
	}
	return fmt.Sprintf("%ds", seconds)
}
func (m model) renderRunnerDetail(width, height int) string {
	selected := m.currentWorker()
	upperHeight, lowerHeight := splitHeight(height)
	title := "Runner"
	info := []string{"", "  Select a runner"}
	resources := []string{"  No current job sample"}
	if selected != nil {
		title += " · " + selected.ID
		info = []string{" Status: " + selected.displayStatus(), " Host: " + cleanText(selected.Hostname), fmt.Sprintf(" Daemon PID: %d", selected.PID), " Started: " + display.RFC3339(selected.StartedAtNs)}
		if selected.TTLSecs > 0 {
			remaining := "-"
			if selected.RemainingSecs != nil {
				remaining = display.TTL(time.Duration(*selected.RemainingSecs * float64(time.Second)))
			}
			info = append(info, " TTL: "+display.TTL(time.Duration(selected.TTLSecs*float64(time.Second))), " Remaining: "+remaining)
		}
		if selected.HeartbeatNs != 0 {
			info = append(info, fmt.Sprintf(" Heartbeat: %ds ago", max(0, int(time.Since(time.Unix(0, selected.HeartbeatNs)).Seconds()))))
		}
		info = append(info, " Busy compute slots: "+selected.slots())
		if len(selected.jobs) > 0 {
			label := " Running:"
			if !selected.Alive {
				label = " Last recorded:"
			}
			info = append(info, label)
			for _, j := range selected.jobs {
				info = append(info, fmt.Sprintf("  %s  PID %d  %s", j.label(), j.PID, cleanText(j.Label)))
			}
		}
		var sampled []string
		for _, j := range selected.jobs {
			if j.stats == nil {
				continue
			}
			item := job{Job: client.Job{Status: protocol.StatusRunning, EffectiveStatus: protocol.StatusRunning}, stats: j.stats}
			if len(selected.jobs) > 1 {
				sampled = append(sampled, " "+j.label())
			}
			sampled = append(sampled, metricLines(&item, width-1)...)
		}
		if len(sampled) > 0 {
			resources = sampled
		}
	}
	upper := pane(title, headWindow(info, upperHeight-1, m.upperOffset()), width, upperHeight, m.focus == 2 && m.detailSection == 0)
	lower := pane("Resources", headWindow(resources, lowerHeight-1, m.lowerOffset()), width, lowerHeight, m.focus == 2 && m.detailSection == 1)
	return upper + "\n" + lower
}
func (m model) renderActivityDetail(width, height int) string {
	selected := m.currentEvent()
	lines := []string{"", "  Select an event"}
	if selected != nil {
		lines = []string{" Kind: " + selected.Kind, " Time: " + display.RFC3339Nano(selected.TSNs)}
		if selected.JobID != "" {
			lines = append(lines, " Job: "+selected.JobID)
		}
		if selected.WorkerID != "" {
			lines = append(lines, " Runner: "+selected.WorkerID)
		}
		if selected.Status != "" {
			lines = append(lines, " Status: "+selected.Status)
		}
		if selected.ExitCode != nil {
			lines = append(lines, fmt.Sprintf(" Exit: %d", *selected.ExitCode))
		}
		keys := make([]string, 0, len(selected.Fields))
		for key := range selected.Fields {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			lines = append(lines, " "+cleanText(key)+": "+cleanText(selected.Fields[key]))
		}
		if selected.JobID != "" {
			for _, item := range m.history {
				if item.ID == selected.JobID {
					lines = append(lines, "", " Current job status: "+item.EffectiveStatus)
					break
				}
			}
		}
	}
	return pane("Event", tailWindow(lines, height-1, m.detailOffset), width, height, m.focus == 2)
}
func metricLines(selected *job, width int) []string {
	if selected == nil || selected.stats == nil {
		return []string{"  Waiting for a fresh runner sample…"}
	}
	s := selected.stats
	barWidth := min(20, max(8, width-25))
	lines := []string{fmt.Sprintf(" CPU  %s  %.0f%%", bar(s.CPUPercent, barWidth), s.CPUPercent), fmt.Sprintf(" RAM  %s RSS", formatBytes(s.RSSBytes))}
	if selected.MemMB > 0 {
		pct := 100 * float64(s.RSSBytes) / float64(selected.MemMB*1024*1024)
		lines[1] = fmt.Sprintf(" RAM  %s  %s / %d MiB hint", bar(pct, barWidth), formatBytes(s.RSSBytes), selected.MemMB)
	}
	if noGPU(s) {
		lines = append(lines, " GPU  n/a · "+s.Note)
	} else {
		lines = append(lines, fmt.Sprintf(" GPU  %s  %d%%", bar(float64(s.GPUUtilPercent), barWidth), s.GPUUtilPercent))
		if s.GPUMemTotalBytes > 0 {
			pct := 100 * float64(s.GPUMemBytes) / float64(s.GPUMemTotalBytes)
			lines = append(lines, fmt.Sprintf(" VRAM %s  %s / %s", bar(pct, barWidth), formatBytes(s.GPUMemBytes), formatBytes(s.GPUMemTotalBytes)))
		}
	}
	when := time.Unix(0, s.SampledAtNs)
	lines = append(lines, fmt.Sprintf(" %d processes · sampled %ds ago", s.NumProcs, max(0, int(time.Since(when).Seconds()))))
	return lines
}

// noGPU reports a sample without GPU data: no nvidia-smi, or a platform the
// runner cannot sample at all.
func noGPU(s *client.StatsSample) bool {
	return !s.GPUAvailable || strings.Contains(s.Note, "non-linux")
}
func bar(percent float64, width int) string {
	filled := max(0, min(width, int(percent*float64(width)/100)))
	return "[" + strings.Repeat("█", filled) + strings.Repeat("░", width-filled) + "]"
}
func formatBytes(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	value := float64(n)
	for _, unit := range []string{"KiB", "MiB", "GiB", "TiB"} {
		value /= 1024
		if value < 1024 || unit == "TiB" {
			return fmt.Sprintf("%.1f %s", value, unit)
		}
	}
	return "0 B"
}
func pane(title string, lines []string, width, height int, focused bool) string {
	separatorColor := muted
	titleColor := muted
	if focused {
		separatorColor = accent
		titleColor = accent
	}
	inside := width - 1
	separator := lipgloss.NewStyle().Foreground(separatorColor).Render("│")
	header := lipgloss.NewStyle().Foreground(titleColor).Bold(focused).Render(fit(" "+title, inside))
	out := []string{header + strings.Repeat(" ", max(0, inside-lipgloss.Width(header))) + separator}
	for i := 0; i < height-1; i++ {
		line := ""
		if i < len(lines) {
			line = fit(lines[i], inside)
		}
		out = append(out, line+strings.Repeat(" ", max(0, inside-lipgloss.Width(line)))+separator)
	}
	return strings.Join(out, "\n")
}
func fit(s string, width int) string {
	if width <= 0 {
		return ""
	}
	return ansi.Truncate(s, width, "…")
}
func cleanText(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\t' {
			return ' '
		}
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, ansi.Strip(s))
}
