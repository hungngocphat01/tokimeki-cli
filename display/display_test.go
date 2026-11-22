package display

import (
	"testing"
	"time"
)

func TestDuration_UsesDaysForLongDurations(t *testing.T) {
	tests := []struct {
		name string
		in   time.Duration
		want string
	}{
		{name: "under a minute", in: 59 * time.Second, want: "0m"},
		{name: "subday", in: 23*time.Hour + 59*time.Minute, want: "23h59m"},
		{name: "one day", in: 24 * time.Hour, want: "1d"},
		{name: "day and hour", in: 25 * time.Hour, want: "1d1h"},
		{name: "multiple days", in: 49*time.Hour + 2*time.Minute + 30*time.Second, want: "2d1h2m"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Duration(tt.in.Seconds()); got != tt.want {
				t.Fatalf("Duration(%s) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestGroupColor(t *testing.T) {
	cases := map[string]string{"R": Cyan, "Q": Yellow, "C": Green, "F": Red, "D": Magenta, "S": LightGray, "X": ""}
	for grp, want := range cases {
		if got := GroupColor(grp); got != want {
			t.Errorf("GroupColor(%s) = %q, want %q", grp, got, want)
		}
	}
	if StatusGroup("stopped") != "S" || StatusGroup("crashed") != "D" || StatusGroup("weird") != "W" {
		t.Errorf("StatusGroup mapping wrong")
	}
}

func TestSmartCommand(t *testing.T) {
	cases := map[string]string{
		"/a/b/c/run.sh":           "run.sh",
		"bash /a/b/c/train.sh":    "bash train.sh",
		"python /a/b/eval.py -e5": "python eval.py -e5",
		"python -u /a/b/eval.py":  "python -u eval.py",
		"myapp /etc/config.toml":  "myapp /etc/config.toml",
		"echo hi":                 "echo hi",
		"":                        "",
	}
	for in, want := range cases {
		if got := SmartCommand(in); got != want {
			t.Errorf("SmartCommand(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTTL(t *testing.T) {
	cases := map[time.Duration]string{
		50 * time.Hour:                "2d 2h",
		46*time.Hour + 30*time.Minute: "1d 23h",
		48 * time.Hour:                "2d",
		90 * time.Minute:              "2h",
		0:                             "0h",
		-time.Hour:                    "0h",
	}
	for in, want := range cases {
		if got := TTL(in); got != want {
			t.Errorf("TTL(%s) = %q, want %q", in, got, want)
		}
	}
}

func TestParseSize(t *testing.T) {
	cases := map[string]int64{"10G": 10 << 30, "500m": 500 << 20, "1234": 1234, "2K": 2048, "": 0}
	for in, want := range cases {
		got, err := ParseSize(in)
		if err != nil || got != want {
			t.Errorf("ParseSize(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	if _, err := ParseSize("x"); err == nil {
		t.Error("ParseSize(x) succeeded")
	}
}

func TestSizeAndBytes(t *testing.T) {
	if Size(1536) != "1.5K" || Size(10) != "10B" || Size(3<<30) != "3.0G" {
		t.Errorf("Size: %q %q %q", Size(1536), Size(10), Size(3<<30))
	}
	if Bytes(0) != "-" || Bytes(1536) != "2K" || Bytes(5<<20) != "5M" {
		t.Errorf("Bytes: %q %q %q", Bytes(0), Bytes(1536), Bytes(5<<20))
	}
}

func TestRFC3339RoundTrip(t *testing.T) {
	ts := time.Date(2026, 1, 5, 11, 23, 45, 123456789, time.UTC)
	if got := RFC3339(ts.UnixNano()); got != "2026-01-05T11:23:45Z" {
		t.Errorf("RFC3339 = %q", got)
	}
	if got := RFC3339Nano(ts.UnixNano()); got != "2026-01-05T11:23:45.123456789Z" {
		t.Errorf("RFC3339Nano = %q", got)
	}
	if RFC3339(0) != "" || RFC3339Nano(0) != "" {
		t.Error("zero timestamp not empty")
	}
}

func TestLocalTimeUsesTZ(t *testing.T) {
	prev := time.Local
	defer func() { time.Local = prev }()
	time.Local = time.FixedZone("JST", 9*3600)
	ts := time.Date(2026, 5, 10, 14, 34, 35, 0, time.UTC)
	if got := LocalTime(ts.UnixNano()); got != "2026-05-10 23:34:35" {
		t.Errorf("LocalTime = %q", got)
	}
	if LocalTime(0) != "-" {
		t.Error("LocalTime(0) != -")
	}
}

func TestAges(t *testing.T) {
	if Age(500*time.Millisecond) != "<1s" || Age(90*time.Second) != "1m30s" || Age(3*time.Hour) != "3h" {
		t.Errorf("Age: %q %q %q", Age(500*time.Millisecond), Age(90*time.Second), Age(3*time.Hour))
	}
	if StatsAge(-time.Second) != "0s" || StatsAge(90*time.Second) != "1m" {
		t.Errorf("StatsAge: %q %q", StatsAge(-time.Second), StatsAge(90*time.Second))
	}
}

func TestVisibleLenAndTruncate(t *testing.T) {
	if VisibleLen(Red+"abc"+Reset) != 3 {
		t.Error("VisibleLen counts escapes")
	}
	if Truncate("abcdefgh", 6) != "abc..." || Truncate("abc", 6) != "abc" {
		t.Error("Truncate wrong")
	}
}

func TestStatusDisplay(t *testing.T) {
	t.Setenv("LC_ALL", "C")
	t.Setenv("LC_CTYPE", "")
	t.Setenv("LANG", "")
	if StatusDisplay("running", false) != "running" || StatusDisplay("queued", true) != "Q" {
		t.Errorf("ASCII StatusDisplay wrong")
	}
	t.Setenv("LC_ALL", "en_US.UTF-8")
	if StatusDisplay("running", false) != "▶ running" || StatusDisplay("stopped", true) != "○" {
		t.Errorf("Unicode StatusDisplay wrong")
	}
}

func TestRunnerSlots(t *testing.T) {
	for _, tc := range []struct {
		busy, threads int
		want          string
	}{{0, 1, "0/1"}, {2, 3, "2/3"}, {3, 3, "3/3"}} {
		if got := RunnerSlots(tc.busy, tc.threads); got != tc.want {
			t.Errorf("RunnerSlots(%d, %d) = %q, want %q", tc.busy, tc.threads, got, tc.want)
		}
	}
}

func TestGoDuration(t *testing.T) {
	for secs, want := range map[float64]string{0: "0s", 45: "45s", 1800: "30m", 3600: "1h", 5400: "1h30m", 3661: "1h1m1s", 7200 + 30: "2h0m30s"} {
		if got := GoDuration(secs); got != want {
			t.Errorf("GoDuration(%v) = %q, want %q", secs, got, want)
		}
	}
}
