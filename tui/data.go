package tui

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/ngocphat/tokimeki/eventlog"
	"github.com/ngocphat/tokimeki/protocol"
)

type job struct {
	protocol.JobMeta
	effectiveStatus string
	queuePosition   int
	stats           *stats
	logs            []string
}

type worker struct {
	info      protocol.WorkerInfo
	status    string
	heartbeat time.Time
	current   *protocol.JobInfo
	stats     *stats
}

func (w worker) displayStatus() string {
	if w.status == "running" && w.current == nil {
		return "idle"
	}
	return w.status
}

func (w worker) remainingTTL(now time.Time) string {
	if w.status != "running" {
		return "-"
	}
	if remaining, ok := protocol.RemainingTTL(w.info, now); ok {
		return protocol.FormatTTL(remaining)
	}
	return "-"
}

type snapshot struct {
	jobs    []job
	workers []worker
	events  []eventlog.Event
}

type stats struct {
	JobID            string  `json:"job_id"`
	CPUPercent       float64 `json:"cpu_percent"`
	RSSBytes         int64   `json:"rss_bytes"`
	GPUUtil          int     `json:"gpu_util_percent"`
	GPUMemBytes      int64   `json:"gpu_mem_bytes"`
	GPUMemTotalBytes int64   `json:"gpu_mem_total_bytes"`
	NumProcs         int     `json:"num_procs"`
	SampledAt        string  `json:"sampled_at"`
	Note             string  `json:"note"`
}

// loadSnapshot reads persisted state only. Actions use the existing request
// protocol separately; refreshing the dashboard never contacts a runner.
func loadSnapshot(paths *protocol.Paths) (snapshot, error) {
	workers, err := loadWorkers(paths)
	if err != nil {
		return snapshot{}, err
	}
	jobs, err := loadJobs(paths, workers)
	if err != nil {
		return snapshot{}, err
	}
	events, err := loadEvents(paths)
	if err != nil {
		return snapshot{}, err
	}
	return snapshot{jobs: jobs, workers: workers, events: events}, nil
}

func loadJobs(paths *protocol.Paths, workers []worker) ([]job, error) {
	entries, err := os.ReadDir(paths.JobsDir())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read jobs: %w", err)
	}
	alive := make(map[string]bool, len(workers))
	for _, item := range workers {
		alive[item.info.WorkerID] = item.status == "running"
	}
	queuePosition := readQueuePositions(paths)
	jobs := make([]job, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		data, err := os.ReadFile(paths.JobMeta(entry.Name()))
		if err != nil {
			continue // a job may be removed by gc during this scan
		}
		var meta protocol.JobMeta
		if json.Unmarshal(data, &meta) != nil || meta.JobID != entry.Name() {
			continue
		}
		status := meta.Status
		if status == protocol.StatusRunning && !alive[meta.WorkerID] {
			status = "stopped"
		}
		jobs = append(jobs, job{JobMeta: meta, effectiveStatus: status, queuePosition: queuePosition[meta.JobID]})
	}
	return jobs, nil
}

func readQueuePositions(paths *protocol.Paths) map[string]int {
	positions := map[string]int{}
	data, err := os.ReadFile(paths.GlobalQueue())
	if err != nil {
		return positions
	}
	var queue []string
	if json.Unmarshal(data, &queue) != nil {
		return positions
	}
	for i, id := range queue {
		positions[id] = i + 1
	}
	return positions
}

func loadWorkers(paths *protocol.Paths) ([]worker, error) {
	entries, err := os.ReadDir(paths.WorkersDir())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read runners: %w", err)
	}
	workers := make([]worker, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		data, err := os.ReadFile(paths.WorkerInfo(entry.Name()))
		if err != nil {
			continue
		}
		var item worker
		if json.Unmarshal(data, &item.info) != nil || item.info.WorkerID != entry.Name() {
			continue
		}
		if data, err := os.ReadFile(paths.WorkerHeartbeat(entry.Name())); err == nil {
			if nsec, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64); err == nil {
				item.heartbeat = time.Unix(0, nsec)
				if age := time.Since(item.heartbeat); age <= 30*time.Second {
					item.status = "running"
				}
			}
		}
		if item.status != "running" {
			continue
		}
		if data, err := os.ReadFile(paths.WorkerCurrent(entry.Name())); err == nil {
			var current protocol.JobInfo
			if json.Unmarshal(data, &current) == nil && current.JobID != "" {
				item.current = &current
			}
		}
		workers = append(workers, item)
	}
	sort.Slice(workers, func(i, j int) bool {
		return workers[i].info.WorkerID < workers[j].info.WorkerID
	})
	return workers, nil
}

func loadEvents(paths *protocol.Paths) ([]eventlog.Event, error) {
	f, err := os.Open(paths.EventsLog())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read activity: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	const maxBytes int64 = 256 << 10
	start := max(int64(0), info.Size()-maxBytes)
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(f, maxBytes))
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if start > 0 && len(lines) > 0 {
		lines = lines[1:]
	}
	events := make([]eventlog.Event, 0, min(200, len(lines)))
	for i := len(lines) - 1; i >= 0 && len(events) < 200; i-- {
		var item eventlog.Event
		if json.Unmarshal([]byte(lines[i]), &item) == nil && item.Kind != "" {
			events = append(events, item)
		}
	}
	return events, nil
}

func loadStats(paths *protocol.Paths, selected job) *stats {
	if selected.Status != protocol.StatusRunning || selected.WorkerID == "" {
		return nil
	}
	data, err := os.ReadFile(paths.WorkerStats(selected.WorkerID))
	if err != nil {
		return nil
	}
	var sample stats
	if json.Unmarshal(data, &sample) != nil || sample.JobID != selected.JobID {
		return nil
	}
	when, err := time.Parse(time.RFC3339, sample.SampledAt)
	if err != nil || time.Since(when) > 30*time.Second || time.Until(when) > 30*time.Second {
		return nil
	}
	return &sample
}

func loadLogs(paths *protocol.Paths, selected job, limit int) []string {
	stdout := tailLines(paths.JobStdout(selected.JobID), limit)
	stderr := tailLines(paths.JobStderr(selected.JobID), limit)
	if len(stderr) == 0 {
		return stdout
	}
	if len(stdout) == 0 {
		return append([]string{"stderr"}, stderr...)
	}
	return append(append(stdout, "", "stderr"), stderr...)
}

func tailLines(path string, limit int) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil
	}
	const maxBytes int64 = 64 << 10
	start := info.Size() - maxBytes
	if start < 0 {
		start = 0
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(f, maxBytes))
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimRight(string(data), "\r\n"), "\n")
	if start > 0 && len(lines) > 0 {
		lines = lines[1:] // partial first line
	}
	if len(lines) == 1 && lines[0] == "" {
		return nil
	}
	if len(lines) > limit {
		lines = lines[len(lines)-limit:]
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
