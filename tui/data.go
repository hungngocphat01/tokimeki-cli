package tui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/hungngocphat01/tokimeki/client"
	"github.com/hungngocphat01/tokimeki/protocol"
	"github.com/ngocphat/tokimeki-cli/display"
)

type job struct {
	client.Job
	stats *client.StatsSample
	logs  []string
}

type worker struct {
	client.Runner
	jobs []runnerJob // running jobs in start order
}

// runnerJob is one job a runner is running, with its fresh stats sample.
type runnerJob struct {
	client.CurrentJob
	stats *client.StatsSample
}

// slots renders the runner's busy compute slots, e.g. "2/3".
func (w worker) slots() string {
	busy := 0
	for _, j := range w.jobs {
		if j.Kind != protocol.KindCoordinator {
			busy++
		}
	}
	return display.RunnerSlots(busy, w.ComputeThreads)
}

// label renders a running job as "[C] id" for coordinators, "id" otherwise.
func (j runnerJob) label() string {
	if j.Kind == protocol.KindCoordinator {
		return "[C] " + j.JobID
	}
	return j.JobID
}

func (w worker) displayStatus() string {
	if !w.Alive {
		return client.StatusStopped
	}
	if len(w.jobs) == 0 {
		return "idle"
	}
	return "running"
}

type snapshot struct {
	jobs    []job
	workers []worker
	events  []client.Event
}

// loadSnapshot reads persisted state only. Actions use the existing request
// protocol separately; refreshing the dashboard never contacts a runner.
func loadSnapshot(paths *protocol.Paths) (snapshot, error) {
	c := client.New(paths.Base)
	runners, err := c.ListRunners(client.ListRunnersOpts{})
	if err != nil {
		return snapshot{}, err
	}
	samples, err := c.ListStats()
	if err != nil {
		return snapshot{}, err
	}
	workers := make([]worker, 0, len(runners))
	for _, r := range runners {
		if !r.HasInfo {
			continue
		}
		item := worker{Runner: r}
		for _, run := range r.Running {
			if run.JobID == "" {
				continue
			}
			item.jobs = append(item.jobs, runnerJob{CurrentJob: run, stats: freshSample(samples, r.ID, run.JobID)})
		}
		workers = append(workers, item)
	}
	all, err := c.ListJobs(client.ListJobsOpts{All: true})
	if err != nil {
		return snapshot{}, err
	}
	events, err := c.RecentEvents(200)
	if err != nil {
		return snapshot{}, err
	}
	return snapshot{jobs: wrapJobs(all), workers: workers, events: events}, nil
}

func wrapJobs(jobs []client.Job) []job {
	out := make([]job, len(jobs))
	for i, j := range jobs {
		out[i] = job{Job: j}
	}
	return out
}

// freshSample returns workerID's sample if it is recent and for jobID.
func freshSample(samples []client.StatsSample, workerID, jobID string) *client.StatsSample {
	for i := range samples {
		s := &samples[i]
		if s.WorkerID == workerID && s.JobID == jobID && !s.Stale {
			return s
		}
	}
	return nil
}

func loadStats(paths *protocol.Paths, selected job) *client.StatsSample {
	if selected.Status != protocol.StatusRunning || selected.Worker == "" {
		return nil
	}
	samples, err := client.New(paths.Base).ListStats()
	if err != nil {
		return nil
	}
	return freshSample(samples, selected.Worker, selected.ID)
}

func loadLogs(paths *protocol.Paths, selected job, limit int) []string {
	c := client.New(paths.Base)
	stdout := tailLines(c, selected.ID, client.StreamStdout, limit)
	stderr := tailLines(c, selected.ID, client.StreamStderr, limit)
	if len(stderr) == 0 {
		return stdout
	}
	if len(stdout) == 0 {
		return append([]string{"stderr"}, stderr...)
	}
	return append(append(stdout, "", "stderr"), stderr...)
}

// tailLines returns a stream's last lines with terminal control sequences
// removed.
func tailLines(c *client.Client, jobID string, stream client.LogStream, limit int) []string {
	lines, err := c.LogTail(jobID, stream, limit)
	if err != nil {
		return nil
	}
	for i, line := range lines {
		line = ansi.Strip(line)
		lines[i] = strings.Map(func(r rune) rune {
			if r == '\t' {
				return ' '
			}
			if r < 32 || r == 127 {
				return -1
			}
			return r
		}, line)
	}
	return lines
}
