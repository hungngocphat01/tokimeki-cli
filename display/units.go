package display

import (
	"fmt"
	"time"
)

// LocalTime formats Unix ns in time.Local (which honors TZ) as
// "2006-01-02 15:04:05"; 0 is "-".
func LocalTime(ns int64) string {
	if ns == 0 {
		return "-"
	}
	return time.Unix(0, ns).Local().Format("2006-01-02 15:04:05")
}

// RFC3339 formats Unix ns as UTC RFC3339; 0 is "".
func RFC3339(ns int64) string {
	if ns == 0 {
		return ""
	}
	return time.Unix(0, ns).UTC().Format(time.RFC3339)
}

// RFC3339Nano formats Unix ns as UTC RFC3339 with fractional seconds; 0 is "".
func RFC3339Nano(ns int64) string {
	if ns == 0 {
		return ""
	}
	return time.Unix(0, ns).UTC().Format(time.RFC3339Nano)
}

// Duration renders seconds ps-style, truncated to the minute: "1d2h3m",
// "0m" under a minute.
func Duration(secs float64) string {
	d := time.Duration(secs * float64(time.Second)).Truncate(time.Minute)
	days := d / (24 * time.Hour)
	d -= days * 24 * time.Hour
	hours := d / time.Hour
	d -= hours * time.Hour
	minutes := d / time.Minute

	var out string
	if days > 0 {
		out += fmt.Sprintf("%dd", days)
	}
	if hours > 0 {
		out += fmt.Sprintf("%dh", hours)
	}
	if minutes > 0 {
		out += fmt.Sprintf("%dm", minutes)
	}
	if out == "" {
		return "0m"
	}
	return out
}

// Age renders a heartbeat/event age for top: "<1s", "42s", "3m5s", "2h".
func Age(d time.Duration) string {
	if d < time.Second {
		return "<1s"
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh", int(d.Hours()))
}

// StatsAge renders a stats sample age: "0s" for the future, "<1s", "42s",
// "3m", "2h".
func StatsAge(d time.Duration) string {
	if d < 0 {
		return "0s"
	}
	if d < time.Second {
		return "<1s"
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh", int(d.Hours()))
}

// TTL renders a duration in whole days and hours, rounding up: "2d 2h",
// "2d", "5h"; non-positive is "0h".
func TTL(d time.Duration) string {
	if d <= 0 {
		return "0h"
	}
	hours := int64(d / time.Hour)
	if d%time.Hour != 0 {
		hours++
	}
	days := hours / 24
	hours %= 24
	if days == 0 {
		return fmt.Sprintf("%dh", hours)
	}
	if hours == 0 {
		return fmt.Sprintf("%dd", days)
	}
	return fmt.Sprintf("%dd %dh", days, hours)
}

// Bytes renders a memory size for stats: "1.5G", "512M", "64K", "12B"; 0 is
// "-".
func Bytes(b int64) string {
	const (
		KB = 1024
		MB = 1024 * KB
		GB = 1024 * MB
	)
	switch {
	case b >= GB:
		return fmt.Sprintf("%.1fG", float64(b)/GB)
	case b >= MB:
		return fmt.Sprintf("%.0fM", float64(b)/MB)
	case b >= KB:
		return fmt.Sprintf("%.0fK", float64(b)/KB)
	case b > 0:
		return fmt.Sprintf("%dB", b)
	default:
		return "-"
	}
}

// Size renders a disk size for gc: "1.5K", "2.0M", "3.1G", "12B".
func Size(n int64) string {
	const (
		KB = 1 << 10
		MB = 1 << 20
		GB = 1 << 30
	)
	switch {
	case n >= GB:
		return fmt.Sprintf("%.1fG", float64(n)/GB)
	case n >= MB:
		return fmt.Sprintf("%.1fM", float64(n)/MB)
	case n >= KB:
		return fmt.Sprintf("%.1fK", float64(n)/KB)
	default:
		return fmt.Sprintf("%dB", n)
	}
}

// ParseSize accepts "10G", "500M", "1024K", "12B" or plain bytes ("1234");
// suffixes are case-insensitive. Empty is 0.
func ParseSize(s string) (int64, error) {
	if s == "" {
		return 0, nil
	}
	mult := int64(1)
	switch s[len(s)-1] {
	case 'G', 'g':
		mult = 1 << 30
		s = s[:len(s)-1]
	case 'M', 'm':
		mult = 1 << 20
		s = s[:len(s)-1]
	case 'K', 'k':
		mult = 1 << 10
		s = s[:len(s)-1]
	case 'B', 'b':
		s = s[:len(s)-1]
	}
	var v int64
	if _, err := fmt.Sscanf(s, "%d", &v); err != nil {
		return 0, fmt.Errorf("invalid size %q: %w", s, err)
	}
	return v * mult, nil
}
