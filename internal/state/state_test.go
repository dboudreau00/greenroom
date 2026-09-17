package state

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAppliedRoundTrip(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if a, err := s.LoadApplied("demo"); err != nil || a != nil {
		t.Fatalf("empty: %v %v", a, err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	in := &Applied{Persona: "cfo", Version: 2, SetID: "cfo@v2", SetHash: "abc", At: now, RunID: "r1",
		Fingerprints: map[string]Fingerprint{"db": {Kind: "docker-container", Value: "abc123", At: now}},
		Baselines:    map[string]map[string]float64{"db": {"orders": 12}}}
	if err := s.SaveApplied("demo", in); err != nil {
		t.Fatal(err)
	}
	out, err := s.LoadApplied("demo")
	if err != nil || out == nil || out.SetID != "cfo@v2" || out.Baselines["db"]["orders"] != 12 || out.Fingerprints["db"].Value != "abc123" {
		t.Fatalf("round trip: %+v %v", out, err)
	}
	if err := s.ClearApplied("demo"); err != nil {
		t.Fatal(err)
	}
	if a, _ := s.LoadApplied("demo"); a != nil {
		t.Fatal("not cleared")
	}
	if err := s.ClearApplied("demo"); err != nil {
		t.Fatal("clear twice should be fine")
	}
}

func TestSamplesAndUptime(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	base := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	seq := []bool{true, true, false, false, true, true, true, false}
	for i, ok := range seq {
		if err := s.AppendSample("demo", Sample{At: base.Add(time.Duration(i) * time.Minute), Dep: "db", OK: ok, MS: int64(10 + i), Msg: map[bool]string{false: "down"}[ok]}); err != nil {
			t.Fatal(err)
		}
	}
	s.AppendSample("demo", Sample{At: base.Add(3 * time.Minute), Dep: "web", OK: true, MS: 5})
	now := base.Add(8 * time.Minute)
	all := s.Samples("demo", time.Time{})
	if len(all) != 9 {
		t.Fatalf("samples %d", len(all))
	}
	u := ComputeUptime("db", all, time.Hour, now, 8)
	if u.Total != 8 || u.OK != 5 {
		t.Fatalf("total/ok %d/%d", u.Total, u.OK)
	}
	if u.Percent < 62.4 || u.Percent > 62.6 {
		t.Fatalf("percent %v", u.Percent)
	}
	if len(u.Incidents) != 2 || u.Incidents[0].Open || !u.Incidents[1].Open || u.Incidents[0].Samples != 2 {
		t.Fatalf("incidents %+v", u.Incidents)
	}
	if u.Incidents[0].Duration != 2*time.Minute {
		t.Fatalf("closed incident duration %v", u.Incidents[0].Duration)
	}
	if u.Latest == nil || u.Latest.OK {
		t.Fatal("latest should be the failing sample")
	}
	if u.LastOK == nil || u.LastFail == nil {
		t.Fatal("last ok/fail")
	}
	if u.AvgMS == 0 || u.P95MS == 0 {
		t.Fatal("latency stats")
	}
	if len(u.Points) != 8 {
		t.Fatalf("points %v", u.Points)
	}
	latest := s.Latest("demo")
	if latest["web"].MS != 5 || latest["db"].OK {
		t.Fatalf("latest %+v", latest)
	}
	since := s.Samples("demo", base.Add(6*time.Minute))
	if len(since) != 2 {
		t.Fatalf("since %d", len(since))
	}

	// Reload from disk in a fresh store.
	s2, _ := Open(dir)
	if got := s2.Samples("demo", time.Time{}); len(got) != 9 {
		t.Fatalf("reload %d", len(got))
	}
	if err := s2.Compact("demo", now.Sub(base.Add(5*time.Minute))); err != nil {
		t.Fatal(err)
	}
	// Compact uses wall clock; everything here is in the past so all rows go.
	if got := s2.Samples("demo", time.Time{}); len(got) != 0 {
		t.Fatalf("compact kept %d", len(got))
	}
	s3, _ := Open(dir)
	if got := s3.Samples("demo", time.Time{}); len(got) != 0 {
		t.Fatalf("compact not persisted: %d", len(got))
	}
}

func TestBucketise(t *testing.T) {
	base := time.Now().Add(-time.Hour)
	samples := []Sample{{At: base.Add(time.Minute), Dep: "x", OK: true}, {At: base.Add(59 * time.Minute), Dep: "x", OK: false}}
	pts := bucketise("x", samples, base, base.Add(time.Hour), 6)
	if pts[0] != 1 || pts[5] != 0 || pts[2] != -1 {
		t.Fatalf("points %v", pts)
	}
}

func TestRuns(t *testing.T) {
	s, _ := Open(t.TempDir())
	if list, err := s.ListRuns("demo", 10); err != nil || len(list) != 0 {
		t.Fatalf("empty list %v %v", list, err)
	}
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var ids []string
	for i := 0; i < 5; i++ {
		id := NewRunID(t0.Add(time.Duration(i) * time.Second))
		ids = append(ids, id)
		r := &Run{ID: id, Env: "demo", Kind: RunCheck, Status: StatusOK, StartedAt: t0, Steps: []StepResult{{Name: "a", Status: StatusFailed}}, Log: []LogLine{{Text: "x"}}}
		if err := s.SaveRun(r); err != nil {
			t.Fatal(err)
		}
	}
	if ids[0] >= ids[1] {
		t.Fatal("run ids must sort by time")
	}
	list, err := s.ListRuns("demo", 3)
	if err != nil || len(list) != 3 || list[0].ID != ids[4] || list[0].Failed != 1 || list[0].Steps != 1 {
		t.Fatalf("list %+v %v", list, err)
	}
	r, err := s.LoadRun("demo", ids[2])
	if err != nil || len(r.Log) != 1 {
		t.Fatalf("load %v", err)
	}
	if _, err := s.LoadRun("demo", "../x"); err == nil {
		t.Fatal("bad id accepted")
	}
	if err := s.PruneRuns("demo", 2); err != nil {
		t.Fatal(err)
	}
	list, _ = s.ListRuns("demo", 10)
	if len(list) != 2 || list[1].ID != ids[3] {
		t.Fatalf("prune %+v", list)
	}
	if err := s.SaveRun(&Run{}); err == nil {
		t.Fatal("run without id saved")
	}
}

func TestSnapshotsAndHeals(t *testing.T) {
	s, _ := Open(t.TempDir())
	if err := s.SaveSnapshot("demo", SnapshotRecord{Dep: "db", Strategy: "template", Ref: "demo_golden", At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveSnapshot("demo", SnapshotRecord{Dep: "db", Strategy: "template", Ref: "demo_golden2", At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	s.SaveSnapshot("demo", SnapshotRecord{Dep: "cache", Strategy: "commit", Ref: "img", At: time.Now()})
	list, _ := s.Snapshots("demo")
	if len(list) != 2 {
		t.Fatalf("snapshots %+v", list)
	}
	rec, _ := s.Snapshot("demo", "db")
	if rec == nil || rec.Ref != "demo_golden2" {
		t.Fatalf("replace: %+v", rec)
	}
	if rec, _ := s.Snapshot("demo", "nope"); rec != nil {
		t.Fatal("missing snapshot")
	}
	if last := s.LastHeal("demo", "db"); !last.IsZero() {
		t.Fatal("no heals yet")
	}
	t1 := time.Now().Add(-time.Hour)
	s.AppendHeal("demo", HealEvent{At: t1, Dep: "db", Action: "start", OK: true})
	s.AppendHeal("demo", HealEvent{At: t1.Add(time.Minute), Dep: "web", Action: "restart", OK: false, Error: "boom"})
	heals, _ := s.Heals("demo", 10)
	if len(heals) != 2 || heals[0].Dep != "web" {
		t.Fatalf("heals newest first: %+v", heals)
	}
	if last := s.LastHeal("demo", "db"); !last.Equal(t1) {
		t.Fatalf("last heal %v", last)
	}
	if _, err := os.Stat(filepath.Join(s.Dir(), "demo", "heals.json")); err != nil {
		t.Fatal(err)
	}
}
