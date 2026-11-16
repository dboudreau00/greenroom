package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSeedsDiffAndBump(t *testing.T) {
	dir := t.TempDir()
	if code := run([]string{"-C", dir, "init"}, io.Discard, io.Discard); code != 0 {
		t.Fatalf("init: %d", code)
	}
	var out, errb bytes.Buffer
	// Plain listing still works.
	if code := run([]string{"-C", dir, "seeds", "acme-demo"}, &out, &errb); code != 0 || !strings.Contains(out.String(), "cfo@v2") {
		t.Fatalf("seeds: %d %s %s", code, out.String(), errb.String())
	}
	out.Reset()
	if code := run([]string{"-C", dir, "seeds", "diff", "acme-demo", "cfo@v2", "cfo@v2"}, &out, &errb); code != 0 || !strings.Contains(out.String(), "identical") {
		t.Fatalf("identical diff: %d %s %s", code, out.String(), errb.String())
	}
	out.Reset()
	if code := run([]string{"-C", dir, "seeds", "diff", "acme-demo", "cfo@v1", "cfo@v2"}, &out, &errb); code != 1 || !strings.Contains(out.String(), "--- a/seed.json") || !strings.Contains(out.String(), "@@ ") {
		t.Fatalf("diff: %d %s %s", code, out.String(), errb.String())
	}
	out.Reset()
	if code := run([]string{"-C", dir, "seeds", "diff", "acme-demo", "cfo@v1", "retail-ops@v1", "--stat"}, &out, &errb); code != 1 || strings.Contains(out.String(), "@@ ") || !strings.Contains(out.String(), "files:") {
		t.Fatalf("stat: %d %s", code, out.String())
	}
	out.Reset()
	if code := run([]string{"-C", dir, "seeds", "diff", "acme-demo", "cfo@v1", "cfo@v2", "--json"}, &out, &errb); code != 1 || !strings.Contains(out.String(), `"hunks"`) {
		t.Fatalf("json: %d %s", code, out.String())
	}
	errb.Reset()
	if code := run([]string{"-C", dir, "seeds", "diff", "acme-demo", "cfo@v1", "cfo@v9"}, io.Discard, &errb); code != 3 || !strings.Contains(errb.String(), "not found") {
		t.Fatalf("missing set: %d %s", code, errb.String())
	}

	envFile := filepath.Join(dir, "environments", "acme-demo", "environment.json")
	before, _ := os.ReadFile(envFile)
	out.Reset()
	code := run([]string{"-C", dir, "seeds", "bump", "acme-demo", "cfo", "--title", "Tidewell quarter close", "--pin"}, &out, &errb)
	if code != 0 || !strings.Contains(out.String(), "created cfo@v3 from cfo@v2") || !strings.Contains(out.String(), "pinned acme-demo to cfo@v3") {
		t.Fatalf("bump: %d %s %s", code, out.String(), errb.String())
	}
	after, _ := os.ReadFile(envFile)
	if string(after) != strings.Replace(string(before), `"pin": { "persona": "cfo", "version": 2 }`, `"pin": { "persona": "cfo", "version": 3 }`, 1) {
		t.Fatalf("environment.json rewritten beyond the pin:\n%s", after)
	}
	if !strings.Contains(string(after), "// Acme demo:") {
		t.Fatal("comments lost")
	}
	out.Reset()
	if code := run([]string{"-C", dir, "validate"}, &out, &errb); code != 0 || !strings.Contains(out.String(), "4 seed sets, ok") {
		t.Fatalf("validate after bump: %d %s", code, out.String())
	}
	out.Reset()
	if code := run([]string{"-C", dir, "seeds", "acme-demo"}, &out, &errb); code != 0 || !strings.Contains(out.String(), "cfo@v3") || !strings.Contains(out.String(), "Tidewell quarter close") {
		t.Fatalf("seeds after bump: %d %s", code, out.String())
	}
	out.Reset()
	if code := run([]string{"-C", dir, "seeds", "bump", "acme-demo", "cfo", "--from", "v1"}, &out, &errb); code != 0 || !strings.Contains(out.String(), "created cfo@v4 from cfo@v1") || strings.Contains(out.String(), "pinned") {
		t.Fatalf("bump from v1: %d %s %s", code, out.String(), errb.String())
	}
	errb.Reset()
	if code := run([]string{"-C", dir, "seeds", "bump", "acme-demo", "cfo", "--from", "x"}, io.Discard, &errb); code != 3 {
		t.Fatalf("bad from: %d", code)
	}
}
