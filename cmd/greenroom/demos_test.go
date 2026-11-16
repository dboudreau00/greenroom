package main

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"
)

func TestDemoCommands(t *testing.T) {
	dir := t.TempDir()
	var out, errb bytes.Buffer
	if code := run([]string{"-C", dir, "init"}, io.Discard, &errb); code != 0 {
		t.Fatalf("init: %d %s", code, errb.String())
	}
	at := time.Now().Add(48 * time.Hour).Truncate(time.Minute)
	code := run([]string{"-C", dir, "book", "acme-demo", "--at", at.Format("2006-01-02 15:04"), "--for", "45m", "--title", "Larkspur intro",
		"--prospect", "Larkspur", "--persona", "cfo@v1", "--attendee", "Ana Ruiz", "--attendee", "Ben Okafor", "--reset-after"}, &out, &errb)
	if code != 0 || !strings.Contains(out.String(), "booked ") || !strings.Contains(out.String(), "greenroom serve") || !strings.Contains(out.String(), "cfo@v1") {
		t.Fatalf("book: %d %s %s", code, out.String(), errb.String())
	}
	errb.Reset()
	if code := run([]string{"-C", dir, "book", "acme-demo", "--at", at.Add(15 * time.Minute).Format(time.RFC3339), "--title", "Clash"}, io.Discard, &errb); code != 3 || !strings.Contains(errb.String(), "booked from") {
		t.Fatalf("overlapping book: %d %s", code, errb.String())
	}
	errb.Reset()
	if code := run([]string{"-C", dir, "book", "acme-demo", "--at", "2020-01-01 10:00", "--title", "Old"}, io.Discard, &errb); code != 3 || !strings.Contains(errb.String(), "future") {
		t.Fatalf("past book: %d %s", code, errb.String())
	}
	if code := run([]string{"-C", dir, "book", "acme-demo", "--at", "tomorrow", "--title", "Vague"}, io.Discard, io.Discard); code != 3 {
		t.Fatalf("bad time: %d", code)
	}

	out.Reset()
	if code := run([]string{"-C", dir, "demos", "--json"}, &out, &errb); code != 0 {
		t.Fatalf("demos json: %d %s", code, errb.String())
	}
	var list []struct {
		ID      string `json:"id"`
		Title   string `json:"title"`
		Persona string `json:"persona"`
		Version int    `json:"version"`
	}
	if err := json.Unmarshal(out.Bytes(), &list); err != nil || len(list) != 1 || list[0].Title != "Larkspur intro" || list[0].Version != 1 {
		t.Fatalf("demos json: %v %s", err, out.String())
	}
	id := list[0].ID
	out.Reset()
	if code := run([]string{"-C", dir, "demos"}, &out, &errb); code != 0 || !strings.Contains(out.String(), id) || !strings.Contains(out.String(), "scheduled") {
		t.Fatalf("demos: %d %s", code, out.String())
	}

	out.Reset()
	if code := run([]string{"-C", dir, "demo", "note", id, "--kind", "issue", "Login", "was", "slow"}, &out, &errb); code != 0 || !strings.Contains(out.String(), "Login was slow") || !strings.Contains(out.String(), "issue") {
		t.Fatalf("demo note: %d %s %s", code, out.String(), errb.String())
	}
	out.Reset()
	if code := run([]string{"-C", dir, "demo", "set", id, "--stage", "poc", "--next-step", "Send the pricing sheet"}, &out, &errb); code != 0 || !strings.Contains(out.String(), "poc") || !strings.Contains(out.String(), "Send the pricing sheet") {
		t.Fatalf("demo set: %d %s %s", code, out.String(), errb.String())
	}
	out.Reset()
	if code := run([]string{"-C", dir, "demo", "show", id}, &out, &errb); code != 0 || !strings.Contains(out.String(), "Ana Ruiz, Ben Okafor") || !strings.Contains(out.String(), "booked by cli") {
		t.Fatalf("demo show: %d %s", code, out.String())
	}
	out.Reset()
	if code := run([]string{"-C", dir, "demo", "cancel", id}, &out, &errb); code != 0 || !strings.Contains(out.String(), "cancelled") {
		t.Fatalf("demo cancel: %d %s %s", code, out.String(), errb.String())
	}
	errb.Reset()
	if code := run([]string{"-C", dir, "demo", "cancel", id}, io.Discard, &errb); code != 3 || !strings.Contains(errb.String(), "already cancelled") {
		t.Fatalf("cancel twice: %d %s", code, errb.String())
	}
	out.Reset()
	if code := run([]string{"-C", dir, "demos", "--past"}, &out, &errb); code != 0 || !strings.Contains(out.String(), id) {
		t.Fatalf("demos past: %d %s", code, out.String())
	}

	out.Reset()
	code = run([]string{"-C", dir, "demo", "log", "acme-demo", "--title", "Booth demo", "--outcome", "positive", "--prospect", "Tidewell", "--note", "Asked about SSO"}, &out, &errb)
	if code != 0 || !strings.Contains(out.String(), "(done, logged)") || !strings.Contains(out.String(), "Asked about SSO") {
		t.Fatalf("demo log: %d %s %s", code, out.String(), errb.String())
	}
	for _, args := range [][]string{{"demo", "show", "../../etc"}, {"demo", "bogus", "x"}, {"demo"}, {"demo", "note", id}} {
		if code := run(append([]string{"-C", dir}, args...), io.Discard, io.Discard); code != 3 {
			t.Fatalf("%v: %d", args, code)
		}
	}
	out.Reset()
	if code := run([]string{"help"}, &out, io.Discard); code != 0 || !strings.Contains(out.String(), "  book        ") || !strings.Contains(out.String(), "  demos       ") {
		t.Fatalf("usage lacks the demo commands: %s", out.String())
	}
}
