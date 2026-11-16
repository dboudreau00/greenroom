package engine

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dboudreau00/greenroom/internal/config"
	"github.com/dboudreau00/greenroom/internal/demos"
	"github.com/dboudreau00/greenroom/internal/gaps"
)

// DefaultDebriefWindow is how far back the debrief queue and insights look
// when no since is given.
const DefaultDebriefWindow = 90 * 24 * time.Hour

// DebriefRequest writes the whole debrief of a demo. Each gap either links
// an existing gap by id or describes a new one, which is created first.
type DebriefRequest struct {
	Rating           int               `json:"rating"`
	Highlights       string            `json:"highlights"`
	Lowlights        string            `json:"lowlights"`
	EnvironmentNotes string            `json:"environmentNotes"`
	Competitors      []string          `json:"competitors"`
	NextSteps        string            `json:"nextSteps"`
	Outcome          string            `json:"outcome"`
	Gaps             []DebriefGapInput `json:"gaps"`
}

// DebriefGapInput is one gap of a debrief request.
type DebriefGapInput struct {
	GapID    string  `json:"gapId"`
	New      *NewGap `json:"new"`
	Severity string  `json:"severity"`
	Detail   string  `json:"detail"`
	Quote    string  `json:"quote"`
}

// NewGap describes a gap to create while saving a debrief.
type NewGap struct {
	Title  string `json:"title"`
	Kind   string `json:"kind"`
	Env    string `json:"env"`
	Module string `json:"module"`
}

// GapRequest creates a gap directly.
type GapRequest struct {
	Title  string `json:"title"`
	Kind   string `json:"kind"`
	Status string `json:"status"`
	Owner  string `json:"owner"`
	Notes  string `json:"notes"`
	Env    string `json:"env"`
	Module string `json:"module"`
}

// GapEdit changes a gap. Absent fields are left alone.
type GapEdit struct {
	Title  *string `json:"title"`
	Kind   *string `json:"kind"`
	Status *string `json:"status"`
	Owner  *string `json:"owner"`
	Notes  *string `json:"notes"`
	Env    *string `json:"env"`
	Module *string `json:"module"`
}

// GapFilter selects board rows. Status "active" means open, planned or
// in-progress.
type GapFilter struct {
	Status        string
	Kind          string
	Env           string
	IncludeMerged bool
}

// GapView is one gap with its stats, every mention and the gaps merged
// into it.
type GapView struct {
	Gap        gaps.Row       `json:"gap"`
	Mentions   []gaps.Mention `json:"mentions"`
	MergedFrom []string       `json:"mergedFrom"`
}

// DebriefView is a demo's debrief with the gaps it references, for the
// debrief form and the demo page.
type DebriefView struct {
	Demo    demos.Brief          `json:"demo"`
	Debrief *demos.Debrief       `json:"debrief"`
	Gaps    map[string]*gaps.Gap `json:"gaps"`
}

// DebriefBrief is a submitted debrief in the list.
type DebriefBrief struct {
	demos.Brief
	Rating      int       `json:"rating"`
	Gaps        int       `json:"gaps"`
	SubmittedBy string    `json:"submittedBy,omitempty"`
	SubmittedAt time.Time `json:"submittedAt"`
}

// RatingGroup is the average rating of one persona's demos.
type RatingGroup struct {
	Persona string  `json:"persona"`
	Average float64 `json:"average"`
	Count   int     `json:"count"`
}

// EnvGapCount counts environment gaps of one environment.
type EnvGapCount struct {
	Env      string `json:"env"`
	Gaps     int    `json:"gaps"`
	Mentions int    `json:"mentions"`
}

// NameCount is a name and how often it came up.
type NameCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// Insights roll up the debriefs of demos that started inside the window.
type Insights struct {
	Since           time.Time      `json:"since"`
	Pending         int            `json:"pending"`
	Submitted       int            `json:"submitted"`
	AverageRating   float64        `json:"averageRating"`
	RatingByPersona []RatingGroup  `json:"ratingByPersona"`
	Outcomes        map[string]int `json:"outcomes"`
	Mentions        int            `json:"mentions"`
	GapsMentioned   int            `json:"gapsMentioned"`
	TopGaps         []gaps.Row     `json:"topGaps"`
	GapsByKind      map[string]int `json:"gapsByKind"`
	EnvironmentGaps []EnvGapCount  `json:"environmentGaps"`
	Competitors     []NameCount    `json:"competitors"`
}

// DebriefOverview is the debrief portal's data: the queue of demos waiting
// for a debrief (oldest first), the submitted debriefs (newest first) and
// the insights.
type DebriefOverview struct {
	Since     time.Time      `json:"since"`
	Pending   []demos.Brief  `json:"pending"`
	Submitted []DebriefBrief `json:"submitted"`
	Insights  Insights       `json:"insights"`
}

// Gaps returns the gap registry.
func (e *Engine) Gaps() *gaps.Store { return e.gaps }

var daysRe = regexp.MustCompile(`^([0-9]{1,4})d$`)

// ParseSince reads a window start: empty for the default 90 days, "all",
// a number of days ("30d"), a Go duration ("720h"), a date or RFC 3339.
func ParseSince(s string, now time.Time) (time.Time, error) {
	s = strings.TrimSpace(s)
	const most = 3650 * 24 * time.Hour
	switch {
	case s == "":
		return now.Add(-DefaultDebriefWindow).UTC(), nil
	case s == "all":
		return time.Time{}, nil
	}
	if m := daysRe.FindStringSubmatch(s); m != nil {
		n, _ := strconv.Atoi(m[1])
		if n < 1 || time.Duration(n)*24*time.Hour > most {
			return time.Time{}, fmt.Errorf("since %q: use 1d to 3650d", s)
		}
		return now.AddDate(0, 0, -n).UTC(), nil
	}
	if d, err := time.ParseDuration(s); err == nil {
		if d <= 0 || d > most {
			return time.Time{}, fmt.Errorf("since %q: use a positive window of at most 3650d", s)
		}
		return now.Add(-d).UTC(), nil
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t.UTC(), nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC(), nil
	}
	return time.Time{}, fmt.Errorf("since %q: use 30d, 720h, all, a date (2026-09-01) or RFC 3339", s)
}

func debriefable(d *demos.Demo) error {
	if d.Status != demos.Done {
		return fmt.Errorf("%w: demo %s is %s; a debrief opens once it is done", demos.ErrState, d.ID, d.Status)
	}
	return nil
}

// cleanText cleans a multi-line field and refuses it over max characters.
func cleanText(name, s string, max int) (string, error) {
	s = demos.CleanText(s)
	if n := len([]rune(s)); n > max {
		return "", fmt.Errorf("%s is %d characters; the limit is %d", name, n, max)
	}
	return s, nil
}

func cleanCompetitors(in []string) ([]string, error) {
	out, seen := []string{}, map[string]bool{}
	for _, c := range in {
		c = demos.CleanLine(c, demos.MaxCompetitor)
		k := strings.ToLower(c)
		if c == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, c)
	}
	if len(out) > demos.MaxCompetitors {
		return nil, fmt.Errorf("at most %d competitors", demos.MaxCompetitors)
	}
	return out, nil
}

func cleanDebrief(req DebriefRequest) (*demos.Debrief, error) {
	if req.Rating < 1 || req.Rating > 5 {
		return nil, fmt.Errorf("rating %d: use 1 to 5", req.Rating)
	}
	db := &demos.Debrief{Rating: req.Rating}
	var err error
	for _, f := range []struct {
		name string
		in   string
		out  *string
	}{{"highlights", req.Highlights, &db.Highlights}, {"lowlights", req.Lowlights, &db.Lowlights},
		{"environment notes", req.EnvironmentNotes, &db.EnvironmentNotes}, {"next steps", req.NextSteps, &db.NextSteps}} {
		if *f.out, err = cleanText(f.name, f.in, demos.MaxDebriefText); err != nil {
			return nil, err
		}
	}
	if db.Competitors, err = cleanCompetitors(req.Competitors); err != nil {
		return nil, err
	}
	db.Outcome = strings.TrimSpace(req.Outcome)
	if db.Outcome != "" && !demos.OneOf(db.Outcome, demos.Outcomes) {
		return nil, fmt.Errorf("outcome %q: use one of %s", db.Outcome, strings.Join(demos.Outcomes, ", "))
	}
	if len(req.Gaps) > demos.MaxGapRefs {
		return nil, fmt.Errorf("at most %d gaps per debrief", demos.MaxGapRefs)
	}
	return db, nil
}

// gapPlace checks where an environment gap points. Only environment gaps
// carry an environment, which must exist, and a module, which must be one
// of that environment's template modules.
func (e *Engine) gapPlace(kind, env, module string) (string, string, error) {
	env, module = strings.TrimSpace(env), strings.TrimSpace(module)
	if kind != gaps.KindEnvironment {
		if env != "" || module != "" {
			return "", "", errors.New("env and module only apply to gaps of kind environment")
		}
		return "", "", nil
	}
	if env == "" {
		if module != "" {
			return "", "", errors.New("a module needs its environment")
		}
		return "", "", nil
	}
	if !config.ValidName(env) {
		return "", "", fmt.Errorf("environment name %q is not valid", env)
	}
	v, err := e.Env(env)
	if err != nil {
		return "", "", err
	}
	if module == "" {
		return env, "", nil
	}
	var mods []string
	if v.Config.Template != nil {
		mods = v.Config.Template.Modules
	}
	if !demos.OneOf(module, mods) {
		if len(mods) == 0 {
			return "", "", fmt.Errorf("module %q: environment %s was not built from a template with modules", module, env)
		}
		return "", "", fmt.Errorf("module %q: environment %s has %s", module, env, strings.Join(mods, ", "))
	}
	return env, module, nil
}

// newGap validates a gap to create; it does not write it.
func (e *Engine) newGap(req GapRequest, actor string, now time.Time) (*gaps.Gap, error) {
	g := &gaps.Gap{Title: demos.CleanLine(req.Title, gaps.MaxTitle), Kind: strings.TrimSpace(req.Kind), Status: strings.TrimSpace(req.Status),
		Owner: demos.CleanLine(req.Owner, gaps.MaxOwner), CreatedAt: now, UpdatedAt: now, CreatedBy: actor}
	if g.Title == "" {
		return nil, errors.New("title is required")
	}
	if !demos.OneOf(g.Kind, gaps.Kinds) {
		return nil, fmt.Errorf("kind %q: use one of %s", g.Kind, strings.Join(gaps.Kinds, ", "))
	}
	if g.Status == "" {
		g.Status = gaps.StatusOpen
	}
	if !demos.OneOf(g.Status, gaps.Statuses) {
		return nil, fmt.Errorf("status %q: use one of %s", g.Status, strings.Join(gaps.Statuses, ", "))
	}
	var err error
	if g.Notes, err = cleanText("notes", req.Notes, gaps.MaxNotes); err != nil {
		return nil, err
	}
	if g.Env, g.Module, err = e.gapPlace(g.Kind, req.Env, req.Module); err != nil {
		return nil, err
	}
	return g, nil
}

// sameGap returns the id of an unmerged gap with the same kind and the same
// title once normalized (case, spacing, stop words, plural s), or "".
func sameGap(all map[string]*gaps.Gap, title, kind string) string {
	want := strings.Join(gaps.Tokens(title), " ")
	if want == "" {
		return ""
	}
	best := ""
	for id, g := range all {
		if g.MergedInto != "" || g.Kind != strings.TrimSpace(kind) {
			continue
		}
		if strings.Join(gaps.Tokens(g.Title), " ") == want && (best == "" || id < best) {
			best = id
		}
	}
	return best
}

// SaveDebrief writes the debrief of a demo that is done. New gaps are
// created in the registry first; an unknown gap id fails the whole save,
// and gaps created for a save that then fails are removed again. The
// demo's outcome follows the debrief's, and a system journal entry records
// who submitted or updated it.
func (e *Engine) SaveDebrief(id string, req DebriefRequest, actor string) (*demos.Demo, error) {
	now := e.Now().UTC()
	cur, err := e.demos.Get(id)
	if err != nil {
		return nil, err
	}
	if err := debriefable(cur); err != nil {
		return nil, err
	}
	db, err := cleanDebrief(req)
	if err != nil {
		return nil, err
	}
	all, err := e.gaps.Map()
	if err != nil {
		return nil, err
	}
	type pending struct {
		at int
		g  *gaps.Gap
	}
	var news []pending
	refs := []demos.GapRef{}
	seen := map[string]bool{}
	newSeen := map[string]int{} // kind and normalized title of new gaps -> gap number
	for i, in := range req.Gaps {
		n := i + 1
		sev := strings.TrimSpace(in.Severity)
		if !demos.OneOf(sev, gaps.Severities) {
			return nil, fmt.Errorf("gap %d: severity %q: use low, medium, high or blocker", n, sev)
		}
		ref := demos.GapRef{Severity: sev}
		if ref.Detail, err = cleanText(fmt.Sprintf("gap %d detail", n), in.Detail, demos.MaxGapDetail); err != nil {
			return nil, err
		}
		if ref.Quote, err = cleanText(fmt.Sprintf("gap %d quote", n), in.Quote, demos.MaxGapDetail); err != nil {
			return nil, err
		}
		gid := strings.TrimSpace(in.GapID)
		switch {
		case gid != "" && in.New != nil:
			return nil, fmt.Errorf("gap %d: give gapId or new, not both", n)
		case gid != "":
			if _, ok := all[gid]; !ok || !gaps.ValidID(gid) {
				return nil, fmt.Errorf("gap %d: gap %q does not exist", n, gid)
			}
			gid = gaps.Resolve(all, gid)
			if seen[gid] {
				return nil, fmt.Errorf("gap %d: gap %s is listed twice", n, gid)
			}
			seen[gid] = true
			ref.GapID = gid
		case in.New != nil:
			// The same ask under the same title and kind links to the gap
			// that already exists instead of splitting it across two rows.
			if same := sameGap(all, in.New.Title, in.New.Kind); same != "" {
				if seen[same] {
					return nil, fmt.Errorf("gap %d: gap %s is listed twice", n, same)
				}
				seen[same] = true
				ref.GapID = same
				break
			}
			g, err := e.newGap(GapRequest{Title: in.New.Title, Kind: in.New.Kind, Env: in.New.Env, Module: in.New.Module}, actor, now)
			if err != nil {
				return nil, fmt.Errorf("gap %d: %v", n, err)
			}
			if key := strings.Join(gaps.Tokens(g.Title), " "); key != "" {
				key = g.Kind + "\x00" + key
				if first, ok := newSeen[key]; ok {
					return nil, fmt.Errorf("gap %d: gap %q is listed twice (as gap %d)", n, g.Title, first)
				}
				newSeen[key] = n
			}
			news = append(news, pending{len(refs), g})
		default:
			return nil, fmt.Errorf("gap %d: link an existing gap (gapId) or describe a new one (new)", n)
		}
		refs = append(refs, ref)
	}
	var created []string
	rollback := func() {
		for _, gid := range created {
			if err := e.gaps.Delete(gid); err != nil {
				e.log.Warn("remove gap after a failed debrief", "gap", gid, "err", err)
			}
		}
	}
	for _, p := range news {
		if err := e.gaps.Create(p.g); err != nil {
			rollback()
			return nil, err
		}
		created = append(created, p.g.ID)
		refs[p.at].GapID = p.g.ID
	}
	db.Gaps = refs
	d, err := e.demos.Update(id, func(d *demos.Demo, _ func() ([]*demos.Demo, error)) error {
		if err := debriefable(d); err != nil {
			return err
		}
		verb := "submitted"
		db.SubmittedBy, db.SubmittedAt = actor, now
		if d.Debrief != nil {
			verb = "updated"
			db.SubmittedBy, db.SubmittedAt = d.Debrief.SubmittedBy, d.Debrief.SubmittedAt
		}
		db.UpdatedBy, db.UpdatedAt = actor, now
		if db.Outcome == "" {
			db.Outcome = d.Outcome
		}
		d.Outcome = db.Outcome
		d.Debrief = db
		d.UpdatedAt = now
		text := fmt.Sprintf("debrief %s by %s: rating %d/5, %s", verb, actor, db.Rating, plural(len(db.Gaps), "gap", "gaps"))
		if len(created) > 0 {
			text += fmt.Sprintf(" (%d new)", len(created))
		}
		d.AddEntry(demos.Entry{At: now, Author: systemAuthor, Kind: "system", Text: text})
		return nil
	})
	if err != nil {
		rollback()
		return nil, err
	}
	return d, nil
}

// GetDebrief returns a demo's debrief, nil when none was written yet, with
// the gaps it references.
func (e *Engine) GetDebrief(id string) (*DebriefView, error) {
	d, err := e.demos.Get(id)
	if err != nil {
		return nil, err
	}
	v := &DebriefView{Demo: d.Brief(), Debrief: d.Debrief, Gaps: map[string]*gaps.Gap{}}
	if d.Debrief == nil {
		return v, nil
	}
	all, err := e.gaps.Map()
	if err != nil {
		return nil, err
	}
	for _, r := range d.Debrief.Gaps {
		if g, ok := all[r.GapID]; ok {
			v.Gaps[r.GapID] = g
		}
		if to := gaps.Resolve(all, r.GapID); to != r.GapID {
			if g, ok := all[to]; ok {
				v.Gaps[to] = g
			}
		}
	}
	return v, nil
}

// mentions derives every gap mention from the debriefs of demos that
// started at or after since (zero for all), newest first. References follow
// merges to the gap that absorbed them; a demo that names the same gap
// twice counts once, at its highest severity.
func mentions(list []*demos.Demo, all map[string]*gaps.Gap, since time.Time) []gaps.Mention {
	out := []gaps.Mention{}
	for _, d := range list {
		if d.Debrief == nil || d.Start.Before(since) {
			continue
		}
		at := map[string]int{}
		for _, r := range d.Debrief.Gaps {
			id := gaps.Resolve(all, r.GapID)
			if _, ok := all[id]; !ok {
				continue
			}
			m := gaps.Mention{GapID: id, Demo: d.ID, Title: d.Title, Prospect: d.Prospect, Brand: d.Brand, Env: d.Env, Persona: d.Persona,
				Start: d.Start, Severity: r.Severity, Detail: r.Detail, Quote: r.Quote}
			if i, ok := at[id]; ok {
				if gaps.Weight(m.Severity) > gaps.Weight(out[i].Severity) {
					out[i] = m
				}
				continue
			}
			at[id] = len(out)
			out = append(out, m)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Start.After(out[j].Start) })
	return out
}

// board loads the gaps, the demos and the mentions since a time.
func (e *Engine) board(since time.Time) ([]*gaps.Gap, map[string]*gaps.Gap, []*demos.Demo, []gaps.Mention, error) {
	list, err := e.gaps.List()
	if err != nil {
		return nil, nil, nil, nil, err
	}
	all := make(map[string]*gaps.Gap, len(list))
	for _, g := range list {
		all[g.ID] = g
	}
	ds, err := e.demos.List()
	if err != nil {
		return nil, nil, nil, nil, err
	}
	return list, all, ds, mentions(ds, all, since), nil
}

func (f GapFilter) check() error {
	if f.Status != "" && f.Status != "active" && !demos.OneOf(f.Status, gaps.Statuses) {
		return fmt.Errorf("status %q: use active or one of %s", f.Status, strings.Join(gaps.Statuses, ", "))
	}
	if f.Kind != "" && !demos.OneOf(f.Kind, gaps.Kinds) {
		return fmt.Errorf("kind %q: use one of %s", f.Kind, strings.Join(gaps.Kinds, ", "))
	}
	if f.Env != "" && !config.ValidName(f.Env) {
		return fmt.Errorf("environment name %q is not valid", f.Env)
	}
	return nil
}

func (f GapFilter) keep(r gaps.Row) bool {
	switch {
	case r.MergedInto != "" && !f.IncludeMerged:
		return false
	case f.Status == "active" && !gaps.Active(r.Status):
		return false
	case f.Status != "" && f.Status != "active" && r.Status != f.Status:
		return false
	case f.Kind != "" && r.Kind != f.Kind:
		return false
	case f.Env != "" && r.Gap.Env != f.Env && !demos.OneOf(f.Env, r.Envs):
		return false
	}
	return true
}

// GapBoard returns the board: every gap the filter keeps with its derived
// stats, highest score first. Merged gaps are left out unless asked for.
func (e *Engine) GapBoard(f GapFilter) ([]gaps.Row, error) {
	rows, _, err := e.gapRows(f)
	return rows, err
}

func (e *Engine) gapRows(f GapFilter) ([]gaps.Row, map[string][]gaps.Mention, error) {
	if err := f.check(); err != nil {
		return nil, nil, err
	}
	list, _, _, ms, err := e.board(time.Time{})
	if err != nil {
		return nil, nil, err
	}
	out := []gaps.Row{}
	for _, r := range gaps.Rows(list, ms) {
		if f.keep(r) {
			out = append(out, r)
		}
	}
	return out, gaps.ByGap(ms), nil
}

// GapDetail returns one gap with its stats and mentions.
func (e *Engine) GapDetail(id string) (*GapView, error) {
	g, err := e.gaps.Get(id)
	if err != nil {
		return nil, err
	}
	list, _, _, ms, err := e.board(time.Time{})
	if err != nil {
		return nil, err
	}
	mine := gaps.ByGap(ms)[g.ID]
	if mine == nil {
		mine = []gaps.Mention{}
	}
	v := &GapView{Gap: gaps.Stats(g, mine), Mentions: mine, MergedFrom: []string{}}
	for _, o := range list {
		if o.MergedInto == g.ID {
			v.MergedFrom = append(v.MergedFrom, o.ID)
		}
	}
	return v, nil
}

// CreateGap adds a gap to the registry without a debrief.
func (e *Engine) CreateGap(req GapRequest, actor string) (*gaps.Gap, error) {
	g, err := e.newGap(req, actor, e.Now().UTC())
	if err != nil {
		return nil, err
	}
	if err := e.gaps.Create(g); err != nil {
		return nil, err
	}
	return g, nil
}

// EditGap changes a gap. A merged gap can not change; a gap that stops
// being an environment gap drops its environment and module.
func (e *Engine) EditGap(id string, ed GapEdit, actor string) (*gaps.Gap, error) {
	now := e.Now().UTC()
	return e.gaps.Update(id, func(g *gaps.Gap) error {
		if g.MergedInto != "" {
			return fmt.Errorf("%w: gap %s was merged into %s", gaps.ErrState, g.ID, g.MergedInto)
		}
		changed := false
		if ed.Title != nil {
			t := demos.CleanLine(*ed.Title, gaps.MaxTitle)
			if t == "" {
				return errors.New("title can not be empty")
			}
			changed = changed || t != g.Title
			g.Title = t
		}
		if ed.Status != nil {
			s := strings.TrimSpace(*ed.Status)
			if !demos.OneOf(s, gaps.Statuses) {
				return fmt.Errorf("status %q: use one of %s", s, strings.Join(gaps.Statuses, ", "))
			}
			changed = changed || s != g.Status
			g.Status = s
		}
		if ed.Owner != nil {
			o := demos.CleanLine(*ed.Owner, gaps.MaxOwner)
			changed = changed || o != g.Owner
			g.Owner = o
		}
		if ed.Notes != nil {
			n, err := cleanText("notes", *ed.Notes, gaps.MaxNotes)
			if err != nil {
				return err
			}
			changed = changed || n != g.Notes
			g.Notes = n
		}
		kind, env, module := g.Kind, g.Env, g.Module
		if ed.Kind != nil {
			kind = strings.TrimSpace(*ed.Kind)
			if !demos.OneOf(kind, gaps.Kinds) {
				return fmt.Errorf("kind %q: use one of %s", kind, strings.Join(gaps.Kinds, ", "))
			}
		}
		if ed.Env != nil {
			env = *ed.Env
		}
		if ed.Module != nil {
			module = *ed.Module
		}
		if kind != gaps.KindEnvironment && ed.Env == nil && ed.Module == nil {
			env, module = "", ""
		}
		if kind != g.Kind || ed.Env != nil || ed.Module != nil {
			var err error
			if env, module, err = e.gapPlace(kind, env, module); err != nil {
				return err
			}
		}
		changed = changed || kind != g.Kind || env != g.Env || module != g.Module
		g.Kind, g.Env, g.Module = kind, env, module
		if !changed {
			return gaps.ErrUnchanged
		}
		g.UpdatedAt = now
		return nil
	})
}

// MergeGap folds gap id into gap into: the source is marked merged and
// wont-do with a note, the target gets a note, and every debrief that
// names the source is rewritten to name the target, keeping the highest
// severity where a debrief names both. It returns the source and the
// number of demos rewritten.
func (e *Engine) MergeGap(id, into, actor string) (*gaps.Gap, int, error) {
	now := e.Now().UTC()
	into = strings.TrimSpace(into)
	if id == into {
		return nil, 0, errors.New("a gap can not be merged into itself")
	}
	if _, err := e.gaps.Get(id); err != nil {
		return nil, 0, err
	}
	target, err := e.gaps.Get(into)
	if err != nil {
		return nil, 0, fmt.Errorf("merge into %q: %v", into, err)
	}
	if target.MergedInto != "" {
		return nil, 0, fmt.Errorf("%w: gap %s was merged into %s; merge into that one", gaps.ErrState, target.ID, target.MergedInto)
	}
	day := now.Format("2006-01-02")
	src, err := e.gaps.Update(id, func(g *gaps.Gap) error {
		if g.MergedInto != "" {
			return fmt.Errorf("%w: gap %s was already merged into %s", gaps.ErrState, g.ID, g.MergedInto)
		}
		g.MergedInto, g.Status, g.UpdatedAt = target.ID, gaps.StatusWontDo, now
		g.Notes = gaps.AppendNote(g.Notes, fmt.Sprintf("%s: merged into %s (%s) by %s", day, target.ID, target.Title, actor))
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	if _, err := e.gaps.Update(target.ID, func(g *gaps.Gap) error {
		g.Notes = gaps.AppendNote(g.Notes, fmt.Sprintf("%s: %s (%s) merged in by %s", day, src.ID, src.Title, actor))
		g.UpdatedAt = now
		return nil
	}); err != nil {
		return src, 0, err
	}
	list, err := e.demos.List()
	if err != nil {
		return src, 0, err
	}
	n := 0
	for _, d := range list {
		if d.Debrief == nil || !namesGap(d.Debrief, src.ID) {
			continue
		}
		_, err := e.demos.Update(d.ID, func(d *demos.Demo, _ func() ([]*demos.Demo, error)) error {
			if d.Debrief == nil || !rewriteRefs(d.Debrief, src.ID, target.ID) {
				return demos.ErrUnchanged
			}
			d.UpdatedAt = now
			d.AddEntry(demos.Entry{At: now, Author: systemAuthor, Kind: "system", Text: fmt.Sprintf("debrief gap %s merged into %s by %s", src.ID, target.ID, actor)})
			return nil
		})
		if err != nil {
			return src, n, fmt.Errorf("rewrite the debrief of demo %s: %v", d.ID, err)
		}
		n++
	}
	return src, n, nil
}

func namesGap(db *demos.Debrief, id string) bool {
	for _, r := range db.Gaps {
		if r.GapID == id {
			return true
		}
	}
	return false
}

// rewriteRefs points references to from at to. When the debrief already
// names to, the two collapse into the one with the higher severity, which
// borrows the other's detail or quote when it has none.
func rewriteRefs(db *demos.Debrief, from, to string) bool {
	out := make([]demos.GapRef, 0, len(db.Gaps))
	changed, at := false, -1
	for _, r := range db.Gaps {
		if r.GapID == from {
			r.GapID, changed = to, true
		}
		if r.GapID == to {
			if at >= 0 {
				keep, other := out[at], r
				if gaps.Weight(r.Severity) > gaps.Weight(keep.Severity) {
					keep, other = r, out[at]
				}
				if keep.Detail == "" {
					keep.Detail = other.Detail
				}
				if keep.Quote == "" {
					keep.Quote = other.Quote
				}
				out[at] = keep
				continue
			}
			at = len(out)
		}
		out = append(out, r)
	}
	db.Gaps = out
	return changed
}

// SuggestGaps returns active gaps whose titles look like q, so people link
// an existing gap instead of writing a duplicate.
func (e *Engine) SuggestGaps(q string) ([]gaps.Suggestion, error) {
	list, err := e.gaps.List()
	if err != nil {
		return nil, err
	}
	return gaps.Suggest(list, demos.CleanLine(q, 200), 5), nil
}

func pendingDebrief(d *demos.Demo, since time.Time) bool {
	return d.Status == demos.Done && d.Debrief == nil && d.Outcome != "no-show" && !d.Start.Before(since)
}

// PendingDebriefs counts demos done since a time that have no debrief.
func (e *Engine) PendingDebriefs(since time.Time) (int, error) {
	list, err := e.demos.List()
	if err != nil {
		return 0, err
	}
	n := 0
	for _, d := range list {
		if pendingDebrief(d, since) {
			n++
		}
	}
	return n, nil
}

// Debriefs returns the queue, the submitted debriefs and the insights for
// demos that started at or after since.
func (e *Engine) Debriefs(since time.Time) (*DebriefOverview, error) {
	list, all, ds, ms, err := e.board(since)
	if err != nil {
		return nil, err
	}
	out := &DebriefOverview{Since: since, Pending: []demos.Brief{}, Submitted: []DebriefBrief{}}
	in := Insights{Since: since, RatingByPersona: []RatingGroup{}, Outcomes: map[string]int{}, TopGaps: []gaps.Row{}, GapsByKind: map[string]int{},
		EnvironmentGaps: []EnvGapCount{}, Competitors: []NameCount{}}
	ratingSum := 0
	persona := map[string]*RatingGroup{}
	personaSum := map[string]int{}
	comp := map[string]*NameCount{}
	for _, d := range ds {
		if pendingDebrief(d, since) {
			out.Pending = append(out.Pending, d.Brief())
		}
		if d.Debrief == nil || d.Start.Before(since) {
			continue
		}
		db := d.Debrief
		out.Submitted = append(out.Submitted, DebriefBrief{Brief: d.Brief(), Rating: db.Rating, Gaps: len(db.Gaps), SubmittedBy: db.SubmittedBy, SubmittedAt: db.SubmittedAt})
		ratingSum += db.Rating
		p := firstNonEmpty(d.Persona, "none")
		if persona[p] == nil {
			persona[p] = &RatingGroup{Persona: p}
		}
		persona[p].Count++
		personaSum[p] += db.Rating
		if db.Outcome != "" {
			in.Outcomes[db.Outcome]++
		}
		for _, c := range db.Competitors {
			k := strings.ToLower(c)
			if comp[k] == nil {
				comp[k] = &NameCount{Name: c}
			}
			comp[k].Count++
		}
	}
	sort.SliceStable(out.Submitted, func(i, j int) bool {
		a, b := out.Submitted[i], out.Submitted[j]
		if !a.SubmittedAt.Equal(b.SubmittedAt) {
			return a.SubmittedAt.After(b.SubmittedAt)
		}
		return a.Start.After(b.Start)
	})
	in.Pending, in.Submitted, in.Mentions = len(out.Pending), len(out.Submitted), len(ms)
	if in.Submitted > 0 {
		in.AverageRating = round2(float64(ratingSum) / float64(in.Submitted))
	}
	for p, g := range persona {
		g.Average = round2(float64(personaSum[p]) / float64(g.Count))
		in.RatingByPersona = append(in.RatingByPersona, *g)
	}
	sort.Slice(in.RatingByPersona, func(i, j int) bool {
		a, b := in.RatingByPersona[i], in.RatingByPersona[j]
		if a.Count != b.Count {
			return a.Count > b.Count
		}
		return a.Persona < b.Persona
	})
	for _, c := range comp {
		in.Competitors = append(in.Competitors, *c)
	}
	sortCounts(in.Competitors)
	envGaps := map[string]*EnvGapCount{}
	envSeen := map[string]bool{}
	mentioned := map[string]bool{}
	for _, m := range ms {
		g := all[m.GapID]
		if !mentioned[g.ID] {
			mentioned[g.ID] = true
			in.GapsByKind[g.Kind]++
		}
		if g.Kind != gaps.KindEnvironment {
			continue
		}
		env := firstNonEmpty(g.Env, m.Env)
		if envGaps[env] == nil {
			envGaps[env] = &EnvGapCount{Env: env}
		}
		envGaps[env].Mentions++
		if k := env + "\x00" + g.ID; !envSeen[k] {
			envSeen[k] = true
			envGaps[env].Gaps++
		}
	}
	in.GapsMentioned = len(mentioned)
	for _, c := range envGaps {
		in.EnvironmentGaps = append(in.EnvironmentGaps, *c)
	}
	sort.Slice(in.EnvironmentGaps, func(i, j int) bool {
		a, b := in.EnvironmentGaps[i], in.EnvironmentGaps[j]
		if a.Mentions != b.Mentions {
			return a.Mentions > b.Mentions
		}
		return a.Env < b.Env
	})
	for _, r := range gaps.Rows(list, ms) {
		if r.Mentions == 0 || r.MergedInto != "" {
			continue
		}
		if in.TopGaps = append(in.TopGaps, r); len(in.TopGaps) == 10 {
			break
		}
	}
	out.Insights = in
	return out, nil
}

func sortCounts(list []NameCount) {
	sort.Slice(list, func(i, j int) bool {
		if list[i].Count != list[j].Count {
			return list[i].Count > list[j].Count
		}
		return strings.ToLower(list[i].Name) < strings.ToLower(list[j].Name)
	})
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

// ExportGaps renders the board as Markdown or CSV. It returns the body,
// its content type and a dated file name.
func (e *Engine) ExportGaps(format, status string) ([]byte, string, string, error) {
	rows, by, err := e.gapRows(GapFilter{Status: status})
	if err != nil {
		return nil, "", "", err
	}
	now := e.Now().UTC()
	name := "greenroom-gaps-" + now.Format("2006-01-02")
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", "md", "markdown":
		return gaps.Markdown(rows, by, now, status), "text/markdown; charset=utf-8", name + ".md", nil
	case "csv":
		return gaps.CSV(rows), "text/csv; charset=utf-8", name + ".csv", nil
	}
	return nil, "", "", fmt.Errorf("format %q: use md or csv", format)
}
