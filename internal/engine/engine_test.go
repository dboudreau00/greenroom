package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dboudreau00/greenroom/internal/config"
	"github.com/dboudreau00/greenroom/internal/probe"
	"github.com/dboudreau00/greenroom/internal/seeds"
	"github.com/dboudreau00/greenroom/internal/state"
)

// TestHelperProcess is re-executed as the fake command behind probes, steps,
// heals and drift checks.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	args := os.Args
	for i, a := range args {
		if a == "--" {
			args = args[i+1:]
			break
		}
	}
	if len(args) == 0 {
		os.Exit(2)
	}
	switch args[0] {
	case "echo":
		fmt.Println(strings.Join(args[1:], " "))
	case "cat":
		b, err := os.ReadFile(args[1])
		if err != nil {
			os.Exit(1)
		}
		os.Stdout.Write(b)
	case "exists":
		if _, err := os.Stat(args[1]); err != nil {
			fmt.Fprintln(os.Stderr, "missing")
			os.Exit(1)
		}
		fmt.Println("present")
	case "touch":
		os.WriteFile(args[1], []byte("x"), 0o644)
	case "sleep":
		ms, _ := strconv.Atoi(args[1])
		time.Sleep(time.Duration(ms) * time.Millisecond)
	case "fail":
		fmt.Fprintln(os.Stderr, "boom")
		os.Exit(9)
	}
	os.Exit(0)
}

func helper(cmd ...string) []string {
	return append([]string{os.Args[0], "-test.run=TestHelperProcess", "--"}, cmd...)
}

// testAPI is the http dependency: /healthz reports a version header and can
// be switched to failing; /seed counts seed calls.
type testAPI struct {
	srv     *httptest.Server
	version atomic.Value
	failing atomic.Bool
	seeds   atomic.Int32
}

func newTestAPI(t *testing.T) *testAPI {
	a := &testAPI{}
	a.version.Store("1.0")
	a.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Version", a.version.Load().(string))
		switch r.URL.Path {
		case "/healthz":
			if a.failing.Load() {
				w.WriteHeader(500)
				return
			}
			io.WriteString(w, "ok")
		case "/seed":
			a.seeds.Add(1)
			io.WriteString(w, `{"seeded":true}`)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(a.srv.Close)
	return a
}

type fixture struct {
	dir        string
	envDir     string
	api        *testAPI
	workerFile string
	countFile  string
	eng        *Engine
}

func (f *fixture) writeEnv(t *testing.T, pinVersion int, staleAfter time.Duration) {
	t.Helper()
	env := config.Environment{
		Name: "demo", Title: "Demo", StaleAfter: config.Duration(staleAfter),
		Pin: config.Pin{Persona: "cfo", Version: pinVersion},
		Env: map[string]string{"GO_WANT_HELPER_PROCESS": "1"},
		Targets: map[string]config.Target{"local": {Kind: config.TargetLocal}},
		Dependencies: []config.Dependency{
			{Name: "api", Kind: config.KindHTTP, URL: f.api.srv.URL + "/healthz", Critical: true},
			{Name: "worker", Kind: config.KindCommand, Target: "local", Command: helper("exists", f.workerFile),
				Heal:  &config.Heal{Action: "command", Command: helper("touch", f.workerFile)},
				Drift: []config.DriftCheck{{Name: "count", Command: helper("cat", f.countFile)}, {Name: "bounded", Command: helper("cat", f.countFile), Max: ptr(100)}}},
		},
	}
	b, _ := json.MarshalIndent(env, "", "  ")
	if err := os.WriteFile(filepath.Join(f.envDir, config.EnvironmentFile), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func ptr(f float64) *float64 { return &f }

func (f *fixture) writeSeed(t *testing.T, persona string, version int, m seeds.Manifest, files map[string]string) {
	t.Helper()
	dir := filepath.Join(f.envDir, "seeds", persona, fmt.Sprintf("v%d", version))
	os.MkdirAll(dir, 0o755)
	b, _ := json.MarshalIndent(m, "", "  ")
	os.WriteFile(filepath.Join(dir, config.SeedFile), b, 0o644)
	for n, body := range files {
		os.WriteFile(filepath.Join(dir, n), []byte(body), 0o644)
	}
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{dir: t.TempDir(), api: newTestAPI(t)}
	f.envDir = filepath.Join(f.dir, "environments", "demo")
	os.MkdirAll(f.envDir, 0o755)
	f.workerFile = filepath.Join(f.dir, "worker.flag")
	f.countFile = filepath.Join(f.dir, "count.txt")
	os.WriteFile(f.countFile, []byte("5\n"), 0o644)
	f.writeEnv(t, 1, 7*24*time.Hour)
	seedStep := func(label string) seeds.Manifest {
		return seeds.Manifest{Title: label, Steps: []seeds.Step{
			{Name: "announce", Kind: seeds.StepCommand, Target: "local", Command: helper("echo", "seeding", label)},
			{Name: "load", Kind: seeds.StepHTTP, URL: f.api.srv.URL + "/seed", Method: "POST", Body: `{"x":1}`, ExpectStatus: 200},
			{Name: "settle", Kind: seeds.StepWait, Dependency: "api", Timeout: config.Duration(5 * time.Second)},
			{Name: "script", Kind: seeds.StepShell, Target: "local", File: "notes.txt", ContinueOnError: true},
		}, Verify: []seeds.Check{{Name: "home", Kind: "http", URL: f.api.srv.URL + "/healthz", ExpectStatus: 200}}}
	}
	f.writeSeed(t, "cfo", 1, seedStep("v1"), map[string]string{"notes.txt": "echo hello from a script"})
	f.writeSeed(t, "cfo", 2, seedStep("v2"), map[string]string{"notes.txt": "echo hello from v2"})
	f.writeSeed(t, "slow", 1, seeds.Manifest{Steps: []seeds.Step{{Name: "nap", Kind: seeds.StepCommand, Target: "local", Command: helper("sleep", "1500")}}}, nil)
	ws, err := config.LoadWorkspace(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	st, err := state.Open(ws.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	f.eng = New(ws, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := f.eng.Reload(); err != nil {
		t.Fatal(err)
	}
	return f
}

func itemCodes(rep *StaleReport) string {
	var codes []string
	for _, it := range rep.Items {
		codes = append(codes, it.Code)
	}
	return strings.Join(codes, ",")
}

func TestCheckResetAndStale(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	v, err := f.eng.Env("demo")
	if err != nil {
		t.Fatal(err)
	}

	// Worker flag missing: non-critical dependency down, never reset.
	rep, err := f.eng.Check(ctx, "demo", CheckOptions{Record: true, Drift: true, Actor: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Readiness != Amber || !strings.Contains(rep.Summary, "down: worker") || rep.RunID == "" {
		t.Fatalf("first check: %s %s", rep.Readiness, rep.Summary)
	}
	if !strings.Contains(itemCodes(rep.Stale), "never-reset") || !strings.Contains(itemCodes(rep.Stale), "drift-skipped") {
		t.Fatalf("stale codes %s", itemCodes(rep.Stale))
	}
	if runs, _ := f.eng.Store().ListRuns("demo", 10); len(runs) != 1 || runs[0].Kind != state.RunCheck || runs[0].Status != state.StatusOK {
		t.Fatalf("check run not recorded: %+v", runs)
	}

	// Reset: preflight heals the worker (heal creates the flag), steps run.
	run, err := f.eng.Reset(ctx, "demo", ResetOptions{Actor: "test"})
	if err != nil {
		t.Fatalf("reset failed: %v\n%s", err, logText(run))
	}
	if run.Status != state.StatusOK || run.Persona != "cfo" || run.Version != 1 {
		t.Fatalf("run %+v", run.Brief())
	}
	if !strings.Contains(logText(run), "seeding v1") || !strings.Contains(logText(run), "hello from a script") || !strings.Contains(logText(run), "is up after heal") {
		t.Fatalf("log:\n%s", logText(run))
	}
	if f.api.seeds.Load() != 1 {
		t.Fatalf("http step calls %d", f.api.seeds.Load())
	}
	applied, _ := f.eng.Store().LoadApplied("demo")
	if applied == nil || applied.SetID != "cfo@v1" || applied.Baselines["worker"]["count"] != 5 || applied.Fingerprints["api"].Value != "1.0" {
		t.Fatalf("applied %+v", applied)
	}
	steps := map[string]string{}
	for _, s := range run.Steps {
		steps[s.Name] = s.Status
	}
	for _, name := range []string{"preflight", "announce", "load", "settle", "script", "verify home", "record"} {
		if steps[name] != state.StatusOK {
			t.Fatalf("step %s = %q (%v)", name, steps[name], steps)
		}
	}

	rep, _ = f.eng.Check(ctx, "demo", CheckOptions{Drift: true})
	if rep.Readiness != Green || !rep.Stale.Fresh {
		t.Fatalf("after reset: %s %s %s", rep.Readiness, rep.Summary, itemCodes(rep.Stale))
	}

	// Drift against the baseline.
	os.WriteFile(f.countFile, []byte("7\n"), 0o644)
	sr, _ := f.eng.StaleReport(ctx, v, nil, true)
	if sr.Stale != 1 || !strings.Contains(itemCodes(sr), "drift") || !strings.Contains(sr.Items[0].Message, "count is 7, expected 5") {
		t.Fatalf("drift: %s %+v", itemCodes(sr), sr.Items)
	}
	os.WriteFile(f.countFile, []byte("5\n"), 0o644)

	// Seed file edited after the reset.
	os.WriteFile(filepath.Join(f.envDir, "seeds", "cfo", "v1", "notes.txt"), []byte("echo changed"), 0o644)
	f.eng.Reload()
	v, _ = f.eng.Env("demo")
	sr, _ = f.eng.StaleReport(ctx, v, nil, false)
	if !strings.Contains(itemCodes(sr), "seed-changed") || !strings.Contains(sr.Items[0].Message, "notes.txt (modified)") {
		t.Fatalf("seed-changed: %s %+v", itemCodes(sr), sr.Items)
	}
	os.WriteFile(filepath.Join(f.envDir, "seeds", "cfo", "v1", "notes.txt"), []byte("echo hello from a script"), 0o644)
	f.eng.Reload()
	v, _ = f.eng.Env("demo")

	// Application version changed: informational.
	f.api.version.Store("2.0")
	results := v.Prober.ProbeAll(ctx)
	sr, _ = f.eng.StaleReport(ctx, v, results, false)
	if !strings.Contains(itemCodes(sr), "app-changed") || sr.Stale != 0 {
		t.Fatalf("app-changed: %s", itemCodes(sr))
	}

	// Too old.
	applied.At = time.Now().Add(-8 * 24 * time.Hour)
	f.eng.Store().SaveApplied("demo", applied)
	sr, _ = f.eng.StaleReport(ctx, v, nil, false)
	if !strings.Contains(itemCodes(sr), "too-old") {
		t.Fatalf("too-old: %s", itemCodes(sr))
	}

	// Pin moved to v2 while v1 is applied.
	f.writeEnv(t, 2, 7*24*time.Hour)
	f.eng.Reload()
	v, _ = f.eng.Env("demo")
	sr, _ = f.eng.StaleReport(ctx, v, nil, false)
	if !strings.Contains(itemCodes(sr), "wrong-version") || sr.Pinned != "cfo@v2" {
		t.Fatalf("wrong-version: %s pinned %s", itemCodes(sr), sr.Pinned)
	}
	sum := f.eng.Summary(ctx, v)
	if sum.Readiness != Amber || sum.Stale < 1 || sum.Pinned != "cfo@v2" || sum.SeedSets != 3 {
		t.Fatalf("summary %+v", sum)
	}

	// Reset to the pin: fresh again, and the run list shows both resets.
	run, err = f.eng.Reset(ctx, "demo", ResetOptions{})
	if err != nil || run.Version != 2 {
		t.Fatalf("reset v2: %v", err)
	}
	sr, _ = f.eng.StaleReport(ctx, v, nil, true)
	if !sr.Fresh {
		t.Fatalf("not fresh after v2: %s", itemCodes(sr))
	}
	d, err := f.eng.Detail(ctx, v, false)
	if err != nil || len(d.Runs) < 3 || len(d.Seeds) != 3 || !d.Seeds[1].Applied || !d.Seeds[1].Pinned {
		t.Fatalf("detail %+v %v", d.Seeds, err)
	}
	if d.Dependencies[1].Latest == nil || !d.Dependencies[1].Latest.OK || d.Dependencies[1].Uptime24h.Total == 0 {
		t.Fatalf("dependency view %+v", d.Dependencies[1])
	}
	if ts := f.eng.Targets(ctx, v); len(ts) != 1 || !ts[0].OK {
		t.Fatalf("targets %+v", ts)
	}
}

func logText(r *state.Run) string {
	if r == nil {
		return ""
	}
	var sb strings.Builder
	for _, l := range r.Log {
		sb.WriteString(l.Level + " [" + l.Step + "] " + l.Text + "\n")
	}
	return sb.String()
}

func TestHealBusyAndEvents(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	run, err := f.eng.Heal(ctx, "demo", "worker", false, "test")
	if err != nil {
		t.Fatalf("heal: %v\n%s", err, logText(run))
	}
	if run.Status != state.StatusOK || !strings.Contains(run.Summary, "healed with command") {
		t.Fatalf("heal run %+v", run.Brief())
	}
	if heals, _ := f.eng.Store().Heals("demo", 5); len(heals) != 1 || !heals[0].OK || heals[0].Auto {
		t.Fatalf("heal events %+v", heals)
	}
	if _, err := f.eng.Heal(ctx, "demo", "api", false, "test"); err == nil {
		t.Fatal("heal without a heal block accepted")
	}

	id, err := f.eng.StartReset("demo", ResetOptions{Persona: "slow"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.eng.StartHeal("demo", "worker", "test"); err == nil || !strings.Contains(err.Error(), "busy") {
		t.Fatalf("expected busy error, got %v", err)
	}
	v, _ := f.eng.Env("demo")
	if f.eng.Busy(v) != id {
		t.Fatalf("busy marker %q", f.eng.Busy(v))
	}
	snap, ch, cancel, live := f.eng.Subscribe(id)
	if !live || snap.ID != id {
		t.Fatal("subscribe to live run")
	}
	defer cancel()
	var types []string
	deadline := time.After(20 * time.Second)
	for done := false; !done; {
		select {
		case ev, ok := <-ch:
			if !ok {
				done = true
				break
			}
			types = append(types, ev.Type)
			if ev.Type == "done" {
				done = true
			}
		case <-deadline:
			t.Fatal("run did not finish")
		}
	}
	joined := strings.Join(types, ",")
	if !strings.Contains(joined, "step") || !strings.HasSuffix(joined, "done") {
		t.Fatalf("event types %s", joined)
	}
	final, err := f.eng.Run("demo", id)
	if err != nil || final.Status != state.StatusOK {
		t.Fatalf("final run %v %v", final.Brief(), err)
	}
	if f.eng.Busy(v) != "" {
		t.Fatal("still busy after run")
	}
	if _, _, _, live := f.eng.Subscribe(id); live {
		t.Fatal("finished run reported live")
	}
	if len(f.eng.ActiveRuns("demo")) != 0 {
		t.Fatal("active runs after completion")
	}
}

func TestRedAndForce(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	os.WriteFile(f.workerFile, []byte("x"), 0o644)
	f.api.failing.Store(true)
	rep, _ := f.eng.Check(ctx, "demo", CheckOptions{})
	if rep.Readiness != Red || !strings.Contains(rep.Summary, "api") {
		t.Fatalf("red expected: %s %s", rep.Readiness, rep.Summary)
	}
	run, err := f.eng.Reset(ctx, "demo", ResetOptions{})
	if err == nil || run.Status != state.StatusFailed || !strings.Contains(run.Error, "preflight failed") {
		t.Fatalf("preflight should fail: %v %+v", err, run.Brief())
	}
	run, err = f.eng.Reset(ctx, "demo", ResetOptions{Force: true})
	if err == nil || !strings.Contains(logText(run), "continuing despite") {
		t.Fatalf("forced reset should pass preflight then fail later: %v\n%s", err, logText(run))
	}
	if a, _ := f.eng.Store().LoadApplied("demo"); a != nil {
		t.Fatal("failed reset left an applied record")
	}
	f.api.failing.Store(false)
	if _, err := f.eng.ResolveSet(mustEnv(t, f.eng), "nope", 0); err == nil {
		t.Fatal("unknown persona resolved")
	}
	// An explicit persona with no version means its latest version; no
	// persona at all means the pin.
	if s, err := f.eng.ResolveSet(mustEnv(t, f.eng), "cfo", 0); err != nil || s.Version != 2 {
		t.Fatalf("latest resolve %v %v", s, err)
	}
	if s, err := f.eng.ResolveSet(mustEnv(t, f.eng), "", 0); err != nil || s.Version != 1 {
		t.Fatalf("pinned resolve %v %v", s, err)
	}
	if s, err := f.eng.ResolveSet(mustEnv(t, f.eng), "", 2); err != nil || s.Version != 2 {
		t.Fatalf("version override %v %v", s, err)
	}
}

func mustEnv(t *testing.T, e *Engine) *Env {
	v, err := e.Env("demo")
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestReadinessTable(t *testing.T) {
	r := func(ok, warn, critical bool) probeResult { return probeResult{ok, warn, critical} }
	cases := []struct {
		results []probeResult
		stale   int
		want    string
	}{
		{[]probeResult{r(true, false, true)}, 0, Green},
		{[]probeResult{r(true, false, true)}, 1, Amber},
		{[]probeResult{r(true, true, false)}, 0, Amber},
		{[]probeResult{r(false, false, false)}, 0, Amber},
		{[]probeResult{r(false, false, true), r(true, false, false)}, 0, Red},
		{nil, 0, Amber},
	}
	for i, c := range cases {
		got, _ := Readiness(toResults(c.results), &StaleReport{Stale: c.stale})
		if got != c.want {
			t.Errorf("case %d: got %s want %s", i, got, c.want)
		}
	}
}

type probeResult struct{ ok, warn, critical bool }

func toResults(in []probeResult) (out []probe.Result) {
	for i, r := range in {
		out = append(out, probe.Result{Dep: fmt.Sprintf("d%d", i), OK: r.ok, Warn: r.warn, Critical: r.critical})
	}
	return out
}

func TestCompareDrift(t *testing.T) {
	five := 5.0
	if ok, _ := compareDrift(5, config.DriftCheck{Expect: &five}, nil); !ok {
		t.Fatal("exact")
	}
	if ok, _ := compareDrift(7, config.DriftCheck{Expect: &five, Tolerance: 2}, nil); !ok {
		t.Fatal("tolerance")
	}
	if ok, _ := compareDrift(8, config.DriftCheck{Expect: &five, Tolerance: 2}, nil); ok {
		t.Fatal("outside tolerance")
	}
	if ok, want := compareDrift(50, config.DriftCheck{Min: ptr(1), Max: ptr(10)}, nil); ok || want != "at least 1 and at most 10" {
		t.Fatalf("bounds %v %q", ok, want)
	}
	if ok, _ := compareDrift(9, config.DriftCheck{}, &five); ok {
		t.Fatal("baseline mismatch accepted")
	}
	if ok, _ := compareDrift(9, config.DriftCheck{}, nil); !ok {
		t.Fatal("no expectation should pass")
	}
	if humanDuration(90*time.Minute) != "1h30m" || humanDuration(3*24*time.Hour) != "3d" || humanDuration(45*time.Second) != "45s" {
		t.Fatal("humanDuration")
	}
}
