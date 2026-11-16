package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestExpand(t *testing.T) {
	lookup := func(k string) string {
		return map[string]string{"A": "1", "HOST": "db.local"}[k]
	}
	cases := map[string]string{
		"plain":                 "plain",
		"${A}":                  "1",
		"x${A}y":                "x1y",
		"${MISSING:-def}":       "def",
		"${MISSING}":            "",
		"$$":                    "$",
		"postgres://u@${HOST}/d": "postgres://u@db.local/d",
		"${A:-zzz}":             "1",
		"${UNCLOSED":            "${UNCLOSED",
	}
	for in, want := range cases {
		if got := Expand(in, lookup); got != want {
			t.Errorf("Expand(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestExpandKnown(t *testing.T) {
	lookup := func(k string) (string, bool) {
		v, ok := map[string]string{"A": "1", "E": ""}[k]
		return v, ok
	}
	cases := map[string]string{
		"${A}/${n}":                        "1/${n}",
		"${MISSING}":                       "${MISSING}",
		"${MISSING:-def}":                  "def",
		"${E}":                             "",
		"${E:-d}":                          "d",
		"$$":                               "$",
		"for n in 1 2; do echo ${n}; done": "for n in 1 2; do echo ${n}; done",
		"${UNCLOSED":                       "${UNCLOSED",
	}
	for in, want := range cases {
		if got := ExpandKnown(in, lookup); got != want {
			t.Errorf("ExpandKnown(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStripComments(t *testing.T) {
	in := `{
  // a comment
  "url": "http://x/y", /* block */ "n": 1
}`
	var v map[string]any
	if err := json.Unmarshal(stripComments([]byte(in)), &v); err != nil {
		t.Fatalf("unmarshal after strip: %v\n%s", err, stripComments([]byte(in)))
	}
	if v["url"] != "http://x/y" || v["n"] != float64(1) {
		t.Fatalf("unexpected %v", v)
	}
}

func TestDurationJSON(t *testing.T) {
	var d struct {
		A Duration `json:"a"`
		B Duration `json:"b"`
	}
	if err := json.Unmarshal([]byte(`{"a":"90s","b":45}`), &d); err != nil {
		t.Fatal(err)
	}
	if d.A.D() != 90*time.Second || d.B.D() != 45*time.Second {
		t.Fatalf("got %v %v", d.A.D(), d.B.D())
	}
	if err := json.Unmarshal([]byte(`{"a":"soon"}`), &d); err == nil {
		t.Fatal("expected error for bad duration")
	}
	b, _ := json.Marshal(Duration(2 * time.Minute))
	if string(b) != `"2m0s"` {
		t.Fatalf("marshal: %s", b)
	}
	if Duration(0).Or(time.Second) != time.Second || Duration(time.Minute).Or(time.Second) != time.Minute {
		t.Fatal("Or")
	}
}

func writeEnv(t *testing.T, dir, body string) string {
	t.Helper()
	envDir := filepath.Join(dir, "environments", "demo")
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(envDir, EnvironmentFile), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return envDir
}

func TestLoadEnvironment(t *testing.T) {
	t.Setenv("DEMO_DSN", "postgres://u:p@h/db")
	dir := t.TempDir()
	envDir := writeEnv(t, dir, `{
  // comments are fine
  "title": "Demo",
  "pin": {"persona": "cfo", "version": 2},
  "env": {"API": "http://api.local"},
  "targets": {"local": {"kind": "local"}},
  "dependencies": [
    {"name": "db", "kind": "postgres", "dsn": "${DEMO_DSN}", "critical": true,
     "drift": [{"name": "orders", "query": "select 1"}]},
    {"name": "api", "kind": "http", "url": "${API}/healthz"},
    {"name": "job", "kind": "command", "target": "local", "command": ["true"], "heal": {"action": "command", "command": ["true"]}, "autoHeal": true}
  ]
}`)
	env, err := LoadEnvironment(envDir)
	if err != nil {
		t.Fatal(err)
	}
	if env.Name != "demo" || env.DisplayTitle() != "Demo" {
		t.Fatalf("name/title: %q %q", env.Name, env.DisplayTitle())
	}
	if env.Dependencies[0].DSN != "postgres://u:p@h/db" {
		t.Fatalf("dsn not expanded: %q", env.Dependencies[0].DSN)
	}
	if env.Dependencies[1].URL != "http://api.local/healthz" {
		t.Fatalf("env block not used: %q", env.Dependencies[1].URL)
	}
	if d, ok := env.Dependency("job"); !ok || d.Heal == nil {
		t.Fatal("lookup")
	}
	envs, err := LoadEnvironments(filepath.Join(dir, "environments"))
	if err != nil || len(envs) != 1 {
		t.Fatalf("LoadEnvironments: %v %d", err, len(envs))
	}
}

func TestValidateErrors(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"tcp address", `{"dependencies":[{"name":"a","kind":"tcp"}]}`, "tcp needs address"},
		{"unknown target", `{"dependencies":[{"name":"a","kind":"docker","container":"c","target":"nope"}]}`, "unknown target"},
		{"duplicate", `{"dependencies":[{"name":"a","kind":"tcp","address":"x:1"},{"name":"a","kind":"tcp","address":"x:2"}]}`, "duplicate name"},
		{"bad heal", `{"dependencies":[{"name":"a","kind":"tcp","address":"x:1","heal":{"action":"explode"}}]}`, "heal.action"},
		{"autoheal", `{"dependencies":[{"name":"a","kind":"tcp","address":"x:1","autoHeal":true}]}`, "autoHeal needs a heal block"},
		{"drift", `{"dependencies":[{"name":"a","kind":"tcp","address":"x:1","drift":[{"name":"x"}]}]}`, "needs query or command"},
		{"kind", `{"dependencies":[{"name":"a","kind":"ftp"}]}`, "unknown kind"},
		{"bad name", `{"dependencies":[{"name":"Bad Name","kind":"tcp","address":"x:1"}]}`, "must match"},
		{"no deps", `{"dependencies":[]}`, "declares no dependencies"},
		{"k8s resource", `{"targets":{"k":{"kind":"kubernetes"}},"dependencies":[{"name":"a","kind":"kubernetes","target":"k","resource":"web"}]}`, "resource like deployment/web"},
		{"via target", `{"dependencies":[{"name":"a","kind":"postgres","via":{"target":"x"}}]}`, "via.target"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			envDir := writeEnv(t, dir, c.body)
			_, err := LoadEnvironment(envDir)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want error containing %q, got %v", c.want, err)
			}
		})
	}
}

func TestLoadWorkspaceDefaults(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GREENROOM_TOKEN", "tok")
	ws, err := LoadWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}
	if ws.Listen != DefaultListen || ws.Token != "tok" {
		t.Fatalf("defaults: %+v", ws)
	}
	if !filepath.IsAbs(ws.StateDir) || filepath.Base(ws.StateDir) != "state" {
		t.Fatalf("state dir %q", ws.StateDir)
	}
	if ws.ProbeInterval.D() != DefaultProbeInterval {
		t.Fatal("interval default")
	}
	os.WriteFile(filepath.Join(dir, WorkspaceFile), []byte(`{"listen": ":9000", "probeInterval": "5s", "stateDir": "/abs/state"}`), 0o644)
	ws, err = LoadWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}
	if ws.Listen != ":9000" || ws.ProbeInterval.D() != 5*time.Second {
		t.Fatalf("loaded: %+v", ws)
	}
	if !filepath.IsAbs(ws.StateDir) {
		t.Fatal("abs state dir kept")
	}
}

func TestWriteJSONAtomic(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "x.json")
	if err := WriteJSON(p, map[string]int{"a": 1}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if !strings.HasSuffix(string(b), "\n") || !strings.Contains(string(b), `"a": 1`) {
		t.Fatalf("content %q", b)
	}
	if _, err := os.Stat(p + ".tmp"); err == nil {
		t.Fatal("temp file left behind")
	}
}

// Concurrent writers of one path each use their own temp file: every write
// lands whole and no temp file is left behind, also when the rename fails.
func TestWriteJSONUniqueTemp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.json")
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- WriteJSON(path, map[string]any{"n": i, "pad": strings.Repeat("x", 4096)})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		// Windows can refuse a rename onto a file another writer is
		// replacing at that moment; that write fails cleanly.
		if err != nil && runtime.GOOS != "windows" {
			t.Errorf("write: %v", err)
		}
	}
	var got map[string]any
	b, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(b, &got) != nil || len(got["pad"].(string)) != 4096 {
		t.Fatalf("torn write: %v %.80s", err, b)
	}
	blocked := filepath.Join(dir, "dir.json")
	os.MkdirAll(filepath.Join(blocked, "child"), 0o755)
	if err := WriteJSON(blocked, 1); err == nil {
		t.Fatal("rename onto a directory succeeded")
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("temp file left: %s", e.Name())
		}
	}
}
