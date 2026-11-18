package tui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/hungngocphat01/tokimeki/client"
	"github.com/hungngocphat01/tokimeki/protocol"
)

type job struct {
	client.Job
	stats *client.StatsSample
	logs  []string
}

type worker struct {
	client.Runner
	Current *client.CurrentJob // stopgap: first running job (Task 10 shows all)
	stats   *client.StatsSample
}

func (w worker) displayStatus() string {
	if !w.Alive {
		return client.StatusStopped
	}
	if w.Current == nil {
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
		if len(r.Running) > 0 && r.Running[0].JobID != "" {
			item.Current = &r.Running[0]
		}
		if item.Current != nil {
			item.stats = freshSample(samples, r.ID, item.Current.JobID)
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
