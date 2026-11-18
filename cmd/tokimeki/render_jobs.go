package main

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/hungngocphat01/tokimeki/client"
	"github.com/hungngocphat01/tokimeki/protocol"
	"github.com/ngocphat/tokimeki-cli/display"
)

// printPS renders jobs (already filtered and ordered by the engine) as the
// ps table, coloring rows by status when stdout is a terminal.
func printPS(out io.Writer, jobs []client.Job) error {
	if len(jobs) == 0 {
		fmt.Fprintln(out, "No jobs found.")
		fmt.Fprintln(out, "Hint: submit one with `tok submit -c '<command>'` (use -a to also see finished jobs).")
		return nil
	}

	// Show PRIO only when at least one row has a non-default priority. Most
	// users never set --priority, so the column would just be a wall of zeros.
	showPrio := false
	for _, job := range jobs {
		if job.Priority != 0 {
			showPrio = true
			break
		}
	}

	// Layout plain via tabwriter, then colorize each finished line. ANSI in
	// cells confuses tabwriter's column-width math.
	buf := &strings.Builder{}
	w := tabwriter.NewWriter(buf, 0, 4, 2, ' ', 0)
	if showPrio {
		fmt.Fprintln(w, "JOB_ID\tRUNNER\tPID\tPRIO\tGROUP\tCOMMAND\tSTARTED\tELAPSED\tSTATUS")
	} else {
		fmt.Fprintln(w, "JOB_ID\tRUNNER\tPID\tGROUP\tCOMMAND\tSTARTED\tELAPSED\tSTATUS")
	}
	for _, job := range jobs {
		cells := []string{job.ID, dash(job.Worker), psPID(job)}
		if showPrio {
			cells = append(cells, strconv.Itoa(job.Priority))
		}
		cells = append(cells,
			psName(job),
			display.Truncate(psCommand(job), 40),
			display.LocalTime(job.StartedAtNs),
			psElapsed(job),
			display.StatusDisplay(job.EffectiveStatus, false),
		)
		fmt.Fprintln(w, strings.Join(cells, "\t"))
	}
	if err := w.Flush(); err != nil {
		return err
	}

	if !display.StdoutIsTTY() {
		_, err := fmt.Fprint(out, buf.String())
		return err
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	lines[0] = display.Bold + lines[0] + display.Reset
	for i, job := range jobs {
		if col := display.GroupColor(display.StatusGroup(job.EffectiveStatus)); col != "" {
			lines[i+1] = col + lines[i+1] + display.Reset
		}
	}
	_, err := fmt.Fprintln(out, strings.Join(lines, "\n"))
	return err
}

func psPID(job client.Job) string {
	if job.PID <= 0 {
		return "-"
	}
	return strconv.Itoa(job.PID)
}

// psCommand shortens the command and marks coordinators with "[C] ".
func psCommand(job client.Job) string {
	cmd := display.SmartCommand(job.Command)
	if job.Kind == protocol.KindCoordinator {
		cmd = "[C] " + cmd
	}
	return cmd
}

// printGroup renders a group report: a summary, then its jobs as the ps
// table.
func printGroup(out io.Writer, rep client.GroupReport) error {
	fmt.Fprintf(out, "Group %s\n", rep.Group)
	fmt.Fprintf(out, "Coordinators: %s\n", groupCounts(rep.Coordinators))
	fmt.Fprintf(out, "Compute:      %s\n", groupCounts(rep.Compute))
	if len(rep.Failed) > 0 {
		fmt.Fprintf(out, "Failed:       %s\n", jobList(rep.Failed))
	}
	if len(rep.Stopped) > 0 {
		parts := make([]string, len(rep.Stopped))
		for i, job := range rep.Stopped {
			parts[i] = fmt.Sprintf("%s (on %s)", job.ID, job.Worker)
		}
		fmt.Fprintf(out, "Stopped:      %s — runner gone\n", strings.Join(parts, ", "))
	}
	fmt.Fprintln(out)
	return printPS(out, rep.Jobs)
}

// groupCounts renders "8 total · 2 queued · ..." with zero counts omitted.
func groupCounts(g client.GroupCounts) string {
	parts := []string{fmt.Sprintf("%d total", g.Total)}
	for _, c := range []struct {
		n     int
		label string
	}{
		{g.Queued, "queued"}, {g.Running, "running"}, {g.Stopped, "stopped"}, {g.Completed, "completed"},
		{g.Failed, "failed"}, {g.Killed, "killed"}, {g.Crashed, "crashed"},
	} {
		if c.n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", c.n, c.label))
		}
	}
	return strings.Join(parts, " · ")
}

// jobList renders "id (status, exit N), ..." for the group summary.
func jobList(jobs []client.Job) string {
	parts := make([]string, len(jobs))
	for i, job := range jobs {
		detail := job.EffectiveStatus
		if job.ExitCode != nil && *job.ExitCode != 0 && job.EffectiveStatus == protocol.StatusFailed {
			detail += fmt.Sprintf(", exit %d", *job.ExitCode)
		}
		parts[i] = fmt.Sprintf("%s (%s)", job.ID, detail)
	}
	return strings.Join(parts, ", ")
}

// psName renders the job's tag for the table, or "-" when unset.
func psName(job client.Job) string {
	if job.Name == "" {
		return "-"
	}
	return display.Truncate(job.Name, 16)
}

func psElapsed(job client.Job) string {
	if job.StartedAtNs == 0 {
		return "-"
	}
	return display.Duration(job.ElapsedSecs)
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// topData is everything one top frame shows.
type topData struct {
	base    string
	now     time.Time
	runners []client.Runner // all runners, alive first
	jobs    []client.Job    // all jobs
	recent  []client.Job    // terminal within the last 10 minutes, newest first
}

func loadTop(c *client.Client, base string) (topData, error) {
	d := topData{base: base, now: time.Now()}
	var err error
	if d.runners, err = c.ListRunners(client.ListRunnersOpts{All: true}); err != nil {
		return d, err
	}
	if d.jobs, err = c.ListJobs(client.ListJobsOpts{All: true}); err != nil {
		return d, err
	}
	d.recent, err = c.RecentJobs(10*time.Minute, 8)
	return d, err
}

// printTop prints a one-shot dashboard of runners, active jobs, and recently
// finished jobs.
func printTop(out io.Writer, d topData, plain bool) {
	plain = plain || !display.StdoutIsTTY()
	useColor := display.StdoutIsTTY() && !plain
	title := fmt.Sprintf("tokimeki — %s", d.now.Format("15:04:05"))
	subtitle := fmt.Sprintf("base=%s", d.base)
	if useColor {
		title = display.Bold + title + display.Reset
		subtitle = display.LightGray + subtitle + display.Reset
	}
	fmt.Fprintln(out, title+"  "+subtitle)
	fmt.Fprintln(out)

	runners, jobs, recent := topRunnersPanel(d, useColor), topJobsPanel(d, useColor), topRecentPanel(d, useColor)
	if plain {
		fmt.Fprintln(out, "=== RUNNERS ===")
		fmt.Fprintln(out, runners)
		fmt.Fprintln(out, "\n=== JOBS ===")
		fmt.Fprintln(out, jobs)
		fmt.Fprintln(out, "\n=== RECENT ===")
		fmt.Fprintln(out, recent)
	} else {
		printTopBox(out, "RUNNERS", runners, useColor)
		fmt.Fprintln(out)
		printTopBox(out, "JOBS", jobs, useColor)
		fmt.Fprintln(out)
		printTopBox(out, "RECENT", recent, useColor)
	}
	hint := "(q / Ctrl-C to quit)"
	if useColor {
		hint = display.LightGray + hint + display.Reset
	}
	fmt.Fprintln(out, "\n"+hint)
}

// printTopBox prints `body` (multi-line, no trailing newline) inside a
// rounded box with a title bar.
func printTopBox(out io.Writer, title, body string, useColor bool) {
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	width := display.VisibleLen(title) + 4
	for _, ln := range lines {
		if w := display.VisibleLen(ln); w+2 > width {
			width = w + 2
		}
	}
	const (
		tl, tr, bl, br = "╭", "╮", "╰", "╯"
		h, v           = "─", "│"
	)
	colorize := func(s, prefix string) string {
		if !useColor || prefix == "" {
			return s
		}
		return prefix + s + display.Reset
	}
	titleStyled := colorize(" "+title+" ", display.Bold+display.Cyan)
	border := strings.Repeat(h, width-display.VisibleLen(" "+title+" "))
	fmt.Fprintln(out, colorize(tl, display.Cyan)+titleStyled+colorize(border+tr, display.Cyan))
	for _, ln := range lines {
		pad := max(0, width-display.VisibleLen(ln))
		fmt.Fprintln(out, colorize(v, display.Cyan)+ln+strings.Repeat(" ", pad)+colorize(v, display.Cyan))
	}
	fmt.Fprintln(out, colorize(bl+strings.Repeat(h, width)+br, display.Cyan))
}

// paintPanel wraps the first occurrence of the first matching candidate in
// each line in its color. Line 0 (the header) is bolded.
// Colors go on after tabwriter layout, which would count escapes as width.
func paintPanel(out string, useColor bool, candidates []string, colors []string) string {
	if !useColor {
		return out
	}
	lines := strings.Split(out, "\n")
	lines[0] = display.Bold + lines[0] + display.Reset
	for i := 1; i < len(lines); i++ {
		for k, cand := range candidates {
			if idx := strings.Index(lines[i], cand); idx >= 0 {
				lines[i] = lines[i][:idx] + colors[k] + cand + display.Reset + lines[i][idx+len(cand):]
				break
			}
		}
	}
	return strings.Join(lines, "\n")
}

func topRunnersPanel(d topData, useColor bool) string {
	if len(d.runners) == 0 {
		return "  (no runners)"
	}
	runningStr := display.StatusDisplay(protocol.StatusRunning, false)
	stoppedStr := display.StatusDisplay(client.StatusStopped, false)
	buf := &strings.Builder{}
	w := tabwriter.NewWriter(buf, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "  STATUS\tWORKER\tHOST\tPID\tHEARTBEAT")
	for _, r := range d.runners {
		status := stoppedStr
		if r.Alive {
			status = runningStr
		}
		host, pid := "?", "?"
		if r.HasInfo {
			host, pid = r.Hostname, strconv.Itoa(r.PID)
		}
		age := "?"
		if r.HeartbeatNs != 0 {
			age = display.Age(d.now.Sub(time.Unix(0, r.HeartbeatNs)))
		}
		fmt.Fprintf(w, "  %s\t%s\t%s\t%s\t%s\n", status, r.ID, host, pid, age)
	}
	w.Flush()
	return paintPanel(strings.TrimRight(buf.String(), "\n"), useColor,
		[]string{runningStr, stoppedStr}, []string{display.Green, display.LightGray})
}

func topJobsPanel(d topData, useColor bool) string {
	// By persisted status: a running job on a dead runner still shows here.
	var running, queued []client.Job
	for _, job := range d.jobs {
		switch job.Status {
		case protocol.StatusRunning:
			running = append(running, job)
		case protocol.StatusQueued:
			queued = append(queued, job)
		}
	}
	if len(running)+len(queued) == 0 {
		return "  (no active jobs)"
	}
	sort.SliceStable(running, func(i, j int) bool { return running[i].StartedAtNs < running[j].StartedAtNs })
	sort.SliceStable(queued, func(i, j int) bool {
		if queued[i].Priority != queued[j].Priority {
			return queued[i].Priority > queued[j].Priority
		}
		return queued[i].SubmittedAtNs < queued[j].SubmittedAtNs
	})

	rGlyph := display.StatusDisplay(protocol.StatusRunning, true)
	qGlyph := display.StatusDisplay(protocol.StatusQueued, true)
	buf := &strings.Builder{}
	w := tabwriter.NewWriter(buf, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "  STATE\tJOB_ID\tWORKER\tPRIO\tCMD\tELAPSED")
	for _, job := range running {
		fmt.Fprintf(w, "  %s\t%s\t%s\t%d\t%s\t%s\n",
			rGlyph, job.ID, job.Worker, job.Priority, display.Truncate(display.SmartCommand(job.Command), 30), psElapsed(job))
	}
	for _, job := range queued {
		fmt.Fprintf(w, "  %s\t%s\t%s\t%d\t%s\t-\n",
			qGlyph, job.ID, dash(job.Worker), job.Priority, display.Truncate(display.SmartCommand(job.Command), 30))
	}
	w.Flush()
	return paintPanel(strings.TrimRight(buf.String(), "\n"), useColor,
		[]string{rGlyph, qGlyph}, []string{display.Cyan, display.Yellow})
}

// topRecentPanel shows jobs that reached a terminal state recently, newest
// first.
func topRecentPanel(d topData, useColor bool) string {
	if len(d.recent) == 0 {
		return "  (no recent jobs)"
	}
	buf := &strings.Builder{}
	w := tabwriter.NewWriter(buf, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "  STATUS\tJOB_ID\tWORKER\tEXIT\tCMD\tFINISHED")
	for _, job := range d.recent {
		exit := "-"
		if job.ExitCode != nil {
			exit = strconv.Itoa(*job.ExitCode)
		}
		fmt.Fprintf(w, "  %s\t%s\t%s\t%s\t%s\t%s ago\n",
			display.StatusDisplay(job.Status, false), job.ID, job.Worker, exit,
			display.Truncate(display.SmartCommand(job.Command), 30), display.Age(d.now.Sub(time.Unix(0, job.FinishedAtNs))))
	}
	w.Flush()
	terminal := []string{protocol.StatusCompleted, protocol.StatusFailed, protocol.StatusKilled, protocol.StatusCrashed}
	var disps, colors []string
	for _, st := range terminal {
		disps = append(disps, display.StatusDisplay(st, false))
		colors = append(colors, display.StatusColor(st))
	}
	return paintPanel(strings.TrimRight(buf.String(), "\n"), useColor, disps, colors)
}
