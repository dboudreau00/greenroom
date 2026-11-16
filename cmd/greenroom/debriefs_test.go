package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDebriefCommands(t *testing.T) {
	dir := t.TempDir()
	var out, errb bytes.Buffer
	if code := run([]string{"-C", dir, "init"}, io.Discard, &errb); code != 0 {
		t.Fatalf("init: %d %s", code, errb.String())
	}
	if code := run([]string{"-C", dir, "demo", "log", "acme-demo", "--title", "Larkspur discovery", "--prospect", "Larkspur", "--json"}, &out, &errb); code != 0 {
		t.Fatalf("demo log: %d %s", code, errb.String())
	}
	var d struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(out.Bytes(), &d); err != nil || d.ID == "" {
		t.Fatalf("log json: %v %s", err, out.String())
	}

	out.Reset()
	if code := run([]string{"-C", dir, "debriefs"}, &out, &errb); code != 0 || !strings.Contains(out.String(), "1 waiting") || !strings.Contains(out.String(), d.ID) {
		t.Fatalf("debriefs: %d %s %s", code, out.String(), errb.String())
	}

	out.Reset()
	code := run([]string{"-C", dir, "debrief", d.ID, "--rating", "4", "--highlights", "Billing landed", "--outcome", "positive",
		"--competitor", "Quillmark", "--gap", "feature:high:Scheduled exports: CSV and XLSX", "--gap", "environment:medium:Seed has no EMEA orders"}, &out, &errb)
	if code != 0 || !strings.Contains(out.String(), "rating 4/5") || !strings.Contains(out.String(), "Scheduled exports: CSV and XLSX (feature)") || !strings.Contains(out.String(), "Billing landed") {
		t.Fatalf("debrief: %d %s %s", code, out.String(), errb.String())
	}

	out.Reset()
	if code := run([]string{"-C", dir, "gaps", "--json"}, &out, &errb); code != 0 {
		t.Fatalf("gaps json: %d %s", code, errb.String())
	}
	var rows []struct {
		ID    string `json:"id"`
		Title string `json:"title"`
		Kind  string `json:"kind"`
		Env   string `json:"env"`
		Score int    `json:"score"`
	}
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil || len(rows) != 2 || rows[0].Score != 3 || rows[1].Env != "acme-demo" {
		t.Fatalf("gaps json: %v %s", err, out.String())
	}
	exports, seed := rows[0].ID, rows[1].ID

	// A second run keeps the stored text and adds the linked gap.
	out.Reset()
	if code := run([]string{"-C", dir, "debrief", d.ID, "--rating", "3", "--gap-id", exports + ":blocker:Needs it before renewal"}, &out, &errb); code != 0 ||
		!strings.Contains(out.String(), "rating 3/5") || !strings.Contains(out.String(), "Billing landed") || !strings.Contains(out.String(), "Needs it before renewal") {
		t.Fatalf("debrief again: %d %s %s", code, out.String(), errb.String())
	}
	errb.Reset()
	if code := run([]string{"-C", dir, "debrief", d.ID, "--gap-id", "gap-deadbeef:low"}, io.Discard, &errb); code != 3 || !strings.Contains(errb.String(), "does not exist") {
		t.Fatalf("unknown gap: %d %s", code, errb.String())
	}
	for _, args := range [][]string{{"debrief"}, {"debrief", "../../etc", "--rating", "3"}, {"debrief", d.ID, "--gap", "feature-only"}, {"debrief", d.ID, "--gap-id", "nocolon"}} {
		if code := run(append([]string{"-C", dir}, args...), io.Discard, io.Discard); code != 3 {
			t.Fatalf("%v: %d", args, code)
		}
	}

	out.Reset()
	if code := run([]string{"-C", dir, "gaps"}, &out, &errb); code != 0 || !strings.Contains(out.String(), "SCORE") || !strings.Contains(out.String(), "Scheduled exports") {
		t.Fatalf("gaps: %d %s", code, out.String())
	}
	out.Reset()
	if code := run([]string{"-C", dir, "gaps", "set", exports, "--status", "planned", "--owner", "Data team"}, &out, &errb); code != 0 ||
		!strings.Contains(out.String(), "(feature, planned)") || !strings.Contains(out.String(), "Data team") || !strings.Contains(out.String(), "Larkspur, demo "+d.ID) {
		t.Fatalf("gaps set: %d %s %s", code, out.String(), errb.String())
	}
	out.Reset()
	if code := run([]string{"-C", dir, "gaps", "show", exports}, &out, &errb); code != 0 || !strings.Contains(out.String(), "score      5 from 1 prospect, 1 mention") {
		t.Fatalf("gaps show: %d %s", code, out.String())
	}
	out.Reset()
	if code := run([]string{"-C", dir, "gaps", "export", "--format", "csv"}, &out, &errb); code != 0 || !strings.HasPrefix(out.String(), "rank,id,title,") || strings.Count(out.String(), "\r\n") != 3 {
		t.Fatalf("gaps export csv: %d %q", code, out.String())
	}
	file := filepath.Join(dir, "gaps.md")
	errb.Reset()
	if code := run([]string{"-C", dir, "gaps", "export", "--out", file}, io.Discard, &errb); code != 0 || !strings.Contains(errb.String(), "wrote") {
		t.Fatalf("gaps export md: %d %s", code, errb.String())
	}
	if b, err := os.ReadFile(file); err != nil || !strings.Contains(string(b), "## Demo environment") || !strings.Contains(string(b), "Needs it before renewal") {
		t.Fatalf("export file: %v %s", err, b)
	}
	out.Reset()
	if code := run([]string{"-C", dir, "gaps", "merge", seed, exports}, &out, &errb); code != 0 || !strings.Contains(out.String(), "1 debrief rewritten") {
		t.Fatalf("gaps merge: %d %s %s", code, out.String(), errb.String())
	}
	out.Reset()
	if code := run([]string{"-C", dir, "gaps", "--merged"}, &out, &errb); code != 0 || !strings.Contains(out.String(), "merged into "+exports) {
		t.Fatalf("gaps --merged: %d %s", code, out.String())
	}
	out.Reset()
	if code := run([]string{"-C", dir, "debriefs", "--since", "all"}, &out, &errb); code != 0 || !strings.Contains(out.String(), "0 waiting, 1 submitted") ||
		!strings.Contains(out.String(), "Average rating 3.00") || !strings.Contains(out.String(), "Quillmark 1") || !strings.Contains(out.String(), "Top gaps") {
		t.Fatalf("debriefs after: %d %s", code, out.String())
	}
	for _, args := range [][]string{{"gaps", "show", "nope"}, {"gaps", "bogus"}, {"gaps", "merge", exports}, {"gaps", "export", "--format", "xls"}, {"debriefs", "--since", "soon"}} {
		if code := run(append([]string{"-C", dir}, args...), io.Discard, io.Discard); code != 3 {
			t.Fatalf("%v: %d", args, code)
		}
	}
	out.Reset()
	if code := run([]string{"help"}, &out, io.Discard); code != 0 || !strings.Contains(out.String(), "  debriefs    ") || !strings.Contains(out.String(), "  debrief     ") || !strings.Contains(out.String(), "  gaps        ") {
		t.Fatalf("usage lacks the debrief commands: %s", out.String())
	}
}
