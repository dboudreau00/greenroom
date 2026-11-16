package seeds

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const commentedManifest = `{
  // Larkspur CFO walkthrough.
  "persona": "cfo",
  "version": 2, /* bumped by hand before 0.7.0 */
  "title": "Quarter close",
  "steps": [
    { "name": "schema", "kind": "sql", "dependency": "db", "file": "a.sql",
      "env": { "version": "9", "title": "nested" } },
    { "name": "data", "kind": "sql", "dependency": "db", "sql": "select '\"version\": 1'" }
  ]
}
`

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestBumpLatestKeepsComments(t *testing.T) {
	env := t.TempDir()
	writeSet(t, env, "cfo", "v1", `{"steps":[{"name":"c","kind":"command","command":["true"]}]}`, nil)
	writeSet(t, env, "cfo", "v2", commentedManifest, map[string]string{"a.sql": "select 1;\n"})
	os.MkdirAll(filepath.Join(env, "seeds", "cfo", "v2", ".scratch"), 0o755)
	os.WriteFile(filepath.Join(env, "seeds", "cfo", "v2", ".scratch", "x"), []byte("x"), 0o644)
	os.MkdirAll(filepath.Join(env, "seeds", "cfo", "v2", "fixtures"), 0o755)
	os.WriteFile(filepath.Join(env, "seeds", "cfo", "v2", "fixtures", "rows.csv"), []byte("a,b\n"), 0o644)

	dir, n, err := Bump(env, "cfo", 0, "Quarter close, Tidewell edition")
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 || filepath.Base(dir) != "v3" {
		t.Fatalf("got %s v%d", dir, n)
	}
	got := readFile(t, filepath.Join(dir, "seed.json"))
	want := strings.Replace(strings.Replace(commentedManifest, `"version": 2,`, `"version": 3,`, 1), `"title": "Quarter close",`, `"title": "Quarter close, Tidewell edition",`, 1)
	if got != want {
		t.Fatalf("manifest:\n%s\nwant:\n%s", got, want)
	}
	if readFile(t, filepath.Join(dir, "fixtures", "rows.csv")) != "a,b\n" {
		t.Fatal("nested file not copied")
	}
	if _, err := os.Stat(filepath.Join(dir, ".scratch")); err == nil {
		t.Fatal("hidden directory copied")
	}
	set, err := Load(dir)
	if err != nil || len(set.Problems) > 0 || set.Version != 3 || set.Steps[0].Env["version"] != "9" {
		t.Fatalf("load: %v %+v", err, set)
	}
	entries, _ := os.ReadDir(filepath.Join(env, "seeds", "cfo"))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			t.Fatalf("temp dir left behind: %s", e.Name())
		}
	}
	sets, _ := Discover(env)
	if s, _ := Find(sets, "cfo", 3); s == nil || len(s.Problems) > 0 {
		t.Fatalf("discover: %+v", s)
	}
}

func TestBumpFromExplicitInsertsVersion(t *testing.T) {
	env := t.TempDir()
	writeSet(t, env, "retail-ops", "v1", "{\r\n  \"title\": \"Stores\",\r\n  \"steps\": [{\"name\":\"c\",\"kind\":\"command\",\"command\":[\"true\"]}]\r\n}\r\n", nil)
	writeSet(t, env, "retail-ops", "v4", `{"steps":[{"name":"c","kind":"command","command":["true"]}]}`, nil)
	dir, n, err := Bump(env, "retail-ops", 1, "")
	if err != nil {
		t.Fatal(err)
	}
	if n != 5 {
		t.Fatalf("version %d", n)
	}
	got := readFile(t, filepath.Join(dir, "seed.json"))
	if got != "{\r\n  \"version\": 5,\r\n  \"title\": \"Stores\",\r\n  \"steps\": [{\"name\":\"c\",\"kind\":\"command\",\"command\":[\"true\"]}]\r\n}\r\n" {
		t.Fatalf("manifest %q", got)
	}
	if _, n, err := Bump(env, "retail-ops", 4, ""); err != nil || n != 6 {
		t.Fatalf("compact: %d %v", n, err)
	}
	if !strings.HasPrefix(readFile(t, filepath.Join(env, "seeds", "retail-ops", "v6", "seed.json")), `{"version": 6, "steps"`) {
		t.Fatal("inline insert")
	}
}

func TestBumpRefusals(t *testing.T) {
	env := t.TempDir()
	writeSet(t, env, "cfo", "v1", `{"steps":[{"name":"c","kind":"command","command":["true"]}]}`, nil)
	if _, _, err := Bump(env, "../cfo", 0, ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad persona: %v", err)
	}
	if _, _, err := Bump(env, "nobody", 0, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown persona: %v", err)
	}
	if _, _, err := Bump(env, "cfo", 7, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown version: %v", err)
	}
	if _, _, err := Bump(env, "cfo", 0, "two\nlines"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad title: %v", err)
	}
	// A file named like the next version blocks it.
	os.WriteFile(filepath.Join(env, "seeds", "cfo", "v2"), []byte("x"), 0o644)
	if _, _, err := Bump(env, "cfo", 0, ""); !errors.Is(err, ErrExists) {
		t.Fatalf("target exists: %v", err)
	}
	// A broken source manifest is refused and leaves nothing behind.
	writeSet(t, env, "ops", "v1", `[1, 2]`, nil)
	if _, _, err := Bump(env, "ops", 0, ""); err == nil {
		t.Fatal("non-object manifest accepted")
	}
	if _, err := os.Stat(filepath.Join(env, "seeds", "ops", "v2")); err == nil {
		t.Fatal("v2 created")
	}
}

func TestSetTopLevelNestedUntouched(t *testing.T) {
	in := `{"steps":[{"version":1,"title":"x"}], "meta": {"version": 2}}`
	out, err := SetTopLevel([]byte(in), "version", 3)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"version": 3, "steps":[{"version":1,"title":"x"}], "meta": {"version": 2}}` {
		t.Fatalf("%s", out)
	}
	out, _ = SetTopLevel([]byte(`{}`), "title", "A <b> & \"c\"")
	if string(out) != `{"title": "A <b> & \"c\""}` {
		t.Fatalf("%s", out)
	}
	if _, err := SetTopLevel([]byte(`{"a": }`), "version", 1); err == nil {
		t.Fatal("broken json accepted")
	}
}

const commentedEnv = `{
  // Tidewell demo; edit the pin when a new set is ready.
  "name": "tidewell",
  "pin": {
    "persona": "cfo", // the default walkthrough
    "version": 2
  },
  "dependencies": [ { "name": "api", "kind": "http", "url": "http://127.0.0.1:1/", "pin": { "version": 1 } } ]
}
`

func TestPinKeepsComments(t *testing.T) {
	env := t.TempDir()
	writeSet(t, env, "cfo", "v3", `{"steps":[{"name":"c","kind":"command","command":["true"]}]}`, nil)
	writeSet(t, env, "retail-ops", "v1", `{"steps":[{"name":"c","kind":"command","command":["true"]}]}`, nil)
	path := filepath.Join(env, "environment.json")
	os.WriteFile(path, []byte(commentedEnv), 0o644)
	if err := Pin(env, "cfo", 3); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path); got != strings.Replace(commentedEnv, `"version": 2`, `"version": 3`, 1) {
		t.Fatalf("pin version:\n%s", got)
	}
	if err := Pin(env, "retail-ops", 1); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, path)
	if !strings.Contains(got, `"persona": "retail-ops", // the default walkthrough`) || !strings.Contains(got, `"version": 1`+"\n  },") || !strings.Contains(got, `"pin": { "version": 1 }`) {
		t.Fatalf("pin persona:\n%s", got)
	}
	if err := Pin(env, "cfo", 9); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown set: %v", err)
	}
	// No pin at all: one is inserted.
	os.WriteFile(path, []byte("{\n  \"name\": \"tidewell\"\n}\n"), 0o644)
	if err := Pin(env, "cfo", 3); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path); got != "{\n  \"pin\": { \"persona\": \"cfo\", \"version\": 3 },\n  \"name\": \"tidewell\"\n}\n" {
		t.Fatalf("inserted pin:\n%s", got)
	}
	// A pin without a version gets one inserted.
	os.WriteFile(path, []byte(`{"pin": {"persona": "cfo"}, "name": "tidewell"}`), 0o644)
	if err := Pin(env, "cfo", 3); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path); got != `{"pin": {"version": 3, "persona": "cfo"}, "name": "tidewell"}` {
		t.Fatalf("version insert: %s", got)
	}
}
