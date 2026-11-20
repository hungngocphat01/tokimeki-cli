package main

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/hungngocphat01/tokimeki/notice"
	"github.com/spf13/cobra"
)

// noticeHandler prints engine notices on w: one NDJSON {"notice":{...}} line
// each in --json mode (stdout stays the result), otherwise "warning: ..." and
// timestamped "debug: ..." lines.
func noticeHandler(w io.Writer, asJSON bool) func(notice.Notice) {
	var mu sync.Mutex
	return func(n notice.Notice) {
		mu.Lock()
		defer mu.Unlock()
		if asJSON {
			data, err := json.Marshal(map[string]notice.Notice{"notice": n})
			if err == nil {
				fmt.Fprintf(w, "%s\n", data)
			}
			return
		}
		if n.Level == notice.LevelDebug {
			fmt.Fprintf(w, "%s debug: %s\n", time.Now().Format("15:04:05.000"), n.Message)
			return
		}
		fmt.Fprintf(w, "%s: %s\n", n.Level, n.Message)
	}
}

// installNotices routes engine notices to w for cmd; debug notices only
// with --verbose.
func installNotices(cmd *cobra.Command, w io.Writer, verbose bool) {
	asJSON := false
	if f := cmd.Flags().Lookup("json"); f != nil {
		asJSON = f.Value.String() == "true"
	}
	notice.SetHandler(noticeHandler(w, asJSON), verbose)
}

// holdWarnings collects warnings instead of printing them (debug notices are
// dropped), for full-screen commands; the returned func prints them to w.
func holdWarnings(w io.Writer) (replay func()) {
	var mu sync.Mutex
	var held []notice.Notice
	notice.SetHandler(func(n notice.Notice) {
		if n.Level != notice.LevelWarning {
			return
		}
		mu.Lock()
		held = append(held, n)
		mu.Unlock()
	}, false)
	return func() {
		mu.Lock()
		defer mu.Unlock()
		print := noticeHandler(w, false)
		for _, n := range held {
			print(n)
		}
	}
}
