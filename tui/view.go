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
	"github.com/ngocphat/tokimeki/client"
	"github.com/ngocphat/tokimeki/eventlog"
	"github.com/ngocphat/tokimeki/protocol"
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
	itemTitle := fmt.Sprintf("%s (%d)", featureNames[m.feature], m.itemCount())
	if m.feature == historyFeature {
		itemTitle += " · submitted ↑"
	}
	items := pane(itemTitle, m.itemLines(itemWidth-1, mainHeight-1), itemWidth, mainHeight, m.focus == 1)
	detail := m.renderDetail(detailWidth, mainHeight)
	header := lipgloss.NewStyle().Foreground(accent).Bold(true).Render("TOKIMEKI") + lipgloss.NewStyle().Foreground(muted).Render("  "+m.paths.Base)
	notice := lipgloss.NewStyle().Foreground(bright).Render(fit(m.noticeLine(), m.width))
	help := lipgloss.NewStyle().Foreground(muted).Render(fit(m.statusLine(), m.width))
	return fit(header, m.width) + "\n" + lipgloss.JoinHorizontal(lipgloss.Top, features, items, detail) + "\n" + notice + "\n" + help
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
			name = item.Command
		}
		return fmt.Sprintf("%s %s %s", client.StatusDisplay(item.effectiveStatus, true), item.JobID, name), item.effectiveStatus
	case runnersFeature:
		item := m.workers[index]
		status := item.displayStatus()
		glyph := client.StatusDisplay(item.status, true)
		if status == "idle" {
			glyph = "◇"
		}
		return fmt.Sprintf("%s %s %s", glyph, item.info.WorkerID, item.info.Hostname), status
	case activityFeature:
		item := m.events[index]
		at, err := time.Parse(time.RFC3339Nano, item.TS)
		clock := "?"
		if err == nil {
			clock = at.Local().Format("15:04:05")
		}
		subject := item.JobID
		if subject == "" {
			subject = item.WorkerID
		}
		label := fmt.Sprintf("%s %-12s %s", clock, item.Kind, subject)
		if item.JobID != "" {
			for _, job := range m.history {
				if job.JobID == item.JobID {
					if name := client.SmartCommand(job.Command); name != "" {
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
func eventStatus(item eventlog.Event) string {
	switch item.Kind {
	case eventlog.KindSubmit, eventlog.KindClaim, eventlog.KindCancel:
		return protocol.StatusQueued
	case eventlog.KindStart:
		return protocol.StatusRunning
	case eventlog.KindFinish:
		if item.Status != "" {
			return item.Status
		}
		if item.ExitCode != nil && *item.ExitCode == 0 {
			return protocol.StatusCompleted
		}
		return protocol.StatusFailed
	case eventlog.KindKilled:
		return protocol.StatusKilled
	case eventlog.KindCrashed:
		return protocol.StatusCrashed
	case eventlog.KindRunnerStart:
		return protocol.StatusCompleted
	}
	return "stopped"
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
		title += " · " + selected.JobID
	}
	if m.scriptOpen && selected != nil {
		title = "Script · " + selected.JobID
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
	lines := []string{fmt.Sprintf(" %s · %s", client.StatusDisplay(item.effectiveStatus, false), item.JobID)}
	if showTiming {
		lines = append(lines, jobTimingLines(item, time.Now())...)
	}
	if item.Name != "" {
		lines = append(lines, " Name: "+cleanText(item.Name))
	}
	if item.effectiveStatus == protocol.StatusRunning {
		lines = append(lines, metricLines(&item, width)...)
	} else if item.effectiveStatus == protocol.StatusQueued {
		pos := "unknown"
		if item.queuePosition > 0 {
			pos = fmt.Sprintf("%d (stored order)", item.queuePosition)
		}
		target := item.WorkerID
		if target == "" {
			target = "any runner"
		}
		lines = append(lines, " Queue: "+pos, " Target: "+target, fmt.Sprintf(" Priority: %d", item.Priority))
		if item.NotBefore != "" {
			lines = append(lines, " Not before: "+item.NotBefore)
		}
		if len(item.DependsOn) > 0 {
			for _, id := range item.DependsOn {
				status := "unknown"
				for _, dep := range all {
					if dep.JobID == id {
						status = dep.effectiveStatus
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
		if item.FinishedAt != nil {
			lines = append(lines, " Finished: "+*item.FinishedAt)
		}
	}
	if item.WorkerID != "" && item.effectiveStatus != protocol.StatusQueued {
		lines = append(lines, " Runner: "+item.WorkerID)
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
	if at, err := time.Parse(time.RFC3339Nano, item.SubmittedAt); err == nil {
		submitted = at.Local().Format("2006-01-02 15:04:05 MST")
	}
	lines := []string{" Submitted: " + submitted}
	start := item.SubmittedAt
	context := "since submission"
	if item.effectiveStatus == protocol.StatusRunning {
		start = ""
		if item.StartedAt != nil {
			start = *item.StartedAt
		}
		context = "since start"
	}
	elapsed := "unknown"
	if at, err := time.Parse(time.RFC3339Nano, start); err == nil && !at.After(now) {
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
		title += " · " + selected.info.WorkerID
		info = []string{" Status: " + selected.displayStatus(), " Host: " + cleanText(selected.info.Hostname), fmt.Sprintf(" Daemon PID: %d", selected.info.PID), " Started: " + selected.info.StartedAt}
		if !selected.heartbeat.IsZero() {
			info = append(info, fmt.Sprintf(" Heartbeat: %ds ago", max(0, int(time.Since(selected.heartbeat).Seconds()))))
		}
		if selected.current != nil {
			label := " Current"
			if selected.status != "running" {
				label = " Last recorded"
			}
			info = append(info, fmt.Sprintf("%s job: %s", label, selected.current.JobID), fmt.Sprintf(" Job PID: %d", selected.current.PID), " Command: "+cleanText(selected.current.Command))
		}
		if selected.stats != nil {
			item := job{JobMeta: protocol.JobMeta{Status: protocol.StatusRunning}, effectiveStatus: protocol.StatusRunning, stats: selected.stats}
			resources = metricLines(&item, width-1)
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
		lines = []string{" Kind: " + selected.Kind, " Time: " + selected.TS}
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
				if item.JobID == selected.JobID {
					lines = append(lines, "", " Current job status: "+item.effectiveStatus)
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
	if strings.Contains(s.Note, "nvidia-smi") || strings.Contains(s.Note, "non-linux") {
		lines = append(lines, " GPU  n/a · "+s.Note)
	} else {
		lines = append(lines, fmt.Sprintf(" GPU  %s  %d%%", bar(float64(s.GPUUtil), barWidth), s.GPUUtil))
		if s.GPUMemTotalBytes > 0 {
			pct := 100 * float64(s.GPUMemBytes) / float64(s.GPUMemTotalBytes)
			lines = append(lines, fmt.Sprintf(" VRAM %s  %s / %s", bar(pct, barWidth), formatBytes(s.GPUMemBytes), formatBytes(s.GPUMemTotalBytes)))
		}
	}
	when, _ := time.Parse(time.RFC3339, s.SampledAt)
	lines = append(lines, fmt.Sprintf(" %d processes · sampled %ds ago", s.NumProcs, max(0, int(time.Since(when).Seconds()))))
	return lines
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
