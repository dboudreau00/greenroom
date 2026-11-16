package engine

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/dboudreau00/greenroom/internal/config"
	"github.com/dboudreau00/greenroom/internal/demos"
	"github.com/dboudreau00/greenroom/internal/state"
)

// ErrBusy is wrapped by the error returned when a run already holds the
// environment.
var ErrBusy = errors.New("busy")

// ErrBooked is wrapped by the error returned when a reset or snapshot would
// hit an environment while a booked demo is on stage.
var ErrBooked = errors.New("booked")

// Booking limits.
const (
	DefaultPrepLead = 30 * time.Minute
	MinPrepLead     = 5 * time.Minute
	MaxPrepLead     = 12 * time.Hour
	MaxDemoLength   = 12 * time.Hour
	MinBookingLead  = time.Minute
)

// systemAuthor signs the journal entries the scheduler writes.
const systemAuthor = "greenroom"

// DemoRequest books or logs a demo. End may be left out when Duration is
// set. Persona and Version default to the environment's pin.
type DemoRequest struct {
	Title      string          `json:"title"`
	Env        string          `json:"env"`
	Prospect   string          `json:"prospect"`
	Brand      string          `json:"brand"`
	Persona    string          `json:"persona"`
	Version    int             `json:"version"`
	Owner      string          `json:"owner"`
	Attendees  []string        `json:"attendees"`
	Start      time.Time       `json:"start"`
	End        time.Time       `json:"end"`
	Duration   config.Duration `json:"duration"`
	PrepLead   config.Duration `json:"prepLead"`
	ResetAfter bool            `json:"resetAfter"`
	Stage      string          `json:"stage"`
	Outcome    string          `json:"outcome"`
	NextStep   string          `json:"nextStep"`
}

// DemoEdit changes a demo. Absent fields are left alone. The schedule
// fields (Start, End, Duration, Persona, Version, Brand, PrepLead,
// ResetAfter) can only change while the demo is scheduled.
type DemoEdit struct {
	Title      *string          `json:"title"`
	Prospect   *string          `json:"prospect"`
	Owner      *string          `json:"owner"`
	Attendees  *[]string        `json:"attendees"`
	Stage      *string          `json:"stage"`
	Outcome    *string          `json:"outcome"`
	NextStep   *string          `json:"nextStep"`
	Start      *time.Time       `json:"start"`
	End        *time.Time       `json:"end"`
	Duration   *config.Duration `json:"duration"`
	Persona    *string          `json:"persona"`
	Version    *int             `json:"version"`
	Brand      *string          `json:"brand"`
	PrepLead   *config.Duration `json:"prepLead"`
	ResetAfter *bool            `json:"resetAfter"`
}

// DemoFilter selects demos for a listing. Scope is upcoming (not final and
// not over, soonest first), past (final or over, latest first) or all
// (latest first).
type DemoFilter struct {
	Env   string
	Scope string
	Limit int
}

// Demos returns the demo store.
func (e *Engine) Demos() *demos.Store { return e.demos }

func fmtUTC(t time.Time) string { return t.UTC().Format("2006-01-02 15:04 UTC") }

func demoActor(id string) string { return "demo:" + id }

// fillCommon cleans and checks the fields a booked and a logged demo share.
func (e *Engine) fillCommon(d *demos.Demo, req DemoRequest, v *Env, actor string) error {
	d.Env = v.Config.Name
	d.Title = demos.CleanLine(req.Title, demos.MaxTitle)
	if d.Title == "" {
		return errors.New("title is required")
	}
	d.Prospect = demos.CleanLine(req.Prospect, demos.MaxProspect)
	d.Owner = demos.CleanLine(firstNonEmpty(req.Owner, actor), demos.MaxOwner)
	att, err := cleanAttendees(req.Attendees)
	if err != nil {
		return err
	}
	d.Attendees = att
	d.Stage = firstNonEmpty(strings.TrimSpace(req.Stage), demos.DefaultStage)
	if !demos.OneOf(d.Stage, demos.Stages) {
		return fmt.Errorf("stage %q: use one of %s", d.Stage, strings.Join(demos.Stages, ", "))
	}
	d.Outcome = strings.TrimSpace(req.Outcome)
	if d.Outcome != "" && !demos.OneOf(d.Outcome, demos.Outcomes) {
		return fmt.Errorf("outcome %q: use one of %s", d.Outcome, strings.Join(demos.Outcomes, ", "))
	}
	d.NextStep = demos.CleanLine(req.NextStep, demos.MaxNextStep)
	if err := e.checkBrand(req.Brand); err != nil {
		return err
	}
	d.Brand = strings.TrimSpace(req.Brand)
	return e.setPersona(d, v, req.Persona, req.Version)
}

func cleanAttendees(in []string) ([]string, error) {
	out := []string{}
	for _, a := range in {
		if a = demos.CleanLine(a, demos.MaxAttendee); a != "" {
			out = append(out, a)
		}
	}
	if len(out) > demos.MaxAttendees {
		return nil, fmt.Errorf("at most %d attendees", demos.MaxAttendees)
	}
	return out, nil
}

// checkBrand fails closed on a brand kit that does not exist. The error is
// flattened so an unknown kit is bad input, not a missing demo.
func (e *Engine) checkBrand(slug string) error {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return nil
	}
	if _, err := e.brands.Get(slug); err != nil {
		return fmt.Errorf("brand kit %s: %v", slug, err)
	}
	return nil
}

// setPersona resolves the seed set the demo will be prepared with and
// stores it concretely, so a later pin change does not move the demo.
func (e *Engine) setPersona(d *demos.Demo, v *Env, persona string, version int) error {
	set, err := e.ResolveSet(v, strings.TrimSpace(persona), version)
	if err != nil {
		return err
	}
	if len(set.Problems) > 0 {
		return fmt.Errorf("seed set %s cannot be applied: %s", set.ID(), strings.Join(set.Problems, "; "))
	}
	d.Persona, d.Version = set.Persona, set.Version
	return nil
}

func checkSlot(start, end, now time.Time) error {
	if start.IsZero() {
		return errors.New("start is required")
	}
	if start.Before(now.Add(MinBookingLead)) {
		return fmt.Errorf("start %s is not at least a minute in the future; use log for a demo that already happened", fmtUTC(start))
	}
	if !end.After(start) {
		return errors.New("end must be after start")
	}
	if end.Sub(start) > MaxDemoLength {
		return fmt.Errorf("a demo is at most %s long", humanDuration(MaxDemoLength))
	}
	return nil
}

func slotEnd(start, end time.Time, dur config.Duration) time.Time {
	if end.IsZero() && dur > 0 {
		return start.Add(dur.D())
	}
	return end.UTC()
}

func conflict(o *demos.Demo) error {
	return fmt.Errorf("%w: %s is booked from %s to %s by demo %s (%s)", demos.ErrConflict, o.Env, fmtUTC(o.Start), fmtUTC(o.End), o.ID, o.Title)
}

// BookDemo books a demo on an environment's calendar. The scheduler of a
// running portal prepares it prepLead before the start.
func (e *Engine) BookDemo(req DemoRequest, actor string) (*demos.Demo, error) {
	now := e.Now().UTC()
	v, err := e.Env(req.Env)
	if err != nil {
		return nil, err
	}
	d := &demos.Demo{Booked: true, Status: demos.Scheduled, ResetAfter: req.ResetAfter, CreatedAt: now, UpdatedAt: now, CreatedBy: actor}
	if err := e.fillCommon(d, req, v, actor); err != nil {
		return nil, err
	}
	d.Start = req.Start.UTC()
	d.End = slotEnd(d.Start, req.End, req.Duration)
	if err := checkSlot(d.Start, d.End, now); err != nil {
		return nil, err
	}
	lead := req.PrepLead.Or(DefaultPrepLead)
	if lead < MinPrepLead || lead > MaxPrepLead {
		return nil, fmt.Errorf("prep lead %s: use %s to %s", lead, humanDuration(MinPrepLead), humanDuration(MaxPrepLead))
	}
	d.PrepLead = config.Duration(lead)
	d.AddEntry(demos.Entry{At: now, Author: actor, Kind: "system", Text: fmt.Sprintf("booked by %s: %s, %s", actor, d.SetID(), brandWord(d.Brand))})
	err = e.demos.Create(d, func(all []*demos.Demo) error {
		if o := demos.Overlapping(all, d.Env, d.Start, d.End, ""); o != nil {
			return conflict(o)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return d, nil
}

// LogDemo records a demo that already happened. It is never scheduled and
// starts out done.
func (e *Engine) LogDemo(req DemoRequest, actor string) (*demos.Demo, error) {
	now := e.Now().UTC()
	v, err := e.Env(req.Env)
	if err != nil {
		return nil, err
	}
	d := &demos.Demo{Booked: false, Status: demos.Done, CreatedAt: now, UpdatedAt: now, CreatedBy: actor}
	if err := e.fillCommon(d, req, v, actor); err != nil {
		return nil, err
	}
	d.Start = req.Start.UTC()
	if d.Start.IsZero() {
		d.Start = now
	}
	if d.Start.After(now.Add(MinBookingLead)) {
		return nil, errors.New("a logged demo has already happened; book a demo in the future instead")
	}
	d.End = slotEnd(d.Start, req.End, req.Duration)
	if d.End.IsZero() {
		d.End = d.Start
	}
	if d.End.Before(d.Start) {
		return nil, errors.New("end must not be before start")
	}
	if d.End.Sub(d.Start) > MaxDemoLength {
		return nil, fmt.Errorf("a demo is at most %s long", humanDuration(MaxDemoLength))
	}
	d.AddEntry(demos.Entry{At: now, Author: actor, Kind: "system", Text: fmt.Sprintf("logged by %s after the fact: %s, %s", actor, d.SetID(), brandWord(d.Brand))})
	if err := e.demos.Create(d, nil); err != nil {
		return nil, err
	}
	return d, nil
}

func brandWord(slug string) string {
	if slug == "" {
		return "house brand"
	}
	return "brand " + slug
}

// EditDemo applies an edit. Changing the slot re-runs the overlap check.
func (e *Engine) EditDemo(id string, ed DemoEdit, actor string) (*demos.Demo, error) {
	now := e.Now().UTC()
	return e.demos.Update(id, func(d *demos.Demo, all func() ([]*demos.Demo, error)) error {
		var changed []string
		if ed.Title != nil {
			t := demos.CleanLine(*ed.Title, demos.MaxTitle)
			if t == "" {
				return errors.New("title can not be empty")
			}
			if t != d.Title {
				d.Title, changed = t, append(changed, "title")
			}
		}
		text := func(in *string, out *string, max int, name string) {
			if in == nil {
				return
			}
			if v := demos.CleanLine(*in, max); v != *out {
				*out, changed = v, append(changed, name)
			}
		}
		text(ed.Prospect, &d.Prospect, demos.MaxProspect, "prospect")
		text(ed.Owner, &d.Owner, demos.MaxOwner, "owner")
		text(ed.NextStep, &d.NextStep, demos.MaxNextStep, "next step")
		if ed.Attendees != nil {
			att, err := cleanAttendees(*ed.Attendees)
			if err != nil {
				return err
			}
			if strings.Join(att, "\x00") != strings.Join(d.Attendees, "\x00") {
				d.Attendees, changed = att, append(changed, "attendees")
			}
		}
		if ed.Stage != nil {
			s := strings.TrimSpace(*ed.Stage)
			if !demos.OneOf(s, demos.Stages) {
				return fmt.Errorf("stage %q: use one of %s", s, strings.Join(demos.Stages, ", "))
			}
			if s != d.Stage {
				d.Stage, changed = s, append(changed, "stage")
			}
		}
		if ed.Outcome != nil {
			o := strings.TrimSpace(*ed.Outcome)
			if o != "" && !demos.OneOf(o, demos.Outcomes) {
				return fmt.Errorf("outcome %q: use one of %s", o, strings.Join(demos.Outcomes, ", "))
			}
			if o != d.Outcome {
				d.Outcome, changed = o, append(changed, "outcome")
			}
		}
		schedule := ed.Start != nil || ed.End != nil || ed.Duration != nil || ed.Persona != nil || ed.Version != nil || ed.Brand != nil || ed.PrepLead != nil || ed.ResetAfter != nil
		if schedule {
			if !d.Booked || d.Status != demos.Scheduled {
				return fmt.Errorf("%w: start, end, persona, brand, prep lead and reset after only change while the demo is scheduled (it is %s)", demos.ErrState, d.Status)
			}
			sc, err := e.editSchedule(d, ed, now, all)
			if err != nil {
				return err
			}
			changed = append(changed, sc...)
		}
		if len(changed) == 0 {
			return demos.ErrUnchanged
		}
		d.UpdatedAt = now
		d.AddEntry(demos.Entry{At: now, Author: actor, Kind: "system", Text: "changed by " + actor + ": " + strings.Join(changed, ", ")})
		return nil
	})
}

func (e *Engine) editSchedule(d *demos.Demo, ed DemoEdit, now time.Time, all func() ([]*demos.Demo, error)) ([]string, error) {
	var changed []string
	v, err := e.Env(d.Env)
	if err != nil {
		return nil, err
	}
	start, end := d.Start, d.End
	if ed.Start != nil {
		start = ed.Start.UTC()
		if ed.End == nil && ed.Duration == nil {
			end = start.Add(d.End.Sub(d.Start))
		}
	}
	if ed.End != nil {
		end = ed.End.UTC()
	}
	if ed.Duration != nil {
		end = start.Add(ed.Duration.D())
	}
	if !start.Equal(d.Start) || !end.Equal(d.End) {
		if err := checkSlot(start, end, now); err != nil {
			return nil, err
		}
		list, err := all()
		if err != nil {
			return nil, err
		}
		if o := demos.Overlapping(list, d.Env, start, end, d.ID); o != nil {
			return nil, conflict(o)
		}
		d.Start, d.End = start, end
		changed = append(changed, "time")
	}
	if ed.Persona != nil || ed.Version != nil {
		persona, version := d.Persona, d.Version
		if ed.Persona != nil {
			persona, version = *ed.Persona, 0
		}
		if ed.Version != nil {
			version = *ed.Version
		}
		before := d.SetID()
		if err := e.setPersona(d, v, persona, version); err != nil {
			return nil, err
		}
		if d.SetID() != before {
			changed = append(changed, "seed set")
		}
	}
	if ed.Brand != nil {
		b := strings.TrimSpace(*ed.Brand)
		if err := e.checkBrand(b); err != nil {
			return nil, err
		}
		if b != d.Brand {
			d.Brand, changed = b, append(changed, "brand")
		}
	}
	if ed.PrepLead != nil {
		lead := ed.PrepLead.Or(DefaultPrepLead)
		if lead < MinPrepLead || lead > MaxPrepLead {
			return nil, fmt.Errorf("prep lead %s: use %s to %s", lead, humanDuration(MinPrepLead), humanDuration(MaxPrepLead))
		}
		if config.Duration(lead) != d.PrepLead {
			d.PrepLead, changed = config.Duration(lead), append(changed, "prep lead")
		}
	}
	if ed.ResetAfter != nil && *ed.ResetAfter != d.ResetAfter {
		d.ResetAfter, changed = *ed.ResetAfter, append(changed, "reset after")
	}
	return changed, nil
}

// CancelDemo cancels a demo that is not final. The record stays.
func (e *Engine) CancelDemo(id, actor string) (*demos.Demo, error) {
	now := e.Now().UTC()
	return e.demos.Update(id, func(d *demos.Demo, _ func() ([]*demos.Demo, error)) error {
		if d.Final() {
			return fmt.Errorf("%w: demo %s is already %s", demos.ErrState, d.ID, d.Status)
		}
		text := "cancelled by " + actor
		if d.Status == demos.Preparing {
			text += "; the preparation run already started and finishes on its own"
		}
		d.Status, d.UpdatedAt = demos.Cancelled, now
		d.AddEntry(demos.Entry{At: now, Author: actor, Kind: "system", Text: text})
		return nil
	})
}

// PrepareDemo starts the preparation of a scheduled demo now, without
// waiting for its prep lead.
func (e *Engine) PrepareDemo(id, actor string) (*demos.Demo, error) {
	now := e.Now().UTC()
	return e.demos.Update(id, func(d *demos.Demo, _ func() ([]*demos.Demo, error)) error {
		if !d.Booked || d.Status != demos.Scheduled {
			return fmt.Errorf("%w: only a scheduled demo can be prepared (it is %s)", demos.ErrState, d.Status)
		}
		if !now.Before(d.End) {
			return fmt.Errorf("%w: the slot of demo %s has ended", demos.ErrState, d.ID)
		}
		runID, err := e.startPrep(d)
		if err != nil {
			return err
		}
		e.markPreparing(d, runID, now, "preparation started early by "+actor)
		return nil
	})
}

// startPrep launches the demo's reset. It runs inside a store update, so
// it never passes Override (which would write to the store again).
func (e *Engine) startPrep(d *demos.Demo) (string, error) {
	return e.StartReset(d.Env, ResetOptions{Persona: d.Persona, Version: d.Version, Brand: d.Brand, Actor: demoActor(d.ID)})
}

func (e *Engine) markPreparing(d *demos.Demo, runID string, now time.Time, why string) {
	d.PrepRunID, d.Status, d.Error, d.UpdatedAt = runID, demos.Preparing, "", now
	d.AddEntry(demos.Entry{At: now, Author: systemAuthor, Kind: "system", RunID: runID,
		Text: fmt.Sprintf("%s: reset to %s, %s", why, d.SetID(), brandWord(d.Brand))})
}

// AddDemoEntry appends a journal entry. System entries are the
// scheduler's; people can not write them.
func (e *Engine) AddDemoEntry(id, kind, text, author string) (*demos.Demo, error) {
	kind = strings.TrimSpace(kind)
	if kind == "" {
		kind = "note"
	}
	if kind == "system" || !demos.OneOf(kind, demos.Kinds) {
		return nil, fmt.Errorf("kind %q: use note, issue, question, follow-up or feedback", kind)
	}
	text = demos.CleanText(text)
	if text == "" {
		return nil, errors.New("text is required")
	}
	if n := len([]rune(text)); n > demos.MaxEntryText {
		return nil, fmt.Errorf("text is %d characters; the limit is %d", n, demos.MaxEntryText)
	}
	now := e.Now().UTC()
	return e.demos.Update(id, func(d *demos.Demo, _ func() ([]*demos.Demo, error)) error {
		if len(d.Journal) >= demos.MaxEntries {
			return fmt.Errorf("%w: the journal of demo %s is full (%d entries)", demos.ErrState, d.ID, demos.MaxEntries)
		}
		d.AddEntry(demos.Entry{At: now, Author: demos.CleanLine(author, demos.MaxOwner), Kind: kind, Text: text})
		d.UpdatedAt = now
		return nil
	})
}

// DeleteDemoEntry removes a journal entry a person wrote.
func (e *Engine) DeleteDemoEntry(id, entryID, actor string) (*demos.Demo, error) {
	if !demos.ValidEntryID(entryID) {
		return nil, fmt.Errorf("%w: bad entry id", demos.ErrNotFound)
	}
	now := e.Now().UTC()
	return e.demos.Update(id, func(d *demos.Demo, _ func() ([]*demos.Demo, error)) error {
		for i, en := range d.Journal {
			if en.ID != entryID {
				continue
			}
			if en.Kind == "system" {
				return fmt.Errorf("%w: system entries can not be deleted", demos.ErrState)
			}
			d.Journal = append(d.Journal[:i], d.Journal[i+1:]...)
			d.UpdatedAt = now
			return nil
		}
		return fmt.Errorf("%w: journal entry %s", demos.ErrNotFound, entryID)
	})
}

// ListDemos returns the short form of the demos a filter selects.
func (e *Engine) ListDemos(f DemoFilter) ([]demos.Brief, error) {
	if f.Env != "" && !config.ValidName(f.Env) {
		return nil, fmt.Errorf("environment name %q is not valid", f.Env)
	}
	all, err := e.demos.Briefs()
	if err != nil {
		return nil, err
	}
	now := e.Now()
	out := []demos.Brief{}
	for _, b := range all {
		if f.Env != "" && b.Env != f.Env {
			continue
		}
		over := demos.Final(b.Status) || !now.Before(b.End)
		switch f.Scope {
		case "", "upcoming":
			if over {
				continue
			}
		case "past":
			if !over {
				continue
			}
		case "all":
		default:
			return nil, fmt.Errorf("scope %q: use upcoming, past or all", f.Scope)
		}
		out = append(out, b)
	}
	if f.Scope == "past" || f.Scope == "all" {
		sort.SliceStable(out, func(i, j int) bool { return out[i].Start.After(out[j].Start) })
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// demoMarks finds the next upcoming and the on-stage demo of an
// environment for its card and header.
func demoMarks(all []demos.Brief, env string, now time.Time) (next, live *demos.Brief) {
	for i := range all {
		b := all[i]
		if b.Env != env || !b.Booked || demos.Final(b.Status) {
			continue
		}
		if live == nil && b.Protects(now) {
			live = &b
			continue
		}
		if next == nil && b.Start.After(now) {
			next = &b
		}
	}
	return next, live
}

// liveDemo returns the booked demo on stage on an environment, read fresh
// from disk.
func (e *Engine) liveDemo(env string) *demos.Demo {
	list, err := e.demos.List()
	if err != nil {
		return nil
	}
	now := e.Now()
	for _, d := range list {
		if d.Env == env && d.Protects(now) {
			return d
		}
	}
	return nil
}

// guardLive refuses a reset or snapshot while a booked demo is on stage,
// unless override is set; then it returns the demo so the caller can note
// the override in its journal.
func (e *Engine) guardLive(env string, override bool) (*demos.Demo, error) {
	d := e.liveDemo(env)
	if d == nil {
		return nil, nil
	}
	if !override {
		return nil, fmt.Errorf("environment %s is %w for demo %s (%s) until %s; use override to go ahead anyway", env, ErrBooked, d.ID, d.Title, fmtUTC(d.End))
	}
	return d, nil
}

func (e *Engine) noteOverride(d *demos.Demo, actor, what, runID string) {
	if d == nil {
		return
	}
	now := e.Now().UTC()
	_, err := e.demos.Update(d.ID, func(d *demos.Demo, _ func() ([]*demos.Demo, error)) error {
		d.AddEntry(demos.Entry{At: now, Author: systemAuthor, Kind: "system", RunID: runID,
			Text: fmt.Sprintf("%s by %s during the live demo (override)", what, firstNonEmpty(actor, "unknown"))})
		d.UpdatedAt = now
		return nil
	})
	if err != nil {
		e.log.Warn("note override on demo", "demo", d.ID, "err", err)
	}
}

// DemoTick advances every booked demo that is not final by one scheduler
// pass, reading the records fresh from disk. RunScheduler calls it on its
// own ticker; tests call it after moving e.Now.
func (e *Engine) DemoTick(ctx context.Context) {
	list, err := e.demos.List()
	if err != nil {
		e.log.Warn("list demos", "err", err)
		return
	}
	for _, d := range list {
		if !d.Booked {
			continue
		}
		if d.Final() && !(d.Status == demos.Done && d.Cleanup == demos.CleanupPending) {
			continue
		}
		// Several transitions can be due at once (a portal that was down
		// through the start time); follow them until the demo settles.
		for range 6 {
			if ctx.Err() != nil || !e.stepDemo(ctx, d.ID) {
				break
			}
		}
	}
}

// stepDemo makes one transition and reports whether anything changed.
func (e *Engine) stepDemo(ctx context.Context, id string) bool {
	now := e.Now().UTC()
	changed, checkRun := false, ""
	_, err := e.demos.Update(id, func(d *demos.Demo, all func() ([]*demos.Demo, error)) error {
		sys := func(text, runID string) {
			d.AddEntry(demos.Entry{At: now, Author: systemAuthor, Kind: "system", Text: text, RunID: runID})
			d.UpdatedAt, changed = now, true
		}
		switch {
		case !d.Booked:
			return demos.ErrUnchanged
		case d.Status == demos.Scheduled && !now.Before(d.End):
			d.Status = demos.Missed
			sys("missed: the slot ended before the demo was prepared (was greenroom serve running?)", "")
		case d.Status == demos.Scheduled && !now.Before(d.Start.Add(-d.PrepLead.D())):
			runID, err := e.startPrep(d)
			switch {
			case errors.Is(err, ErrBusy):
				return demos.ErrUnchanged
			case errors.Is(err, ErrBooked):
				// Another demo is on stage; wait for it to end. Say so once.
				text := "preparation waits: " + err.Error()
				if n := len(d.Journal); n > 0 && d.Journal[n-1].Text == text {
					return demos.ErrUnchanged
				}
				d.AddEntry(demos.Entry{At: now, Author: systemAuthor, Kind: "system", Text: text})
				d.UpdatedAt = now
				return nil
			case err != nil:
				d.Status, d.Error = demos.PrepFailed, "preparation could not start: "+err.Error()
				sys(d.Error, "")
			default:
				e.markPreparing(d, runID, now, "preparing")
				changed = true
			}
		case d.Status == demos.Preparing:
			ok, msg := e.prepOutcome(d.Env, d.PrepRunID)
			switch {
			case msg == "":
				return demos.ErrUnchanged
			case !ok:
				d.Status, d.Error = demos.PrepFailed, "preparation failed: "+msg
				sys(d.Error, d.PrepRunID)
			default:
				// The pre-call check probes every dependency; run it
				// outside the store lock.
				checkRun = d.PrepRunID
				return demos.ErrUnchanged
			}
		case (d.Status == demos.Ready || d.Status == demos.PrepFailed) && !now.Before(d.Start):
			d.Status = demos.Live
			sys("live", "")
		case d.Status == demos.Live && !now.Before(d.End):
			d.Status = demos.Done
			if d.ResetAfter {
				d.Cleanup = demos.CleanupPending
			}
			sys("done", "")
		case d.Status == demos.Done && d.Cleanup == demos.CleanupPending:
			list, err := all()
			if err != nil {
				return err
			}
			if o := nextOnStage(list, d, now); o != nil {
				d.Cleanup = demos.CleanupSkipped
				sys(fmt.Sprintf("cleanup skipped: demo %s (%s) is preparing this environment next", o.ID, o.Title), "")
				return nil
			}
			runID, err := e.StartReset(d.Env, ResetOptions{Actor: demoActor(d.ID)})
			switch {
			case errors.Is(err, ErrBusy):
				return demos.ErrUnchanged
			case err != nil:
				d.Cleanup = demos.CleanupFailed
				sys("cleanup could not start: "+err.Error(), "")
			default:
				d.CleanupRunID, d.Cleanup = runID, demos.CleanupStarted
				sys("cleanup: reset to the pin with the house brand", runID)
			}
		default:
			return demos.ErrUnchanged
		}
		return nil
	})
	if err != nil {
		e.log.Warn("advance demo", "demo", id, "err", err)
		return false
	}
	if checkRun != "" {
		return e.finishPrep(ctx, id, checkRun)
	}
	return changed
}

// nextOnStage returns another demo on the same environment whose
// preparation window has begun, so a cleanup reset would undo its prep.
func nextOnStage(all []*demos.Demo, d *demos.Demo, now time.Time) *demos.Demo {
	for _, o := range all {
		if o.ID == d.ID || o.Env != d.Env || !o.Holds() {
			continue
		}
		if !now.Before(o.Start.Add(-o.PrepLead.D())) {
			return o
		}
	}
	return nil
}

// prepOutcome reports how the prep run ended; msg is empty while it runs.
func (e *Engine) prepOutcome(env, runID string) (ok bool, msg string) {
	if runID == "" {
		return false, "no preparation run recorded"
	}
	run, err := e.Run(env, runID)
	if err != nil {
		return false, "preparation run " + runID + " not found"
	}
	switch run.Status {
	case state.StatusRunning:
		e.mu.RLock()
		_, active := e.active[runID]
		e.mu.RUnlock()
		if active {
			return false, ""
		}
		return false, "the preparation run was interrupted (the portal restarted while it ran)"
	case state.StatusOK:
		return true, "ok"
	}
	return false, firstNonEmpty(run.Error, run.Status)
}

// finishPrep runs the recorded pre-call check after a successful prep and
// marks the demo ready.
func (e *Engine) finishPrep(ctx context.Context, id, prepRun string) bool {
	d, err := e.demos.Get(id)
	if err != nil {
		return false
	}
	rep, cerr := e.Check(ctx, d.Env, CheckOptions{Record: true, Drift: true, Actor: demoActor(id)})
	now := e.Now().UTC()
	changed := false
	_, err = e.demos.Update(id, func(d *demos.Demo, _ func() ([]*demos.Demo, error)) error {
		if d.Status != demos.Preparing || d.PrepRunID != prepRun {
			return demos.ErrUnchanged
		}
		d.UpdatedAt, changed = now, true
		if cerr != nil {
			d.Status, d.Error = demos.PrepFailed, "pre-call check failed: "+cerr.Error()
			d.AddEntry(demos.Entry{At: now, Author: systemAuthor, Kind: "system", Text: d.Error})
			return nil
		}
		d.Status, d.CheckRunID, d.Readiness = demos.Ready, rep.RunID, rep.Readiness
		d.AddEntry(demos.Entry{At: now, Author: systemAuthor, Kind: "system", RunID: rep.RunID,
			Text: fmt.Sprintf("ready: %s, %s", strings.ToUpper(rep.Readiness), rep.Summary)})
		return nil
	})
	if err != nil {
		e.log.Warn("finish demo prep", "demo", id, "err", err)
		return false
	}
	return changed
}

// runDemoLoop drives DemoTick on its own ticker until ctx ends.
func (e *Engine) runDemoLoop(ctx context.Context, every time.Duration) {
	every = min(every, 30*time.Second)
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		e.DemoTick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
