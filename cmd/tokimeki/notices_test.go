package main

import (
	"bytes"
	"regexp"
	"testing"

	"github.com/hungngocphat01/tokimeki/notice"
)

func TestNoticeHumanFormat(t *testing.T) {
	var buf bytes.Buffer
	h := noticeHandler(&buf, false)
	h(notice.Notice{Level: notice.LevelWarning, Code: notice.CodeLegacySchema, Message: "3 jobs use meta.json schema 1", Count: 3})
	h(notice.Notice{Level: notice.LevelDebug, Message: "dequeue a1"})
	want := regexp.MustCompile(`^warning: 3 jobs use meta.json schema 1\n\d\d:\d\d:\d\d\.\d{3} debug: dequeue a1\n$`)
	if !want.MatchString(buf.String()) {
		t.Fatalf("output %q", buf.String())
	}
}

func TestNoticeJSONFormat(t *testing.T) {
	var buf bytes.Buffer
	h := noticeHandler(&buf, true)
	h(notice.Notice{Level: notice.LevelWarning, Code: notice.CodeLegacySchema, Message: "m", Count: 3})
	if got, want := buf.String(), `{"notice":{"level":"warning","code":"legacy_schema","message":"m","count":3}}`+"\n"; got != want {
		t.Fatalf("output %q, want %q", got, want)
	}
}
