package seeds

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func hunkText(hs []Hunk) string {
	var sb strings.Builder
	for _, h := range hs {
		sb.WriteString(h.Header + "\n")
		for _, l := range h.Lines {
			sb.WriteString(l + "\n")
		}
	}
	return sb.String()
}

func TestUnifiedHunks(t *testing.T) {
	lines := func(a ...string) []byte { return []byte(strings.Join(a, "\n") + "\n") }
	cases := []struct {
		name     string
		old, new []byte
		want     string
	}{
		{"identical", lines("a", "b"), lines("a", "b"), ""},
		{"both empty", nil, nil, ""},
		{"from empty", nil, lines("a", "b"), "@@ -0,0 +1,2 @@\n+a\n+b\n"},
		{"to empty", lines("a"), nil, "@@ -1 +0,0 @@\n-a\n"},
		{"insert middle", lines("1", "2", "3", "4", "5", "6", "7", "8"), lines("1", "2", "3", "4", "x", "5", "6", "7", "8"),
			"@@ -2,6 +2,7 @@\n 2\n 3\n 4\n+x\n 5\n 6\n 7\n"},
		{"delete first", lines("1", "2", "3", "4", "5"), lines("2", "3", "4", "5"), "@@ -1,4 +1,3 @@\n-1\n 2\n 3\n 4\n"},
		{"replace last", lines("1", "2", "3", "4", "5"), lines("1", "2", "3", "4", "five"), "@@ -2,4 +2,4 @@\n 2\n 3\n 4\n-5\n+five\n"},
		{"insert at end", lines("1"), lines("1", "2"), "@@ -1 +1,2 @@\n 1\n+2\n"},
		{"crlf vs lf", []byte("a\r\nb\r\n"), []byte("a\nb\n"), ""},
		{"crlf with change", []byte("a\r\nb\r\nc\r\n"), []byte("a\nB\nc\n"), "@@ -1,3 +1,3 @@\n a\n-b\n+B\n c\n"},
	}
	for _, c := range cases {
		hs, trunc := UnifiedHunks(c.old, c.new)
		if got := hunkText(hs); got != c.want || trunc {
			t.Errorf("%s: truncated=%v\n got:\n%s\nwant:\n%s", c.name, trunc, got, c.want)
		}
	}
}

func TestUnifiedHunksSeparateAndMerged(t *testing.T) {
	var a, b []string
	for i := 1; i <= 30; i++ {
		a = append(a, fmt.Sprint(i))
		b = append(b, fmt.Sprint(i))
	}
	b[2] = "three" // line 3
	b[24] = "25x"  // line 25: far away, own hunk
	b[8] = "nine"  // line 9: 6 lines from line 3, merged with it
	hs, _ := UnifiedHunks([]byte(strings.Join(a, "\n")), []byte(strings.Join(b, "\n")))
	if len(hs) != 2 {
		t.Fatalf("want 2 hunks, got %d:\n%s", len(hs), hunkText(hs))
	}
	if hs[0].Header != "@@ -1,12 +1,12 @@" || hs[1].Header != "@@ -22,7 +22,7 @@" {
		t.Fatalf("headers %q %q", hs[0].Header, hs[1].Header)
	}
}

// apply replays hunks on old and checks the result equals new, which proves
// the edit script is right for random inputs.
func apply(t *testing.T, old []string, hs []Hunk) []string {
	t.Helper()
	var out []string
	pos := 0
	for _, h := range hs {
		start := h.OldStart - 1
		if h.OldLines == 0 {
			start = h.OldStart
		}
		out = append(out, old[pos:start]...)
		pos = start
		for _, l := range h.Lines {
			switch l[0] {
			case ' ':
				if old[pos] != l[1:] {
					t.Fatalf("context mismatch at %d: %q vs %q", pos, old[pos], l[1:])
				}
				out = append(out, l[1:])
				pos++
			case '-':
				if old[pos] != l[1:] {
					t.Fatalf("delete mismatch at %d", pos)
				}
				pos++
			case '+':
				out = append(out, l[1:])
			}
		}
	}
	return append(out, old[pos:]...)
}

func TestUnifiedHunksRoundTrip(t *testing.T) {
	seed := uint32(7)
	rnd := func(n int) int { seed = seed*1664525 + 1013904223; return int(seed>>16) % n }
	for iter := 0; iter < 200; iter++ {
		var a, b []string
		for i := rnd(40); i > 0; i-- {
			a = append(a, string(rune('a'+rnd(5))))
		}
		for i := rnd(40); i > 0; i-- {
			b = append(b, string(rune('a'+rnd(5))))
		}
		hs, trunc := UnifiedHunks([]byte(strings.Join(a, "\n")), []byte(strings.Join(b, "\n")))
		if trunc {
			t.Fatal("unexpected truncation")
		}
		got := apply(t, a, hs)
		if !reflect.DeepEqual(append([]string{}, got...), append([]string{}, b...)) {
			t.Fatalf("iter %d: replay mismatch\n a=%v\n b=%v\n got=%v\n%s", iter, a, b, got, hunkText(hs))
		}
	}
}

func TestUnifiedHunksTruncation(t *testing.T) {
	var a, b strings.Builder
	for i := 0; i < 3000; i++ {
		fmt.Fprintf(&a, "old %d\n", i)
		fmt.Fprintf(&b, "new %d\n", i)
	}
	hs, trunc := UnifiedHunks([]byte(a.String()), []byte(b.String()))
	if !trunc {
		t.Fatal("expected truncation by output lines")
	}
	n := 0
	for _, h := range hs {
		n += len(h.Lines)
	}
	if n != MaxDiffLines {
		t.Fatalf("emitted %d lines, want %d", n, MaxDiffLines)
	}
	big := []byte(strings.Repeat("x\n", MaxDiffInput))
	if _, trunc := UnifiedHunks(big, append([]byte("y\n"), big...)); !trunc {
		t.Fatal("expected truncation by input size")
	}
}

func TestIsBinary(t *testing.T) {
	if IsBinary([]byte("select 1;\n")) || IsBinary([]byte("caf\xc3\xa9")) || IsBinary(nil) {
		t.Fatal("text reported as binary")
	}
	if !IsBinary([]byte("PNG\x00\x01")) || !IsBinary([]byte("caf\xe9")) {
		t.Fatal("binary reported as text")
	}
}

func TestDiffSets(t *testing.T) {
	env := t.TempDir()
	writeSet(t, env, "cfo", "v1", `{"title":"Close","steps":[
	  {"name":"schema","kind":"sql","dependency":"db","file":"a.sql"},
	  {"name":"data","kind":"sql","dependency":"db","sql":"insert 1"},
	  {"name":"gone","kind":"command","command":["true"]}
	],"verify":[{"name":"orders","kind":"sql","dependency":"db","query":"select 1","expect":12}]}`,
		map[string]string{"a.sql": "create table t (a int);\nselect 1;\n", "logo.png": "\x89PNG\x00\x01", "old.txt": "bye\n", "same.sql": "select 2;\n"})
	writeSet(t, env, "cfo", "v2", `{"title":"Quarter close","steps":[
	  {"name":"schema","kind":"sql","dependency":"db","file":"a.sql"},
	  {"name":"data","kind":"sql","dependency":"warehouse","sql":"insert 2","timeout":"30s"},
	  {"name":"views","kind":"sql","dependency":"db","sql":"create view v"}
	],"verify":[{"name":"orders","kind":"sql","dependency":"db","query":"select 1","expect":14},{"name":"rows","kind":"sql","dependency":"db","query":"select 1","min":1}]}`,
		map[string]string{"a.sql": "create table t (a int, b int);\nselect 1;\n", "logo.png": "\x89PNG\x00\x02", "new.txt": "hi\n", "same.sql": "select 2;\n"})
	writeSet(t, env, "ops", "v1", `{"steps":[{"name":"c","kind":"command","command":["true"]}]}`, nil)
	sets, err := Discover(env)
	if err != nil {
		t.Fatal(err)
	}
	v1, _ := Find(sets, "cfo", 1)
	v2, _ := Find(sets, "cfo", 2)
	d, err := Diff(v1, v2)
	if err != nil {
		t.Fatal(err)
	}
	if d.Identical || d.Stat != (DiffStat{Added: 1, Removed: 1, Changed: 3, Unchanged: 1}) {
		t.Fatalf("stat %+v", d.Stat)
	}
	byPath := map[string]FileChange{}
	for _, f := range d.Files {
		byPath[f.Path] = f
	}
	if f := byPath["logo.png"]; f.Status != FileChanged || !f.Binary || len(f.Hunks) != 0 || f.OldHash == f.NewHash {
		t.Fatalf("binary: %+v", f)
	}
	if f := byPath["a.sql"]; f.Binary || len(f.Hunks) != 1 || !strings.Contains(f.Unified(), "-create table t (a int);\n+create table t (a int, b int);\n") {
		t.Fatalf("a.sql: %s", f.Unified())
	}
	if byPath["new.txt"].Status != FileAdded || byPath["old.txt"].Status != FileRemoved || byPath["same.sql"].Status != FileUnchanged {
		t.Fatalf("statuses: %+v", d.Files)
	}
	m := d.Manifest
	if m.Title == nil || m.Title.From != "Close" || m.Title.To != "Quarter close" || m.Description != nil {
		t.Fatalf("title: %+v", m)
	}
	if !reflect.DeepEqual(m.Steps.Added, []string{"views"}) || !reflect.DeepEqual(m.Steps.Removed, []string{"gone"}) {
		t.Fatalf("steps: %+v", m.Steps)
	}
	if len(m.Steps.Changed) != 1 || m.Steps.Changed[0].Name != "data" || strings.Join(m.Steps.Changed[0].Fields, ",") != "dependency,sql,timeout" {
		t.Fatalf("changed steps: %+v", m.Steps.Changed)
	}
	if !reflect.DeepEqual(m.Verify.Added, []string{"rows"}) || len(m.Verify.Changed) != 1 || m.Verify.Changed[0].Fields[0] != "expect" {
		t.Fatalf("verify: %+v", m.Verify)
	}
	same, _ := Diff(v1, v1)
	if !same.Identical || !same.Manifest.Empty() {
		t.Fatalf("self diff: %+v", same)
	}
	ops, _ := Find(sets, "ops", 1)
	if _, err := Diff(v2, ops); err != nil {
		t.Fatalf("cross persona: %v", err)
	}
	other := t.TempDir()
	writeSet(t, other, "cfo", "v1", `{"steps":[{"name":"c","kind":"command","command":["true"]}]}`, nil)
	foreign, _ := Discover(other)
	if _, err := Diff(v1, foreign[0]); err == nil || !strings.Contains(err.Error(), "different environments") {
		t.Fatalf("cross environment: %v", err)
	}
}

func TestDiffLineEndingsOnly(t *testing.T) {
	env := t.TempDir()
	m := `{"steps":[{"name":"c","kind":"command","command":["true"]}]}`
	writeSet(t, env, "cfo", "v1", m, map[string]string{"a.sql": "a\r\nb\r\n"})
	writeSet(t, env, "cfo", "v2", m, map[string]string{"a.sql": "a\nb\n"})
	sets, _ := Discover(env)
	d, err := Diff(sets[0], sets[1])
	if err != nil {
		t.Fatal(err)
	}
	if d.Identical || d.Files[0].Status != FileChanged || d.Files[0].Note != "line endings only" || len(d.Files[0].Hunks) != 0 {
		t.Fatalf("%+v", d.Files[0])
	}
	if !d.Manifest.Empty() {
		t.Fatal("manifest should be unchanged")
	}
	os.WriteFile(filepath.Join(env, "seeds", "cfo", "v2", "a.sql"), []byte("a\nb"), 0o644)
	sets, _ = Discover(env)
	d, _ = Diff(sets[0], sets[1])
	if d.Files[0].Note != "trailing newline only" {
		t.Fatalf("%+v", d.Files[0])
	}
}
