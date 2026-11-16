package engine

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dboudreau00/greenroom/internal/brand"
	"github.com/dboudreau00/greenroom/internal/config"
	"github.com/dboudreau00/greenroom/internal/demos"
	"github.com/dboudreau00/greenroom/internal/seeds"
	"github.com/dboudreau00/greenroom/internal/state"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Set(t time.Time) {
	c.mu.Lock()
	c.t = t
	c.mu.Unlock()
}

// demoFixture is the engine fixture with the worker up, a fake clock at
// 13:00 UTC and a brand kit.
func demoFixture(t *testing.T) (*fixture, *fakeClock, time.Time) {
	t.Helper()
	f := newFixture(t)
	os.WriteFile(f.workerFile, []byte("x"), 0o644)
	t0 := time.Date(2026, 11, 20, 13, 0, 0, 0, time.UTC)
	clk := &fakeClock{t: t0}
	f.eng.Now = clk.Now
	if err := f.eng.Brands().Save(&brand.Kit{Slug: "larkspur", Name: "Larkspur"}); err != nil {
		t.Fatal(err)
	}
	return f, clk, t0
}

// waitIdle waits for a run to end and the environment to be free.
func waitIdle(t *testing.T, f *fixture, runID string) *state.Run {
	t.Helper()
	v, _ := f.eng.Env("demo")
	deadline := time.Now().Add(30 * time.Second)
	for {
		run, err := f.eng.Run("demo", runID)
		if err == nil && run.Status != state.StatusRunning && f.eng.Busy(v) == "" {
			return run
		}
		if time.Now().After(deadline) {
			t.Fatalf("run %s did not finish: %v %+v", runID, err, run)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func mustDemo(t *testing.T, f *fixture, id string) *demos.Demo {
	t.Helper()
	d, err := f.eng.Demos().Get(id)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func journalText(d *demos.Demo) string {
	var sb strings.Builder
	for _, e := range d.Journal {
		sb.WriteString(e.Kind + " " + e.Author + ": " + e.Text + "\n")
	}
	return sb.String()
}

func TestBookingValidation(t *testing.T) {
	f, _, t0 := demoFixture(t)
	at := func(h, m int) time.Time { return t0.Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute) }
	base := DemoRequest{Env: "demo", Title: "Tidewell discovery", Prospect: "Tidewell", Start: at(2, 0), Duration: config.Duration(45 * time.Minute)}
	d, err := f.eng.BookDemo(base, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if !demos.ValidID(d.ID) || !strings.HasPrefix(d.ID, "20261120-1500-") || d.Status != demos.Scheduled || d.SetID() != "cfo@v1" ||
		d.Stage != "discovery" || d.PrepLead.D() != 30*time.Minute || d.Owner != "alice" || !d.End.Equal(at(2, 45)) {
		t.Fatalf("booked demo %+v", d)
	}
	cases := []struct {
		name string
		mod  func(r *DemoRequest)
		want string
	}{
		{"past start", func(r *DemoRequest) { r.Start = t0.Add(-time.Hour) }, "future"},
		{"start in 30s", func(r *DemoRequest) { r.Start = t0.Add(30 * time.Second) }, "future"},
		{"no start", func(r *DemoRequest) { r.Start = time.Time{} }, "start is required"},
		{"end before start", func(r *DemoRequest) { r.Duration = 0; r.End = r.Start.Add(-time.Minute) }, "end must be after"},
		{"too long", func(r *DemoRequest) { r.Start = at(20, 0); r.Duration = config.Duration(13 * time.Hour) }, "at most"},
		{"unknown env", func(r *DemoRequest) { r.Env = "nope" }, "not found"},
		{"path env", func(r *DemoRequest) { r.Env = "../demo" }, "not found"},
		{"unknown brand", func(r *DemoRequest) { r.Start = at(5, 0); r.Brand = "nobody" }, "brand kit nobody"},
		{"path brand", func(r *DemoRequest) { r.Start = at(5, 0); r.Brand = "../../etc" }, "brand kit"},
		{"unknown persona", func(r *DemoRequest) { r.Start = at(5, 0); r.Persona = "ghost" }, "ghost"},
		{"unknown version", func(r *DemoRequest) { r.Start = at(5, 0); r.Persona, r.Version = "cfo", 9 }, "cfo@v9"},
		{"bad stage", func(r *DemoRequest) { r.Start = at(5, 0); r.Stage = "closing" }, "stage"},
		{"bad outcome", func(r *DemoRequest) { r.Start = at(5, 0); r.Outcome = "won" }, "outcome"},
		{"short prep", func(r *DemoRequest) { r.Start = at(5, 0); r.PrepLead = config.Duration(time.Minute) }, "prep lead"},
		{"no title", func(r *DemoRequest) { r.Start = at(5, 0); r.Title = " \x00 " }, "title is required"},
		{"too many attendees", func(r *DemoRequest) {
			r.Start = at(5, 0)
			for i := 0; i < 21; i++ {
				r.Attendees = append(r.Attendees, "person")
			}
		}, "attendees"},
	}
	for _, c := range cases {
		req := base
		c.mod(&req)
		if _, err := f.eng.BookDemo(req, "alice"); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want %q", c.name, err, c.want)
		}
	}
	over := base
	over.Start = at(2, 30)
	_, err = f.eng.BookDemo(over, "bob")
	if !errors.Is(err, demos.ErrConflict) || !strings.Contains(err.Error(), d.ID) {
		t.Fatalf("overlap: %v", err)
	}
	next := base
	next.Start, next.Title = at(2, 45), "Back to back\ttitle\x07"
	n, err := f.eng.BookDemo(next, "bob")
	if err != nil || n.Title != "Back to back title" {
		t.Fatalf("adjacent slot: %v %+v", err, n)
	}
	if _, err := f.eng.CancelDemo(d.ID, "alice"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.eng.BookDemo(base, "bob"); err != nil {
		t.Fatalf("a cancelled demo must free its slot: %v", err)
	}
	if _, err := f.eng.EditDemo(n.ID, DemoEdit{Start: ptrTime(at(2, 10))}, "bob"); !errors.Is(err, demos.ErrConflict) {
		t.Fatalf("edit into an overlap: %v", err)
	}
	brandSlug, stage := "larkspur", "deep-dive"
	e, err := f.eng.EditDemo(n.ID, DemoEdit{Start: ptrTime(at(6, 0)), Brand: &brandSlug, Stage: &stage}, "bob")
	if err != nil || !e.End.Equal(at(6, 45)) || e.Brand != "larkspur" || !strings.Contains(journalText(e), "changed by bob: stage, time, brand") {
		t.Fatalf("reschedule: %v %+v\n%s", err, e, journalText(e))
	}
}

func ptrTime(t time.Time) *time.Time { return &t }

func TestDemoTimeline(t *testing.T) {
	f, clk, t0 := demoFixture(t)
	ctx := context.Background()
	d, err := f.eng.BookDemo(DemoRequest{Env: "demo", Title: "Larkspur deep dive", Prospect: "Larkspur", Persona: "cfo", Version: 2, Brand: "larkspur",
		Start: t0.Add(2 * time.Hour), Duration: config.Duration(45 * time.Minute), ResetAfter: true}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	f.eng.DemoTick(ctx)
	if got := mustDemo(t, f, d.ID); got.Status != demos.Scheduled {
		t.Fatalf("too early to prepare: %s", got.Status)
	}

	// 14:31 is inside the 30 minute prep lead.
	clk.Set(t0.Add(91 * time.Minute))
	f.eng.DemoTick(ctx)
	got := mustDemo(t, f, d.ID)
	if got.Status != demos.Preparing || got.PrepRunID == "" {
		t.Fatalf("prep not started: %+v\n%s", got, journalText(got))
	}
	prep := waitIdle(t, f, got.PrepRunID)
	if prep.Status != state.StatusOK || prep.Persona != "cfo" || prep.Version != 2 || prep.Brand != "larkspur" || prep.Actor != "demo:"+d.ID {
		t.Fatalf("prep run %+v\n%s", prep.Brief(), logText(prep))
	}

	f.eng.DemoTick(ctx)
	got = mustDemo(t, f, d.ID)
	if got.Status != demos.Ready || got.CheckRunID == "" || got.Readiness == "" {
		t.Fatalf("not ready after prep: %+v\n%s", got, journalText(got))
	}
	if chk, err := f.eng.Run("demo", got.CheckRunID); err != nil || chk.Kind != state.RunCheck || chk.Actor != "demo:"+d.ID {
		t.Fatalf("check run %v %+v", err, chk)
	}
	// Before the start nothing is protected yet.
	if live := f.eng.liveDemo("demo"); live != nil {
		t.Fatalf("protected before the start: %s", live.ID)
	}

	clk.Set(t0.Add(2 * time.Hour))
	f.eng.DemoTick(ctx)
	if got = mustDemo(t, f, d.ID); got.Status != demos.Live {
		t.Fatalf("not live at the start: %s", got.Status)
	}
	v, _ := f.eng.Env("demo")
	sum := f.eng.Summary(ctx, v)
	if sum.LiveDemo == nil || sum.LiveDemo.ID != d.ID {
		t.Fatalf("summary live demo %+v", sum.LiveDemo)
	}

	clk.Set(t0.Add(130 * time.Minute))
	_, err = f.eng.StartReset("demo", ResetOptions{Actor: "bob"})
	if !errors.Is(err, ErrBooked) || !strings.Contains(err.Error(), "booked") || !strings.Contains(err.Error(), d.ID) || !strings.Contains(err.Error(), "15:45") {
		t.Fatalf("reset during a live demo: %v", err)
	}
	if _, err := f.eng.Reset(ctx, "demo", ResetOptions{Actor: "bob"}); !errors.Is(err, ErrBooked) {
		t.Fatalf("sync reset during a live demo: %v", err)
	}
	if _, err := f.eng.StartSnapshot("demo", "worker", "bob", false); !errors.Is(err, ErrBooked) {
		t.Fatalf("snapshot during a live demo: %v", err)
	}
	if run, err := f.eng.Heal(ctx, "demo", "worker", false, "bob"); err != nil || run.Status != state.StatusOK {
		t.Fatalf("heals stay allowed: %v", err)
	}
	over, err := f.eng.StartReset("demo", ResetOptions{Actor: "bob", Override: true})
	if err != nil {
		t.Fatalf("override: %v", err)
	}
	waitIdle(t, f, over)
	got = mustDemo(t, f, d.ID)
	last := got.Journal[len(got.Journal)-1]
	if last.Kind != "system" || last.RunID != over || !strings.Contains(last.Text, "reset by bob during the live demo (override)") {
		t.Fatalf("override not journaled: %+v", last)
	}

	clk.Set(t0.Add(165 * time.Minute))
	f.eng.DemoTick(ctx)
	got = mustDemo(t, f, d.ID)
	if got.Status != demos.Done || got.Cleanup != demos.CleanupStarted || got.CleanupRunID == "" {
		t.Fatalf("done and cleanup: %+v\n%s", got, journalText(got))
	}
	cleanup := waitIdle(t, f, got.CleanupRunID)
	if cleanup.Status != state.StatusOK || cleanup.Persona != "cfo" || cleanup.Version != 1 || cleanup.Brand != "" {
		t.Fatalf("cleanup run %+v\n%s", cleanup.Brief(), logText(cleanup))
	}
	if a, _ := f.eng.Store().LoadApplied("demo"); a == nil || a.SetID != "cfo@v1" || a.Brand != nil {
		t.Fatalf("applied after cleanup %+v", a)
	}
	f.eng.DemoTick(ctx)
	if again := mustDemo(t, f, d.ID); again.CleanupRunID != got.CleanupRunID || len(again.Journal) != len(got.Journal) {
		t.Fatal("a finished demo kept changing")
	}
	text := journalText(got)
	for _, want := range []string{"booked by alice", "preparing: reset to cfo@v2, brand larkspur", "ready: ", "live", "done", "cleanup: reset to the pin"} {
		if !strings.Contains(text, want) {
			t.Errorf("journal lacks %q:\n%s", want, text)
		}
	}
	if _, err := f.eng.StartReset("demo", ResetOptions{Actor: "bob"}); err != nil {
		t.Fatalf("reset after the demo: %v", err)
	}
}

func TestDemoPrepFailureAndMissed(t *testing.T) {
	f, clk, t0 := demoFixture(t)
	ctx := context.Background()
	f.writeSeed(t, "broken", 1, seeds.Manifest{Steps: []seeds.Step{{Name: "boom", Kind: seeds.StepCommand, Target: "local", Command: helper("fail")}}}, nil)
	if err := f.eng.Reload(); err != nil {
		t.Fatal(err)
	}
	bad, err := f.eng.BookDemo(DemoRequest{Env: "demo", Title: "Broken", Persona: "broken", Start: t0.Add(time.Hour), Duration: config.Duration(30 * time.Minute)}, "alice")
	if err != nil {
		t.Fatal(err)
	}
	missed, err := f.eng.BookDemo(DemoRequest{Env: "demo", Title: "Nobody home", Start: t0.Add(5 * time.Hour), Duration: config.Duration(30 * time.Minute)}, "alice")
	if err != nil {
		t.Fatal(err)
	}
	clk.Set(t0.Add(40 * time.Minute))
	f.eng.DemoTick(ctx)
	got := mustDemo(t, f, bad.ID)
	if got.Status != demos.Preparing {
		t.Fatalf("prep not started: %s", got.Status)
	}
	waitIdle(t, f, got.PrepRunID)
	f.eng.DemoTick(ctx)
	got = mustDemo(t, f, bad.ID)
	if got.Status != demos.PrepFailed || !strings.Contains(got.Error, "boom") && !strings.Contains(got.Error, "step") {
		t.Fatalf("prep failure: %+v\n%s", got, journalText(got))
	}
	clk.Set(t0.Add(time.Hour))
	f.eng.DemoTick(ctx)
	if got = mustDemo(t, f, bad.ID); got.Status != demos.Live {
		t.Fatalf("a failed prep still goes live at the start: %s", got.Status)
	}
	if _, err := f.eng.StartReset("demo", ResetOptions{Actor: "bob"}); !errors.Is(err, ErrBooked) {
		t.Fatalf("prep-failed live demo must be protected: %v", err)
	}

	// The portal was down from before the second demo's prep until after it.
	clk.Set(t0.Add(6 * time.Hour))
	f.eng.DemoTick(ctx)
	if got = mustDemo(t, f, missed.ID); got.Status != demos.Missed || !strings.Contains(journalText(got), "missed") {
		t.Fatalf("missed: %+v", got)
	}
	if got = mustDemo(t, f, bad.ID); got.Status != demos.Done || got.Cleanup != "" {
		t.Fatalf("first demo after its end: %+v", got)
	}
	if _, err := f.eng.PrepareDemo(missed.ID, "alice"); !errors.Is(err, demos.ErrState) {
		t.Fatalf("prepare a missed demo: %v", err)
	}
}

func TestDemoBusyRetryAndPrepareNow(t *testing.T) {
	f, clk, t0 := demoFixture(t)
	ctx := context.Background()
	d, err := f.eng.BookDemo(DemoRequest{Env: "demo", Title: "Quillmark intro", Stage: "intro", Start: t0.Add(time.Hour), Duration: config.Duration(30 * time.Minute)}, "alice")
	if err != nil {
		t.Fatal(err)
	}
	slow, err := f.eng.StartReset("demo", ResetOptions{Persona: "slow", Actor: "bob"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.eng.PrepareDemo(d.ID, "alice"); !errors.Is(err, ErrBusy) {
		t.Fatalf("prepare now while busy: %v", err)
	}
	clk.Set(t0.Add(31 * time.Minute))
	f.eng.DemoTick(ctx)
	if got := mustDemo(t, f, d.ID); got.Status != demos.Scheduled || got.PrepRunID != "" {
		t.Fatalf("busy environment must wait: %+v", got)
	}
	waitIdle(t, f, slow)
	f.eng.DemoTick(ctx)
	got := mustDemo(t, f, d.ID)
	if got.Status != demos.Preparing {
		t.Fatalf("retry after busy: %+v", got)
	}
	waitIdle(t, f, got.PrepRunID)

	other, err := f.eng.BookDemo(DemoRequest{Env: "demo", Title: "Early prep", Start: t0.Add(4 * time.Hour), Duration: config.Duration(30 * time.Minute)}, "alice")
	if err != nil {
		t.Fatal(err)
	}
	p, err := f.eng.PrepareDemo(other.ID, "carol")
	if err != nil || p.Status != demos.Preparing || p.PrepRunID == "" || !strings.Contains(journalText(p), "started early by carol") {
		t.Fatalf("prepare now: %v %+v", err, p)
	}
	waitIdle(t, f, p.PrepRunID)
	if _, err := f.eng.PrepareDemo(other.ID, "carol"); !errors.Is(err, demos.ErrState) {
		t.Fatalf("prepare twice: %v", err)
	}
}

func TestDemoJournalCancelAndLog(t *testing.T) {
	f, _, t0 := demoFixture(t)
	d, err := f.eng.BookDemo(DemoRequest{Env: "demo", Title: "Tidewell exec", Start: t0.Add(3 * time.Hour), Duration: config.Duration(time.Hour)}, "alice")
	if err != nil {
		t.Fatal(err)
	}
	d, err = f.eng.AddDemoEntry(d.ID, "issue", "line one\r\nline two\x1b[31m", "bob")
	if err != nil {
		t.Fatal(err)
	}
	en := d.Journal[len(d.Journal)-1]
	if en.Kind != "issue" || en.Text != "line one\nline two[31m" || en.Author != "bob" || !demos.ValidEntryID(en.ID) {
		t.Fatalf("entry %+v", en)
	}
	for _, bad := range []struct{ kind, text string }{{"system", "x"}, {"rumor", "x"}, {"note", "  "}, {"note", strings.Repeat("a", 4001)}} {
		if _, err := f.eng.AddDemoEntry(d.ID, bad.kind, bad.text, "bob"); err == nil {
			t.Errorf("entry %q accepted", bad.kind)
		}
	}
	if _, err := f.eng.DeleteDemoEntry(d.ID, d.Journal[0].ID, "bob"); !errors.Is(err, demos.ErrState) {
		t.Fatalf("delete a system entry: %v", err)
	}
	if _, err := f.eng.DeleteDemoEntry(d.ID, "../x", "bob"); !errors.Is(err, demos.ErrNotFound) {
		t.Fatalf("bad entry id: %v", err)
	}
	if d, err = f.eng.DeleteDemoEntry(d.ID, en.ID, "bob"); err != nil || strings.Contains(journalText(d), "line one") {
		t.Fatalf("delete entry: %v", err)
	}
	if _, err := f.eng.AddDemoEntry("../../x", "note", "x", "bob"); !errors.Is(err, demos.ErrNotFound) {
		t.Fatalf("bad demo id: %v", err)
	}
	d, err = f.eng.CancelDemo(d.ID, "bob")
	if err != nil || d.Status != demos.Cancelled || !strings.Contains(journalText(d), "cancelled by bob") {
		t.Fatalf("cancel: %v %+v", err, d)
	}
	if _, err := f.eng.CancelDemo(d.ID, "bob"); !errors.Is(err, demos.ErrState) {
		t.Fatalf("cancel twice: %v", err)
	}
	reason := "rescheduled"
	if d, err = f.eng.EditDemo(d.ID, DemoEdit{Outcome: &reason}, "bob"); err != nil || d.Outcome != "rescheduled" {
		t.Fatalf("outcome on a cancelled demo: %v", err)
	}
	if _, err := f.eng.EditDemo(d.ID, DemoEdit{Start: ptrTime(t0.Add(9 * time.Hour))}, "bob"); !errors.Is(err, demos.ErrState) {
		t.Fatalf("reschedule a cancelled demo: %v", err)
	}

	logged, err := f.eng.LogDemo(DemoRequest{Env: "demo", Title: "Hallway demo", Prospect: "Quillmark", Outcome: "positive"}, "carol")
	if err != nil || logged.Booked || logged.Status != demos.Done || !logged.Start.Equal(t0) {
		t.Fatalf("log: %v %+v", err, logged)
	}
	if _, err := f.eng.LogDemo(DemoRequest{Env: "demo", Title: "Future", Start: t0.Add(time.Hour)}, "carol"); err == nil {
		t.Fatal("a logged demo in the future was accepted")
	}

	up, _ := f.eng.ListDemos(DemoFilter{})
	past, _ := f.eng.ListDemos(DemoFilter{Scope: "past"})
	all, _ := f.eng.ListDemos(DemoFilter{Scope: "all", Env: "demo"})
	if len(up) != 0 || len(past) != 2 || len(all) != 2 || past[0].ID != d.ID {
		t.Fatalf("listing: up %d past %d all %d", len(up), len(past), len(all))
	}
	if _, err := f.eng.ListDemos(DemoFilter{Scope: "soon"}); err == nil {
		t.Fatal("bad scope accepted")
	}
	if _, err := f.eng.ListDemos(DemoFilter{Env: "../x"}); err == nil {
		t.Fatal("bad env filter accepted")
	}
}

func TestDemoCleanupSkippedForNextDemo(t *testing.T) {
	f, clk, t0 := demoFixture(t)
	ctx := context.Background()
	first, err := f.eng.BookDemo(DemoRequest{Env: "demo", Title: "First", Start: t0.Add(time.Hour), Duration: config.Duration(30 * time.Minute), ResetAfter: true}, "alice")
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.eng.BookDemo(DemoRequest{Env: "demo", Title: "Second", Start: t0.Add(90 * time.Minute), Duration: config.Duration(30 * time.Minute), Brand: "larkspur"}, "alice")
	if err != nil {
		t.Fatal(err)
	}
	clk.Set(t0.Add(31 * time.Minute))
	f.eng.DemoTick(ctx)
	waitIdle(t, f, mustDemo(t, f, first.ID).PrepRunID)
	f.eng.DemoTick(ctx)
	clk.Set(t0.Add(time.Hour))
	f.eng.DemoTick(ctx)
	// 15:05: the second demo's prep window is open but the first is on stage.
	clk.Set(t0.Add(65 * time.Minute))
	f.eng.DemoTick(ctx)
	if got := mustDemo(t, f, second.ID); got.Status != demos.Scheduled || !strings.Contains(journalText(got), "preparation waits") {
		t.Fatalf("second demo must wait for the first: %+v\n%s", got, journalText(got))
	}
	n := len(mustDemo(t, f, second.ID).Journal)
	f.eng.DemoTick(ctx)
	if len(mustDemo(t, f, second.ID).Journal) != n {
		t.Fatal("the wait was journaled twice")
	}
	clk.Set(t0.Add(90 * time.Minute))
	f.eng.DemoTick(ctx)
	if got := mustDemo(t, f, first.ID); got.Status != demos.Done || got.Cleanup != demos.CleanupSkipped {
		t.Fatalf("cleanup must yield to the next demo: %+v\n%s", got, journalText(got))
	}
	got := mustDemo(t, f, second.ID)
	if got.Status != demos.Preparing {
		t.Fatalf("second demo prep: %+v", got)
	}
	waitIdle(t, f, got.PrepRunID)
}
