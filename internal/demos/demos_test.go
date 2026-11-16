package demos

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestCleaning(t *testing.T) {
	if got := CleanLine("  Larkspur\t\n Freight\x00\u202e  ", 120); got != "Larkspur Freight" {
		t.Fatalf("CleanLine: %q", got)
	}
	if got := CleanLine(strings.Repeat("ab", 100), 7); got != "abababa" {
		t.Fatalf("cap: %q", got)
	}
	if got := CleanText(" one\r\ntwo\tthree\x1b\n\n"); got != "one\ntwo three" {
		t.Fatalf("CleanText: %q", got)
	}
}

func TestFoldKeepsRunesWhole(t *testing.T) {
	line := "SUMMARY:" + strings.Repeat("\u00e9", 60)
	out := fold(line)
	if !strings.HasSuffix(out, "\r\n") {
		t.Fatal("no CRLF")
	}
	parts := strings.Split(strings.TrimSuffix(out, "\r\n"), "\r\n")
	if len(parts) < 2 {
		t.Fatalf("not folded: %q", out)
	}
	for i, p := range parts {
		if len(p) > 75 || !utf8.ValidString(strings.TrimPrefix(p, " ")) || (i > 0 && !strings.HasPrefix(p, " ")) {
			t.Fatalf("bad fold %d: %q", i, p)
		}
	}
	if strings.ReplaceAll(strings.TrimSuffix(out, "\r\n"), "\r\n ", "") != line {
		t.Fatal("unfold does not restore the line")
	}
	if EscapeText("a\\b;c,d\ne\r") != `a\\b\;c\,d\ne` {
		t.Fatalf("escape: %q", EscapeText("a\\b;c,d\ne\r"))
	}
}

func TestOnStageWindow(t *testing.T) {
	start := time.Date(2026, 11, 20, 15, 0, 0, 0, time.UTC)
	d := &Demo{Booked: true, Status: Live, Start: start, End: start.Add(45 * time.Minute)}
	for _, c := range []struct {
		at   time.Time
		want bool
	}{{start.Add(-time.Minute), false}, {start, true}, {start.Add(44 * time.Minute), true}, {start.Add(45 * time.Minute), false}} {
		if d.Protects(c.at) != c.want {
			t.Fatalf("at %s: want %v", c.at, c.want)
		}
	}
	d.Status = Scheduled
	if d.Protects(start.Add(time.Minute)) {
		t.Fatal("an unprepared demo protects nothing")
	}
	d.Status, d.Booked = Live, false
	if d.Protects(start.Add(time.Minute)) {
		t.Fatal("a logged demo protects nothing")
	}
}
