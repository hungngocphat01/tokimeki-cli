package main

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/hungngocphat01/tokimeki/client"
	"github.com/ngocphat/tokimeki-cli/display"
)

// runnerStatus is the user-facing runner status: "running" or "stopped".
func runnerStatus(r client.Runner) string {
	if r.Alive {
		return "running"
	}
	return client.StatusStopped
}

// printRunners renders the runners table (runners already ordered: alive
// first, then by ID).
func printRunners(out io.Writer, runners []client.Runner) error {
	if len(runners) == 0 {
		fmt.Fprintln(out, "No workers found.")
		fmt.Fprintln(out, "Hint: start one with `tok runner --id <name>`.")
		return nil
	}

	// Layout with plain text via tabwriter, then paint colors per line. ANSI
	// escapes inside tabwriter cells throw off column-width computation
	// because the escape bytes are counted as printable.
	buf := &strings.Builder{}
	w := tabwriter.NewWriter(buf, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "WORKER\tHOSTNAME\tPID\tSTARTED\tTTL\tREMAINING\tSTATUS")
	for _, r := range runners {
		hostname, pid, started := "?", "?", "?"
		ttl, remaining := "-", "-"
		if r.HasInfo {
			hostname = r.Hostname
			pid = strconv.Itoa(r.PID)
			started = display.LocalTime(r.StartedAtNs)
			if r.TTLSecs > 0 {
				ttl = display.TTL(secs(r.TTLSecs))
			}
			if r.RemainingSecs != nil {
				remaining = display.TTL(secs(*r.RemainingSecs))
			}
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			r.ID, hostname, pid, started, ttl, remaining, display.StatusDisplay(runnerStatus(r), false))
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
	for i, r := range runners {
		status := display.StatusDisplay(runnerStatus(r), false)
		color := display.LightGray
		if r.Alive {
			color = display.Green
		}
		lines[i+1] = strings.TrimSuffix(lines[i+1], status) + color + status + display.Reset
	}
	_, err := fmt.Fprintln(out, strings.Join(lines, "\n"))
	return err
}

func secs(s float64) time.Duration { return time.Duration(s * float64(time.Second)) }

// printStats renders the stats table and then, on stderr, each distinct
// sampler note.
func printStats(out io.Writer, samples []client.StatsSample) error {
	if len(samples) == 0 {
		fmt.Fprintln(out, "No active jobs.")
		fmt.Fprintln(out, "Hint: `tok stats` only reports jobs currently running on a runner. Submit one with `tok submit -c <command>`.")
		return nil
	}

	now := time.Now()
	buf := &strings.Builder{}
	w := tabwriter.NewWriter(buf, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "RUNNER\tHOST\tJOB\tPID\tCPU%\tRSS\tGPU%\tGPU_MEM\tPROCS\tAGE")
	for _, s := range samples {
		age := "?"
		if s.SampledAtNs != 0 {
			age = display.StatsAge(now.Sub(time.Unix(0, s.SampledAtNs)))
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%d\t%s\n",
			s.WorkerID,
			dash(s.Hostname),
			display.Truncate(s.JobID, 8),
			psPID(client.Job{PID: s.PID}),
			fmt.Sprintf("%.0f", s.CPUPercent),
			display.Bytes(s.RSSBytes),
			gpuUtilStr(s),
			gpuMemStr(s),
			s.NumProcs,
			age,
		)
	}
	w.Flush()

	if display.StdoutIsTTY() {
		lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
		lines[0] = display.Bold + lines[0] + display.Reset
		// Paint CPU% and GPU% cells by utilization tier, line by line, so
		// tabwriter's widths (computed uncolored) stay correct.
		for i, s := range samples {
			lines[i+1] = paintCell(lines[i+1], fmt.Sprintf("%.0f", s.CPUPercent), cpuColor(s.CPUPercent))
			lines[i+1] = paintCell(lines[i+1], gpuUtilStr(s), gpuColor(s))
		}
		fmt.Fprintln(out, strings.Join(lines, "\n"))
	} else {
		fmt.Fprint(out, buf.String())
	}

	seen := map[string]bool{}
	for _, s := range samples {
		if s.Note != "" && !seen[s.Note] {
			fmt.Fprintf(os.Stderr, "note: %s\n", s.Note)
			seen[s.Note] = true
		}
	}
	return nil
}

// cpuColor classifies CPU utilization by absolute percent (sum across cores).
//
//	>= 200%: green   (multi-core in use; healthy multi-thread workload)
//	 50-199%: yellow (single-thread or partial utilization)
//	 <  50%: red    (mostly idle — likely IO-bound or stalled)
//
// These are crude thresholds — they don't know the available core count.
// They work well enough as a first-glance indicator on typical ML nodes.
func cpuColor(cpu float64) string {
	switch {
	case cpu >= 200:
		return display.Green
	case cpu >= 50:
		return display.Yellow
	default:
		return display.Red
	}
}

// gpuColor classifies GPU SM utilization. n/a (no GPU) is uncolored.
func gpuColor(s client.StatsSample) string {
	if !s.GPUAvailable {
		return ""
	}
	switch {
	case s.GPUUtilPercent >= 80:
		return display.Green
	case s.GPUUtilPercent >= 40:
		return display.Yellow
	default:
		return display.Red
	}
}

// paintCell finds the first occurrence of `cell` in `line` (matching whole
// fields surrounded by whitespace or line edges) and wraps it in `color`.
// No-op when color is empty or the cell isn't found.
func paintCell(line, cell, color string) string {
	if color == "" || cell == "" {
		return line
	}
	idx := strings.Index(line, cell)
	if idx < 0 {
		return line
	}
	// Guard against partial matches: require the char before/after the cell
	// to be whitespace or the line edge. Otherwise "50" would match inside "250".
	before := idx == 0 || line[idx-1] == ' ' || line[idx-1] == '\t'
	end := idx + len(cell)
	after := end == len(line) || line[end] == ' ' || line[end] == '\t'
	if !before || !after {
		return line
	}
	return line[:idx] + color + cell + display.Reset + line[end:]
}

func gpuUtilStr(s client.StatsSample) string {
	if !s.GPUAvailable {
		return "n/a"
	}
	return fmt.Sprintf("%d%%", s.GPUUtilPercent)
}

// gpuMemStr renders "used/total" when capacity is known, otherwise just used.
// Examples: "14.8G/40.0G", "14.8G" (older runner without total), "n/a".
func gpuMemStr(s client.StatsSample) string {
	if !s.GPUAvailable {
		return "n/a"
	}
	if s.GPUMemTotalBytes > 0 {
		return display.Bytes(s.GPUMemBytes) + "/" + display.Bytes(s.GPUMemTotalBytes)
	}
	return display.Bytes(s.GPUMemBytes)
}
