package engine

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dboudreau00/greenroom/internal/config"
	"github.com/dboudreau00/greenroom/internal/demos"
	"github.com/dboudreau00/greenroom/internal/gaps"
)

func logDemo(t *testing.T, f *fixture, title, prospect string, start time.Time, mods ...func(r *DemoRequest)) *demos.Demo {
	t.Helper()
	req := DemoRequest{Env: "demo", Title: title, Prospect: prospect, Start: start, Duration: config.Duration(45 * time.Minute)}
	for _, m := range mods {
		m(&req)
	}
	d, err := f.eng.LogDemo(req, "alice")
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func newRef(kind, sev, title string) DebriefGapInput {
	return DebriefGapInput{New: &NewGap{Title: title, Kind: kind}, Severity: sev}
}

func linkRef(id, sev string) DebriefGapInput { return DebriefGapInput{GapID: id, Severity: sev} }

func lastEntry(d *demos.Demo) demos.Entry { return d.Journal[len(d.Journal)-1] }

func TestDebriefSaveAndEdit(t *testing.T) {
	f, _, t0 := demoFixture(t)
	d := logDemo(t, f, "Larkspur discovery", "Larkspur", t0.Add(-2*time.Hour))
	booked, err := f.eng.BookDemo(DemoRequest{Env: "demo", Title: "Later", Start: t0.Add(3 * time.Hour), Duration: config.Duration(30 * time.Minute)}, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.eng.SaveDebrief(booked.ID, DebriefRequest{Rating: 4}, "alice"); !errors.Is(err, demos.ErrState) {
		t.Fatalf("debrief of a scheduled demo: %v", err)
	}
	if _, err := f.eng.SaveDebrief("20261120-1500-0000", DebriefRequest{Rating: 4}, "alice"); !errors.Is(err, demos.ErrNotFound) {
		t.Fatalf("debrief of a missing demo: %v", err)
	}

	envGap := newRef("environment", "medium", "Orders seed has no EMEA customers")
	envGap.New.Env = "demo"
	envGap.Detail = "They sell in Germany and France"
	req := DebriefRequest{Rating: 4, Highlights: "Billing\r\nflow landed", Lowlights: "Search was slow", Outcome: "positive",
		Competitors: []string{"Quillmark", " quillmark ", "Tidewell", ""},
		Gaps:        []DebriefGapInput{newRef("feature", "high", "Scheduled exports"), envGap}}
	req.Gaps[0].Quote = "We export every Monday by hand."
	saved, err := f.eng.SaveDebrief(d.ID, req, "alice")
	if err != nil {
		t.Fatal(err)
	}
	db := saved.Debrief
	if db == nil || db.Rating != 4 || db.Highlights != "Billing\nflow landed" || strings.Join(db.Competitors, ",") != "Quillmark,Tidewell" || len(db.Gaps) != 2 {
		t.Fatalf("debrief %+v", db)
	}
	if saved.Outcome != "positive" || db.SubmittedBy != "alice" || !db.SubmittedAt.Equal(t0) {
		t.Fatalf("outcome %q, by %q at %s", saved.Outcome, db.SubmittedBy, db.SubmittedAt)
	}
	if e := lastEntry(saved); e.Kind != "system" || e.Text != "debrief submitted by alice: rating 4/5, 2 gaps (2 new)" {
		t.Fatalf("journal %+v", e)
	}
	list, _ := f.eng.Gaps().List()
	if len(list) != 2 {
		t.Fatalf("%d gaps", len(list))
	}
	exports := db.Gaps[0].GapID
	g, _ := f.eng.Gaps().Get(db.Gaps[1].GapID)
	if g.Kind != "environment" || g.Env != "demo" || g.Status != "open" || g.CreatedBy != "alice" {
		t.Fatalf("environment gap %+v", g)
	}

	bad := []struct {
		name string
		req  DebriefRequest
		want string
	}{
		{"no rating", DebriefRequest{}, "rating 0"},
		{"rating 6", DebriefRequest{Rating: 6}, "rating 6"},
		{"unknown gap", DebriefRequest{Rating: 3, Gaps: []DebriefGapInput{newRef("feature", "low", "Would be orphaned"), linkRef("gap-deadbeef", "low")}}, "does not exist"},
		{"path gap", DebriefRequest{Rating: 3, Gaps: []DebriefGapInput{linkRef("../../etc", "low")}}, "does not exist"},
		{"bad severity", DebriefRequest{Rating: 3, Gaps: []DebriefGapInput{linkRef(exports, "huge")}}, "severity"},
		{"no severity", DebriefRequest{Rating: 3, Gaps: []DebriefGapInput{linkRef(exports, "")}}, "severity"},
		{"twice", DebriefRequest{Rating: 3, Gaps: []DebriefGapInput{linkRef(exports, "low"), linkRef(exports, "high")}}, "listed twice"},
		{"both", DebriefRequest{Rating: 3, Gaps: []DebriefGapInput{{GapID: exports, New: &NewGap{Title: "x", Kind: "feature"}, Severity: "low"}}}, "not both"},
		{"neither", DebriefRequest{Rating: 3, Gaps: []DebriefGapInput{{Severity: "low"}}}, "link an existing gap"},
		{"bad kind", DebriefRequest{Rating: 3, Gaps: []DebriefGapInput{newRef("wish", "low", "x")}}, "kind"},
		{"no title", DebriefRequest{Rating: 3, Gaps: []DebriefGapInput{newRef("feature", "low", " \x00 ")}}, "title is required"},
		{"env on a feature", DebriefRequest{Rating: 3, Gaps: []DebriefGapInput{{New: &NewGap{Title: "x", Kind: "feature", Env: "demo"}, Severity: "low"}}}, "only apply"},
		{"unknown env", DebriefRequest{Rating: 3, Gaps: []DebriefGapInput{{New: &NewGap{Title: "x", Kind: "environment", Env: "nope"}, Severity: "low"}}}, "not found"},
		{"unknown module", DebriefRequest{Rating: 3, Gaps: []DebriefGapInput{{New: &NewGap{Title: "x", Kind: "environment", Env: "demo", Module: "billing"}, Severity: "low"}}}, "module"},
		{"bad outcome", DebriefRequest{Rating: 3, Outcome: "won"}, "outcome"},
		{"long text", DebriefRequest{Rating: 3, Lowlights: strings.Repeat("x", 4001)}, "limit is 4000"},
		{"long detail", DebriefRequest{Rating: 3, Gaps: []DebriefGapInput{{GapID: exports, Severity: "low", Detail: strings.Repeat("x", 1001)}}}, "limit is 1000"},
		{"competitors", DebriefRequest{Rating: 3, Competitors: strings.Split("a,b,c,d,e,f,g,h,i,j,k", ",")}, "at most 10"},
	}
	for _, c := range bad {
		if _, err := f.eng.SaveDebrief(d.ID, c.req, "bob"); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s: %v", c.name, err)
		}
	}
	if list, _ := f.eng.Gaps().List(); len(list) != 2 {
		t.Fatalf("a refused debrief created gaps: %d", len(list))
	}
	if cur := mustDemo(t, f, d.ID); cur.Debrief.Rating != 4 || len(cur.Journal) != len(saved.Journal) {
		t.Fatal("a refused debrief changed the demo")
	}

	edited, err := f.eng.SaveDebrief(d.ID, DebriefRequest{Rating: 3, Gaps: []DebriefGapInput{linkRef(exports, "blocker")}}, "bob")
	if err != nil {
		t.Fatal(err)
	}
	db = edited.Debrief
	if db.Rating != 3 || len(db.Gaps) != 1 || db.Gaps[0].Severity != "blocker" || db.Highlights != "" || len(db.Competitors) != 0 {
		t.Fatalf("edited %+v", db)
	}
	if db.SubmittedBy != "alice" || db.UpdatedBy != "bob" || db.Outcome != "positive" || edited.Outcome != "positive" {
		t.Fatalf("edit kept %q %q %q %q", db.SubmittedBy, db.UpdatedBy, db.Outcome, edited.Outcome)
	}
	if e := lastEntry(edited); e.Text != "debrief updated by bob: rating 3/5, 1 gap" {
		t.Fatalf("journal %q", e.Text)
	}
	o := "negative"
	if d2, err := f.eng.EditDemo(d.ID, DemoEdit{Outcome: &o}, "bob"); err != nil || d2.Debrief.Outcome != "negative" {
		t.Fatalf("outcome sync: %v %+v", err, d2.Debrief)
	}
	v, err := f.eng.GetDebrief(d.ID)
	if err != nil || v.Gaps[exports] == nil || v.Gaps[exports].Title != "Scheduled exports" || !v.Demo.Debriefed {
		t.Fatalf("view %v %+v", err, v)
	}
	if v, err := f.eng.GetDebrief(booked.ID); err != nil || v.Debrief != nil {
		t.Fatalf("view without a debrief: %v %+v", err, v)
	}
}

func TestGapScoresAndMerge(t *testing.T) {
	f, _, t0 := demoFixture(t)
	d1 := logDemo(t, f, "Larkspur intro", "Larkspur", t0.Add(-72*time.Hour))
	d2 := logDemo(t, f, "Larkspur deep dive", "larkspur", t0.Add(-48*time.Hour))
	d3 := logDemo(t, f, "Tidewell discovery", "Tidewell", t0.Add(-24*time.Hour))
	s1, err := f.eng.SaveDebrief(d1.ID, DebriefRequest{Rating: 4, Gaps: []DebriefGapInput{newRef("feature", "high", "Scheduled exports")}}, "alice")
	if err != nil {
		t.Fatal(err)
	}
	a := s1.Debrief.Gaps[0].GapID
	sugg, err := f.eng.SuggestGaps("Scheduled export of orders")
	if err != nil || len(sugg) != 1 || sugg[0].ID != a {
		t.Fatalf("suggest: %v %+v", err, sugg)
	}
	s2, err := f.eng.SaveDebrief(d2.ID, DebriefRequest{Rating: 2, Gaps: []DebriefGapInput{
		{GapID: a, Severity: "low", Detail: "Weekly would do"}, newRef("feature", "blocker", "Export orders on a schedule")}}, "alice")
	if err != nil {
		t.Fatal(err)
	}
	b := s2.Debrief.Gaps[1].GapID
	if _, err := f.eng.SaveDebrief(d3.ID, DebriefRequest{Rating: 5, Gaps: []DebriefGapInput{{GapID: b, Severity: "medium", Quote: "Every Friday"}}}, "alice"); err != nil {
		t.Fatal(err)
	}

	rows, err := f.eng.GapBoard(GapFilter{})
	if err != nil || len(rows) != 2 {
		t.Fatalf("board: %v %d", err, len(rows))
	}
	// B: Larkspur blocker 5 + Tidewell medium 2. A: Larkspur only, high 3 beats low 1.
	if rows[0].ID != b || rows[0].Score != 7 || rows[0].Prospects != 2 || rows[1].ID != a || rows[1].Score != 3 || rows[1].Prospects != 1 || rows[1].Mentions != 2 {
		t.Fatalf("scores %s %d %d / %s %d %d %d", rows[0].ID, rows[0].Score, rows[0].Prospects, rows[1].ID, rows[1].Score, rows[1].Prospects, rows[1].Mentions)
	}
	if rows[1].LastMentioned == nil || !rows[1].LastMentioned.Equal(d2.Start) || strings.Join(rows[1].Envs, ",") != "demo" || strings.Join(rows[1].Personas, ",") != "cfo" {
		t.Fatalf("row stats %+v", rows[1])
	}

	src, n, err := f.eng.MergeGap(b, a, "bob")
	if err != nil || n != 2 || src.MergedInto != a || src.Status != "wont-do" || !strings.Contains(src.Notes, "merged into "+a) {
		t.Fatalf("merge: %v %d %+v", err, n, src)
	}
	got := mustDemo(t, f, d2.ID).Debrief.Gaps
	if len(got) != 1 || got[0].GapID != a || got[0].Severity != "blocker" || got[0].Detail != "Weekly would do" {
		t.Fatalf("collapsed refs %+v", got)
	}
	if got := mustDemo(t, f, d3.ID).Debrief.Gaps; got[0].GapID != a || got[0].Severity != "medium" || got[0].Quote != "Every Friday" {
		t.Fatalf("rewritten ref %+v", got)
	}
	if !strings.Contains(journalText(mustDemo(t, f, d3.ID)), "debrief gap "+b+" merged into "+a+" by bob") {
		t.Fatal("no journal entry for the merge")
	}
	rows, _ = f.eng.GapBoard(GapFilter{})
	if len(rows) != 1 || rows[0].ID != a || rows[0].Score != 7 || rows[0].Mentions != 3 {
		t.Fatalf("after merge %+v", rows)
	}
	if rows, _ := f.eng.GapBoard(GapFilter{IncludeMerged: true}); len(rows) != 2 {
		t.Fatalf("include merged: %d", len(rows))
	}
	if rows, _ := f.eng.GapBoard(GapFilter{Status: "wont-do", IncludeMerged: true}); len(rows) != 1 || rows[0].ID != b {
		t.Fatal("status filter")
	}
	v, err := f.eng.GapDetail(a)
	if err != nil || len(v.Mentions) != 3 || strings.Join(v.MergedFrom, ",") != b || v.Mentions[0].Demo != d3.ID {
		t.Fatalf("detail %v %+v", err, v)
	}
	if tg, _ := f.eng.Gaps().Get(a); !strings.Contains(tg.Notes, b+" (Export orders on a schedule) merged in by bob") {
		t.Fatalf("target note %q", tg.Notes)
	}

	if _, _, err := f.eng.MergeGap(b, a, "bob"); !errors.Is(err, gaps.ErrState) {
		t.Fatalf("merge twice: %v", err)
	}
	if _, _, err := f.eng.MergeGap(a, b, "bob"); !errors.Is(err, gaps.ErrState) {
		t.Fatalf("merge into a merged gap: %v", err)
	}
	if _, _, err := f.eng.MergeGap(a, a, "bob"); err == nil {
		t.Fatal("merge into itself")
	}
	if _, _, err := f.eng.MergeGap(a, "gap-deadbeef", "bob"); err == nil || errors.Is(err, gaps.ErrNotFound) {
		t.Fatalf("merge into an unknown gap must be bad input: %v", err)
	}
	st := "planned"
	if _, err := f.eng.EditGap(b, GapEdit{Status: &st}, "bob"); !errors.Is(err, gaps.ErrState) {
		t.Fatalf("edit merged: %v", err)
	}
	// Linking the merged gap later lands on the gap that absorbed it.
	d4 := logDemo(t, f, "Quillmark intro", "Quillmark", t0.Add(-time.Hour))
	s4, err := f.eng.SaveDebrief(d4.ID, DebriefRequest{Rating: 3, Gaps: []DebriefGapInput{linkRef(b, "low")}}, "alice")
	if err != nil || s4.Debrief.Gaps[0].GapID != a {
		t.Fatalf("link merged: %v %+v", err, s4)
	}
}

func TestEditAndCreateGap(t *testing.T) {
	f, _, _ := demoFixture(t)
	g, err := f.eng.CreateGap(GapRequest{Title: "  Seed   data for EMEA ", Kind: "environment", Env: "demo", Notes: "from the\nQ4 calls"}, "alice")
	if err != nil || g.Title != "Seed data for EMEA" || g.Env != "demo" || g.Status != "open" {
		t.Fatalf("create %v %+v", err, g)
	}
	for _, req := range []GapRequest{{Title: "x", Kind: "nope"}, {Title: "x", Kind: "feature", Status: "closed"}, {Title: "", Kind: "feature"},
		{Title: "x", Kind: "environment", Module: "billing"}, {Title: "x", Kind: "environment", Env: "../demo"}} {
		if _, err := f.eng.CreateGap(req, "alice"); err == nil {
			t.Fatalf("created %+v", req)
		}
	}
	owner, status, kind := "Product: data team", "in-progress", "data"
	g, err = f.eng.EditGap(g.ID, GapEdit{Owner: &owner, Status: &status, Kind: &kind}, "bob")
	if err != nil || g.Kind != "data" || g.Env != "" || g.Status != "in-progress" || g.Owner != owner {
		t.Fatalf("edit %v %+v", err, g)
	}
	env := "demo"
	if _, err := f.eng.EditGap(g.ID, GapEdit{Env: &env}, "bob"); err == nil {
		t.Fatal("env on a data gap")
	}
	bad := "closed"
	if _, err := f.eng.EditGap(g.ID, GapEdit{Status: &bad}, "bob"); err == nil {
		t.Fatal("bad status")
	}
	if _, err := f.eng.EditGap("gap-deadbeef", GapEdit{Owner: &owner}, "bob"); !errors.Is(err, gaps.ErrNotFound) {
		t.Fatalf("missing gap: %v", err)
	}
	before := g.UpdatedAt
	if g2, err := f.eng.EditGap(g.ID, GapEdit{Owner: &owner}, "bob"); err != nil || !g2.UpdatedAt.Equal(before) {
		t.Fatalf("unchanged edit: %v", err)
	}
}

func TestDebriefInsights(t *testing.T) {
	f, _, t0 := demoFixture(t)
	old := logDemo(t, f, "Old demo", "Larkspur", t0.AddDate(0, -6, 0))
	pend := logDemo(t, f, "Waiting", "Tidewell", t0.Add(-5*24*time.Hour))
	logDemo(t, f, "Nobody came", "Quillmark", t0.Add(-4*24*time.Hour), func(r *DemoRequest) { r.Outcome = "no-show" })
	a := logDemo(t, f, "Larkspur deep dive", "Larkspur", t0.Add(-3*24*time.Hour))
	b := logDemo(t, f, "Tidewell exec", "Tidewell", t0.Add(-2*24*time.Hour), func(r *DemoRequest) { r.Persona = "slow" })
	if _, err := f.eng.BookDemo(DemoRequest{Env: "demo", Title: "Booked", Start: t0.Add(time.Hour), Duration: config.Duration(30 * time.Minute)}, "alice"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.eng.SaveDebrief(old.ID, DebriefRequest{Rating: 1, Competitors: []string{"Oldco"}}, "alice"); err != nil {
		t.Fatal(err)
	}
	envGap := newRef("environment", "high", "Login page shows the house logo")
	envGap.New.Env = "demo"
	sa, err := f.eng.SaveDebrief(a.ID, DebriefRequest{Rating: 5, Outcome: "positive", Competitors: []string{"Quillmark", "Brightloom"},
		Gaps: []DebriefGapInput{newRef("feature", "blocker", "SAML single sign-on"), envGap}}, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.eng.SaveDebrief(b.ID, DebriefRequest{Rating: 2, Outcome: "neutral", Competitors: []string{"quillmark"},
		Gaps: []DebriefGapInput{linkRef(sa.Debrief.Gaps[0].GapID, "medium"), linkRef(sa.Debrief.Gaps[1].GapID, "low")}}, "alice"); err != nil {
		t.Fatal(err)
	}

	since, _ := ParseSince("", t0)
	out, err := f.eng.Debriefs(since)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Pending) != 1 || out.Pending[0].ID != pend.ID {
		t.Fatalf("pending %+v", out.Pending)
	}
	if n, _ := f.eng.PendingDebriefs(since); n != 1 {
		t.Fatalf("pending count %d", n)
	}
	if len(out.Submitted) != 2 || out.Submitted[0].ID != b.ID || out.Submitted[0].Rating != 2 || out.Submitted[1].Gaps != 2 {
		t.Fatalf("submitted %+v", out.Submitted)
	}
	in := out.Insights
	if in.Pending != 1 || in.Submitted != 2 || in.AverageRating != 3.5 || in.Outcomes["positive"] != 1 || in.Outcomes["neutral"] != 1 || in.Mentions != 4 || in.GapsMentioned != 2 {
		t.Fatalf("insights %+v", in)
	}
	if len(in.RatingByPersona) != 2 || in.RatingByPersona[0].Persona != "cfo" || in.RatingByPersona[0].Average != 5 || in.RatingByPersona[1].Average != 2 {
		t.Fatalf("by persona %+v", in.RatingByPersona)
	}
	if len(in.Competitors) != 2 || in.Competitors[0].Name != "Quillmark" || in.Competitors[0].Count != 2 {
		t.Fatalf("competitors %+v", in.Competitors)
	}
	if in.GapsByKind["feature"] != 1 || in.GapsByKind["environment"] != 1 {
		t.Fatalf("by kind %+v", in.GapsByKind)
	}
	if len(in.EnvironmentGaps) != 1 || in.EnvironmentGaps[0] != (EnvGapCount{Env: "demo", Gaps: 1, Mentions: 2}) {
		t.Fatalf("environment gaps %+v", in.EnvironmentGaps)
	}
	if len(in.TopGaps) != 2 || in.TopGaps[0].Title != "SAML single sign-on" || in.TopGaps[0].Score != 7 {
		t.Fatalf("top gaps %+v", in.TopGaps)
	}

	all, err := f.eng.Debriefs(time.Time{})
	if err != nil || all.Insights.Submitted != 3 || all.Insights.Competitors[0].Name != "Quillmark" || len(all.Insights.Competitors) != 3 {
		t.Fatalf("all time: %v %+v", err, all.Insights)
	}

	for in, want := range map[string]time.Time{"30d": t0.AddDate(0, 0, -30), "720h": t0.Add(-720 * time.Hour), "2026-09-01": time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), "all": {}} {
		if got, err := ParseSince(in, t0); err != nil || !got.Equal(want) {
			t.Fatalf("since %s: %v %s", in, err, got)
		}
	}
	for _, bad := range []string{"0d", "-5h", "yesterday", "99999d"} {
		if _, err := ParseSince(bad, t0); err == nil {
			t.Fatalf("since %q accepted", bad)
		}
	}

	md, ctype, name, err := f.eng.ExportGaps("md", "")
	if err != nil || ctype != "text/markdown; charset=utf-8" || name != "greenroom-gaps-2026-11-20.md" || !strings.Contains(string(md), "SAML single sign-on") {
		t.Fatalf("md export: %v %s %s", err, ctype, name)
	}
	csvOut, _, name, err := f.eng.ExportGaps("csv", "open")
	if err != nil || name != "greenroom-gaps-2026-11-20.csv" || strings.Count(string(csvOut), "\r\n") != 3 {
		t.Fatalf("csv export: %v %s %q", err, name, csvOut)
	}
	if _, _, _, err := f.eng.ExportGaps("xlsx", ""); err == nil {
		t.Fatal("unknown format")
	}
	if _, _, _, err := f.eng.ExportGaps("md", "closed"); err == nil {
		t.Fatal("unknown status")
	}
}

// A new gap with the same normalized title and kind links to the existing
// one instead of creating a duplicate row on the board.
func TestNewGapWithSameTitleLinks(t *testing.T) {
	f, _, t0 := demoFixture(t)
	a := logDemo(t, f, "First", "Larkspur", t0.Add(-3*time.Hour))
	b := logDemo(t, f, "Second", "Tidewell", t0.Add(-2*time.Hour))
	first, err := f.eng.SaveDebrief(a.ID, DebriefRequest{Rating: 3, Gaps: []DebriefGapInput{newRef("feature", "high", "Scheduled exports")}}, "alice")
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.eng.SaveDebrief(b.ID, DebriefRequest{Rating: 4, Gaps: []DebriefGapInput{
		newRef("feature", "blocker", "  scheduled EXPORT "),
		newRef("integration", "low", "Scheduled exports"),
	}}, "bob")
	if err != nil {
		t.Fatal(err)
	}
	if second.Debrief.Gaps[0].GapID != first.Debrief.Gaps[0].GapID {
		t.Fatalf("same title and kind did not link: %s vs %s", second.Debrief.Gaps[0].GapID, first.Debrief.Gaps[0].GapID)
	}
	if second.Debrief.Gaps[1].GapID == first.Debrief.Gaps[0].GapID {
		t.Fatal("a different kind linked to the feature gap")
	}
	if e := lastEntry(second); !strings.Contains(e.Text, "(1 new)") {
		t.Fatalf("journal %q", e.Text)
	}
	list, _ := f.eng.Gaps().List()
	if len(list) != 2 {
		t.Fatalf("%d gaps, want 2", len(list))
	}
	if _, err := f.eng.SaveDebrief(b.ID, DebriefRequest{Rating: 4, Gaps: []DebriefGapInput{
		newRef("feature", "low", "Scheduled exports"), linkRef(first.Debrief.Gaps[0].GapID, "high"),
	}}, "bob"); err == nil || !strings.Contains(err.Error(), "listed twice") {
		t.Fatalf("same gap twice: %v", err)
	}
}

// Two new gaps with the same normalized title and kind in one debrief are
// one gap listed twice; nothing is created.
func TestNewGapListedTwiceInOneDebrief(t *testing.T) {
	f, _, t0 := demoFixture(t)
	d := logDemo(t, f, "Only", "Larkspur", t0.Add(-2*time.Hour))
	_, err := f.eng.SaveDebrief(d.ID, DebriefRequest{Rating: 3, Gaps: []DebriefGapInput{
		newRef("feature", "high", "Scheduled exports"),
		newRef("feature", "low", "  scheduled EXPORT "),
	}}, "alice")
	if err == nil || !strings.Contains(err.Error(), "gap 2") || !strings.Contains(err.Error(), "listed twice") {
		t.Fatalf("same new gap twice: %v", err)
	}
	if list, _ := f.eng.Gaps().List(); len(list) != 0 {
		t.Fatalf("%d gaps created by a refused debrief", len(list))
	}
	saved, err := f.eng.SaveDebrief(d.ID, DebriefRequest{Rating: 3, Gaps: []DebriefGapInput{
		newRef("feature", "high", "Scheduled exports"),
		newRef("integration", "low", "Scheduled exports"),
	}}, "alice")
	if err != nil || len(saved.Debrief.Gaps) != 2 || saved.Debrief.Gaps[0].GapID == saved.Debrief.Gaps[1].GapID {
		t.Fatalf("same title, different kind: %v", err)
	}
}
