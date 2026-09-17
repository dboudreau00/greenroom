package seeds

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSet(t *testing.T, envDir, persona string, version string, manifest string, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(envDir, "seeds", persona, version)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if manifest != "" {
		if err := os.WriteFile(filepath.Join(dir, "seed.json"), []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestDiscoverAndHash(t *testing.T) {
	env := t.TempDir()
	writeSet(t, env, "cfo", "v1", `{"title":"one","steps":[{"name":"s","kind":"sql","dependency":"db","file":"a.sql"}]}`, map[string]string{"a.sql": "select 1;"})
	writeSet(t, env, "cfo", "v2", `{"steps":[{"name":"s","kind":"sql","dependency":"db","sql":"select 2"}],"verify":[{"name":"n","kind":"sql","dependency":"db","query":"select 1","expect":1}]}`, nil)
	writeSet(t, env, "ops", "v3", `{"persona":"ops","version":3,"steps":[{"name":"c","kind":"command","command":["true"]}]}`, nil)
	writeSet(t, env, "ops", "notaversion", ``, nil)
	sets, err := Discover(env)
	if err != nil {
		t.Fatal(err)
	}
	if len(sets) != 3 {
		t.Fatalf("want 3 sets, got %d", len(sets))
	}
	if sets[0].ID() != "cfo@v1" || sets[1].ID() != "cfo@v2" || sets[2].ID() != "ops@v3" {
		t.Fatalf("order: %s %s %s", sets[0].ID(), sets[1].ID(), sets[2].ID())
	}
	for _, s := range sets {
		if len(s.Problems) > 0 {
			t.Fatalf("%s problems: %v", s.ID(), s.Problems)
		}
	}
	if sets[0].DisplayTitle() != "one" || sets[1].DisplayTitle() != "cfo@v2" {
		t.Fatal("titles")
	}
	if len(sets[0].Files) != 2 {
		t.Fatalf("files: %+v", sets[0].Files)
	}
	before := sets[0].Hash
	os.WriteFile(filepath.Join(env, "seeds", "cfo", "v1", "a.sql"), []byte("select 1; -- changed"), 0o644)
	again, _ := Discover(env)
	if again[0].Hash == before {
		t.Fatal("hash did not change after editing a file")
	}
	latest := Latest(sets)
	if latest["cfo"].Version != 2 || latest["ops"].Version != 3 {
		t.Fatal("latest")
	}
	if got := Personas(sets); strings.Join(got, ",") != "cfo,ops" {
		t.Fatalf("personas %v", got)
	}
	if s, ok := Find(sets, "cfo", 0); !ok || s.Version != 2 {
		t.Fatal("find latest")
	}
	if s, ok := Find(sets, "cfo", 1); !ok || s.Version != 1 {
		t.Fatal("find v1")
	}
	if _, ok := Find(sets, "cfo", 9); ok {
		t.Fatal("find missing")
	}
	b, err := sets[0].ReadFile("a.sql")
	if err != nil || !strings.HasPrefix(string(b), "select 1") {
		t.Fatalf("ReadFile: %v %q", err, b)
	}
	if _, err := sets[0].ReadFile("../../../etc/passwd"); err == nil {
		t.Fatal("escape allowed")
	}
}

func TestProblems(t *testing.T) {
	env := t.TempDir()
	writeSet(t, env, "cfo", "v1", `{"persona":"other","version":7,"steps":[
      {"name":"a","kind":"sql","dependency":"db","file":"missing.sql"},
      {"name":"a","kind":"nope"},
      {"kind":"http"},
      {"name":"c","kind":"copy","file":"x","dest":"/tmp/x"}
    ],"verify":[{"name":"v","kind":"sql"}]}`, nil)
	writeSet(t, env, "bad", "v1", `{not json`, nil)
	writeSet(t, env, "empty", "v1", ``, nil)
	sets, err := Discover(env)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]*Set{}
	for _, s := range sets {
		byID[s.ID()] = s
	}
	p := strings.Join(byID["other@v7"].Problems, "\n")
	for _, want := range []string{"does not match directory", "not found", "unknown kind", "duplicate name", "missing name", "http step needs url", "copy step needs target", "sql check needs dependency"} {
		if !strings.Contains(p, want) {
			t.Errorf("missing problem %q in:\n%s", want, p)
		}
	}
	if len(byID["bad@v1"].Problems) == 0 || !strings.Contains(byID["bad@v1"].Problems[0], "seed.json") {
		t.Fatalf("bad json: %v", byID["bad@v1"].Problems)
	}
	if len(byID["empty@v1"].Problems) == 0 || !strings.Contains(byID["empty@v1"].Problems[0], "missing seed.json") {
		t.Fatalf("empty: %v", byID["empty@v1"].Problems)
	}
}

func TestParseRef(t *testing.T) {
	for in, want := range map[string]struct {
		p string
		v int
	}{"cfo": {"cfo", 0}, "cfo@v3": {"cfo", 3}, "cfo@3": {"cfo", 3}} {
		p, v, err := ParseRef(in)
		if err != nil || p != want.p || v != want.v {
			t.Errorf("ParseRef(%q) = %q %d %v", in, p, v, err)
		}
	}
	if _, _, err := ParseRef("cfo@vx"); err == nil {
		t.Fatal("expected error")
	}
	if _, _, err := ParseRef("cfo@0"); err == nil {
		t.Fatal("expected error for zero")
	}
}

func TestDiscoverMissingDir(t *testing.T) {
	sets, err := Discover(t.TempDir())
	if err != nil || len(sets) != 0 {
		t.Fatalf("%v %d", err, len(sets))
	}
}
