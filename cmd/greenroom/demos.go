package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/dboudreau00/greenroom/internal/config"
	"github.com/dboudreau00/greenroom/internal/demos"
	"github.com/dboudreau00/greenroom/internal/engine"
	"github.com/dboudreau00/greenroom/internal/seeds"
)

const demoUsage = `usage:
  greenroom book <env> --at "2026-11-20 15:00" --for 45m --title "..." [--prospect p] [--persona cfo@v2]
                 [--brand slug] [--prep 30m] [--reset-after] [--attendee "name" ...] [--owner o] [--stage s]
  greenroom demos [--env e] [--past | --all] [--json]
  greenroom demo show <id> [--json]
  greenroom demo note <id> [--kind issue] <text>
  greenroom demo set <id> [--outcome o] [--stage s] [--next-step t] [--prospect p] [--title t] [--owner o]
  greenroom demo cancel <id>
  greenroom demo log <env> --title "..." [--at time] [--for 45m] [--persona p] [--brand slug] [--outcome o]
                 [--prospect p] [--stage s] [--note text]

--at takes RFC 3339 or local "YYYY-MM-DD HH:MM". Stages: intro, discovery, deep-dive, poc,
executive, other. Outcomes: positive, neutral, negative, no-show, rescheduled. Journal kinds:
note, issue, question, follow-up, feedback.`

// listFlag collects a repeated string flag.
type listFlag []string

func (l *listFlag) String() string     { return strings.Join(*l, ", ") }
func (l *listFlag) Set(s string) error { *l = append(*l, s); return nil }

// parseAt reads a demo time: RFC 3339, or a local date and time.
func parseAt(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	for _, layout := range []string{"2006-01-02 15:04", "2006-01-02T15:04", "2006-01-02 15:04:05"} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("time %q: use RFC 3339 or \"YYYY-MM-DD HH:MM\" in local time", s)
}

// localUser names the person running the CLI, for the demo owner.
func localUser() string {
	for _, k := range []string{"GREENROOM_USER", "USER", "USERNAME"} {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}

func localSlot(d *demos.Demo) string {
	s, e := d.Start.Local(), d.End.Local()
	if !d.End.After(d.Start) {
		return s.Format("Mon 2006-01-02 15:04")
	}
	if s.Format("2006-01-02") == e.Format("2006-01-02") {
		return s.Format("Mon 2006-01-02 15:04") + "-" + e.Format("15:04")
	}
	return s.Format("Mon 2006-01-02 15:04") + " to " + e.Format("Mon 2006-01-02 15:04")
}

// demoFlags are the flags book and demo log share.
type demoFlags struct {
	at, title, prospect, persona, brand, owner, stage, outcome, note, nextStep *string
	dur, prep                                                                  *time.Duration
	resetAfter, asJSON                                                         *bool
	attendees                                                                  listFlag
}

func newDemoFlags(fs *flag.FlagSet) *demoFlags {
	f := &demoFlags{}
	f.at = fs.String("at", "", "start: RFC 3339 or local \"YYYY-MM-DD HH:MM\"")
	f.dur = fs.Duration("for", time.Hour, "length of the demo")
	f.title = fs.String("title", "", "title shown on the tracker and the calendar")
	f.prospect = fs.String("prospect", "", "prospect company")
	f.persona = fs.String("persona", "", "seed set, persona or persona@vN (default: the pin)")
	f.brand = fs.String("brand", "", "brand kit to personalize for")
	f.owner = fs.String("owner", "", "owner (default: the local user)")
	f.stage = fs.String("stage", "", "deal stage (default discovery)")
	f.outcome = fs.String("outcome", "", "outcome")
	f.nextStep = fs.String("next-step", "", "agreed next step")
	f.note = fs.String("note", "", "first journal note")
	f.prep = fs.Duration("prep", engine.DefaultPrepLead, "how long before the start the preparation reset runs (5m to 12h)")
	f.resetAfter = fs.Bool("reset-after", false, "reset to the pin with the house brand when the demo ends")
	f.asJSON = fs.Bool("json", false, "print the demo as JSON")
	fs.Var(&f.attendees, "attendee", "attendee name, repeat for more")
	return f
}

func (f *demoFlags) request(env string) (engine.DemoRequest, error) {
	req := engine.DemoRequest{Env: env, Title: *f.title, Prospect: *f.prospect, Brand: *f.brand, Owner: firstNonEmpty(*f.owner, localUser()),
		Attendees: f.attendees, Stage: *f.stage, Outcome: *f.outcome, NextStep: *f.nextStep, Duration: config.Duration(*f.dur),
		PrepLead: config.Duration(*f.prep), ResetAfter: *f.resetAfter}
	if *f.persona != "" {
		p, v, err := seeds.ParseRef(*f.persona)
		if err != nil {
			return req, err
		}
		req.Persona, req.Version = p, v
	}
	if *f.at != "" {
		t, err := parseAt(*f.at)
		if err != nil {
			return req, err
		}
		req.Start = t
	}
	return req, nil
}

func (c *cli) book(args []string) int {
	fs := newFlags("book", c.stderr)
	f := newDemoFlags(fs)
	if err := fs.Parse(reorder(fs, args)); err != nil {
		return 3
	}
	if fs.NArg() < 1 || *f.at == "" {
		return c.fail(errors.New(demoUsage))
	}
	if err := c.load(true); err != nil {
		return c.fail(err)
	}
	req, err := f.request(fs.Arg(0))
	if err != nil {
		return c.fail(err)
	}
	d, err := c.eng.BookDemo(req, "cli")
	if err != nil {
		return c.fail(err)
	}
	if *f.note != "" {
		if d, err = c.eng.AddDemoEntry(d.ID, "note", *f.note, "cli"); err != nil {
			return c.fail(err)
		}
	}
	if *f.asJSON {
		c.printJSON(d)
		return 0
	}
	fmt.Fprintf(c.stdout, "booked %s: %s on %s, %s\n", d.ID, d.Title, d.Env, localSlot(d))
	fmt.Fprintf(c.stdout, "  prep  reset to %s, %s, at %s\n", d.SetID(), brandLabel(d.Brand), d.Start.Add(-d.PrepLead.D()).Local().Format("15:04"))
	fmt.Fprintln(c.stdout, "  note  preparation runs while greenroom serve is running; the CLI only writes the booking")
	return 0
}

func brandLabel(slug string) string {
	if slug == "" {
		return "house brand"
	}
	return "brand " + slug
}

func (c *cli) demoList(args []string) int {
	fs := newFlags("demos", c.stderr)
	env := fs.String("env", "", "only this environment")
	past := fs.Bool("past", false, "demos that are over")
	all := fs.Bool("all", false, "every demo")
	asJSON := fs.Bool("json", false, "print as JSON")
	if err := fs.Parse(reorder(fs, args)); err != nil {
		return 3
	}
	if err := c.open(true); err != nil {
		return c.fail(err)
	}
	scope := "upcoming"
	if *past {
		scope = "past"
	}
	if *all {
		scope = "all"
	}
	list, err := c.eng.ListDemos(engine.DemoFilter{Env: *env, Scope: scope, Limit: 1000})
	if err != nil {
		return c.fail(err)
	}
	if *asJSON {
		c.printJSON(list)
		return 0
	}
	if len(list) == 0 {
		fmt.Fprintf(c.stdout, "no %s demos (greenroom book <env> --at ... books one)\n", scope)
		return 0
	}
	tw := tabwriter.NewWriter(c.stdout, 2, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "WHEN\tID\tENV\tTITLE\tPROSPECT\tSEED SET\tSTATUS\tREADY\tOUTCOME\tNOTES")
	for _, b := range list {
		set := ""
		if b.Persona != "" {
			set = fmt.Sprintf("%s@v%d", b.Persona, b.Version)
		}
		status := b.Status
		if !b.Booked {
			status += " (logged)"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%d\n", b.Start.Local().Format("2006-01-02 15:04"), b.ID, b.Env, b.Title, b.Prospect, set, status,
			b.Readiness, b.Outcome, b.JournalCount)
	}
	tw.Flush()
	return 0
}

func (c *cli) demo(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(c.stderr, demoUsage)
		return 3
	}
	sub, args := args[0], args[1:]
	fs := newFlags("demo "+sub, c.stderr)
	f := newDemoFlags(fs)
	kind := fs.String("kind", "note", "journal entry kind")
	if err := fs.Parse(reorder(fs, args)); err != nil {
		return 3
	}
	if fs.NArg() < 1 {
		return c.fail(errors.New(demoUsage))
	}
	if sub == "log" {
		if err := c.load(true); err != nil {
			return c.fail(err)
		}
	} else if err := c.open(true); err != nil {
		return c.fail(err)
	}
	var d *demos.Demo
	var err error
	switch sub {
	case "show":
		d, err = c.eng.Demos().Get(fs.Arg(0))
	case "note":
		if fs.NArg() < 2 {
			return c.fail(errors.New(demoUsage))
		}
		d, err = c.eng.AddDemoEntry(fs.Arg(0), *kind, strings.Join(fs.Args()[1:], " "), "cli")
	case "set":
		set := map[string]bool{}
		fs.Visit(func(fl *flag.Flag) { set[fl.Name] = true })
		pick := func(name string, p *string) *string {
			if set[name] {
				return p
			}
			return nil
		}
		ed := engine.DemoEdit{Outcome: pick("outcome", f.outcome), Stage: pick("stage", f.stage), NextStep: pick("next-step", f.nextStep),
			Prospect: pick("prospect", f.prospect), Title: pick("title", f.title), Owner: pick("owner", f.owner)}
		d, err = c.eng.EditDemo(fs.Arg(0), ed, "cli")
	case "cancel":
		d, err = c.eng.CancelDemo(fs.Arg(0), "cli")
	case "log":
		var req engine.DemoRequest
		if req, err = f.request(fs.Arg(0)); err != nil {
			return c.fail(err)
		}
		set := map[string]bool{}
		fs.Visit(func(fl *flag.Flag) { set[fl.Name] = true })
		if !set["for"] {
			req.Duration = 0
		}
		if d, err = c.eng.LogDemo(req, "cli"); err == nil && *f.note != "" {
			d, err = c.eng.AddDemoEntry(d.ID, "note", *f.note, "cli")
		}
	default:
		return c.fail(fmt.Errorf("unknown demo command %q\n%s", sub, demoUsage))
	}
	if err != nil {
		return c.fail(err)
	}
	if *f.asJSON {
		c.printJSON(d)
		return 0
	}
	c.printDemo(d)
	return 0
}

func (c *cli) printDemo(d *demos.Demo) {
	kind := "booked"
	if !d.Booked {
		kind = "logged"
	}
	fmt.Fprintf(c.stdout, "%s: %s (%s, %s)\n", d.ID, d.Title, d.Status, kind)
	row := func(label, value string) {
		if value != "" {
			fmt.Fprintf(c.stdout, "  %-10s %s\n", label, value)
		}
	}
	row("when", localSlot(d))
	row("env", d.Env)
	row("prospect", d.Prospect)
	row("seed set", d.SetID())
	row("brand", d.Brand)
	row("owner", d.Owner)
	row("attendees", strings.Join(d.Attendees, ", "))
	row("stage", d.Stage)
	row("outcome", d.Outcome)
	row("next step", d.NextStep)
	if d.Booked {
		row("prep", fmt.Sprintf("%s before the start", d.PrepLead.D()))
		if d.ResetAfter {
			row("after", "reset to the pin, house brand ("+firstNonEmpty(d.Cleanup, "when done")+")")
		}
	}
	row("readiness", strings.ToUpper(d.Readiness))
	row("prep run", d.PrepRunID)
	row("check run", d.CheckRunID)
	row("cleanup", d.CleanupRunID)
	row("error", d.Error)
	if len(d.Journal) == 0 {
		return
	}
	fmt.Fprintln(c.stdout, "  journal")
	for i := len(d.Journal) - 1; i >= 0; i-- {
		en := d.Journal[i]
		lines := strings.Split(en.Text, "\n")
		fmt.Fprintf(c.stdout, "    %s  %-9s %s  [%s, %s]\n", en.At.Local().Format("2006-01-02 15:04"), en.Kind, lines[0], en.Author, en.ID)
		for _, l := range lines[1:] {
			fmt.Fprintf(c.stdout, "    %28s %s\n", "", l)
		}
	}
}
