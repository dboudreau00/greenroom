package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/dboudreau00/greenroom/internal/demos"
	"github.com/dboudreau00/greenroom/internal/engine"
	"github.com/dboudreau00/greenroom/internal/gaps"
)

const debriefUsage = `usage:
  greenroom debriefs [--since 30d] [--json]
  greenroom debrief <demo-id> --rating 4 [--highlights t] [--lowlights t] [--env-notes t] [--next-steps t]
                 [--outcome o] [--competitor name ...] [--gap "kind:severity:title" ...] [--gap-id "id:severity:detail" ...]
  greenroom gaps [--status open] [--kind k] [--env e] [--merged] [--json]
  greenroom gaps show <id> [--json]
  greenroom gaps set <id> [--status s] [--owner o] [--notes t] [--title t] [--kind k] [--env e] [--module m]
  greenroom gaps merge <id> <into>
  greenroom gaps export [--format md|csv] [--status s] [--out file]

A debrief opens once the demo is done. On a demo that already has one, flags that are
not given keep what is stored and --gap / --gap-id add to the stored gaps. --since takes
30d, 720h, all, a date or RFC 3339 (default 90d). Kinds: feature, data, integration,
environment, content, performance, security, other. Severities: low, medium, high, blocker.
Statuses: open, planned, in-progress, done, wont-do (--status active is the first three).`

func (c *cli) debriefs(args []string) int {
	fs := newFlags("debriefs", c.stderr)
	sinceFlag := fs.String("since", "", "window start: 30d, 720h, all, a date (default 90d)")
	asJSON := fs.Bool("json", false, "print as JSON")
	if err := fs.Parse(reorder(fs, args)); err != nil {
		return 3
	}
	if err := c.open(true); err != nil {
		return c.fail(err)
	}
	since, err := engine.ParseSince(*sinceFlag, time.Now())
	if err != nil {
		return c.fail(err)
	}
	out, err := c.eng.Debriefs(since)
	if err != nil {
		return c.fail(err)
	}
	if *asJSON {
		c.printJSON(out)
		return 0
	}
	window := "every demo"
	if !since.IsZero() {
		window = "demos since " + since.Local().Format("2006-01-02")
	}
	in := out.Insights
	fmt.Fprintf(c.stdout, "Debriefs for %s: %d waiting, %d submitted\n", window, len(out.Pending), len(out.Submitted))
	if len(out.Pending) > 0 {
		fmt.Fprintln(c.stdout, "\nWaiting for a debrief (oldest first):")
		tw := tabwriter.NewWriter(c.stdout, 2, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "  WHEN\tID\tENV\tTITLE\tPROSPECT\tOUTCOME")
		for _, b := range out.Pending {
			fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\t%s\n", b.Start.Local().Format("2006-01-02 15:04"), b.ID, b.Env, b.Title, b.Prospect, b.Outcome)
		}
		tw.Flush()
		fmt.Fprintln(c.stdout, "  write one: greenroom debrief <id> --rating 4 --highlights \"...\"")
	}
	if in.Submitted == 0 {
		return 0
	}
	fmt.Fprintf(c.stdout, "\nAverage rating %.2f over %s", in.AverageRating, count(in.Submitted, "debrief"))
	var per []string
	for _, g := range in.RatingByPersona {
		per = append(per, fmt.Sprintf("%s %.2f (%d)", g.Persona, g.Average, g.Count))
	}
	if len(per) > 0 {
		fmt.Fprintf(c.stdout, "; by persona: %s", strings.Join(per, ", "))
	}
	fmt.Fprintln(c.stdout)
	var oc []string
	for _, o := range demos.Outcomes {
		if n := in.Outcomes[o]; n > 0 {
			oc = append(oc, fmt.Sprintf("%s %d", o, n))
		}
	}
	if len(oc) > 0 {
		fmt.Fprintln(c.stdout, "Outcomes:", strings.Join(oc, ", "))
	}
	if len(in.Competitors) > 0 {
		var cs []string
		for _, n := range in.Competitors {
			cs = append(cs, fmt.Sprintf("%s %d", n.Name, n.Count))
		}
		fmt.Fprintln(c.stdout, "Competitors:", strings.Join(cs, ", "))
	}
	if len(in.TopGaps) > 0 {
		fmt.Fprintf(c.stdout, "\nTop gaps (%s of %s):\n", count(in.Mentions, "mention"), count(in.GapsMentioned, "gap"))
		c.gapTable(in.TopGaps, "  ")
	}
	return 0
}

func (c *cli) gapTable(rows []gaps.Row, indent string) {
	tw := tabwriter.NewWriter(c.stdout, 2, 4, 2, ' ', 0)
	fmt.Fprintln(tw, indent+"SCORE\tPROSPECTS\tMENTIONS\tLAST\tKIND\tSTATUS\tID\tTITLE")
	for _, r := range rows {
		last := ""
		if r.LastMentioned != nil {
			last = r.LastMentioned.Local().Format("2006-01-02")
		}
		status := r.Status
		if r.MergedInto != "" {
			status = "merged into " + r.MergedInto
		}
		fmt.Fprintf(tw, "%s%d\t%d\t%d\t%s\t%s\t%s\t%s\t%s\n", indent, r.Score, r.Prospects, r.Mentions, last, r.Kind, status, r.ID, r.Title)
	}
	tw.Flush()
}

// count says "1 gap" or "3 gaps".
func count(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// parseNewGap reads "kind:severity:title".
func parseNewGap(s, env string) (engine.DebriefGapInput, error) {
	p := strings.SplitN(s, ":", 3)
	if len(p) < 3 || strings.TrimSpace(p[2]) == "" {
		return engine.DebriefGapInput{}, fmt.Errorf("--gap %q: use kind:severity:title", s)
	}
	ng := &engine.NewGap{Kind: strings.TrimSpace(p[0]), Title: p[2]}
	if ng.Kind == gaps.KindEnvironment {
		ng.Env = env
	}
	return engine.DebriefGapInput{New: ng, Severity: strings.TrimSpace(p[1])}, nil
}

// parseGapID reads "id:severity:detail"; the detail is optional.
func parseGapID(s string) (engine.DebriefGapInput, error) {
	p := strings.SplitN(s, ":", 3)
	if len(p) < 2 {
		return engine.DebriefGapInput{}, fmt.Errorf("--gap-id %q: use id:severity[:detail]", s)
	}
	in := engine.DebriefGapInput{GapID: strings.TrimSpace(p[0]), Severity: strings.TrimSpace(p[1])}
	if len(p) == 3 {
		in.Detail = p[2]
	}
	return in, nil
}

func (c *cli) debrief(args []string) int {
	fs := newFlags("debrief", c.stderr)
	rating := fs.Int("rating", 0, "how the demo went, 1 to 5")
	highlights := fs.String("highlights", "", "what landed")
	lowlights := fs.String("lowlights", "", "what fell flat")
	envNotes := fs.String("env-notes", "", "what broke or was missing in the demo environment")
	nextSteps := fs.String("next-steps", "", "next steps")
	outcome := fs.String("outcome", "", "outcome (positive, neutral, negative, no-show, rescheduled)")
	asJSON := fs.Bool("json", false, "print the debrief as JSON")
	var competitors, newGaps, gapIDs listFlag
	fs.Var(&competitors, "competitor", "competitor the prospect mentioned, repeat for more")
	fs.Var(&newGaps, "gap", "new gap as kind:severity:title, repeat for more")
	fs.Var(&gapIDs, "gap-id", "existing gap as id:severity[:detail], repeat for more")
	if err := fs.Parse(reorder(fs, args)); err != nil {
		return 3
	}
	if fs.NArg() < 1 {
		return c.fail(errors.New(debriefUsage))
	}
	if err := c.open(true); err != nil {
		return c.fail(err)
	}
	d, err := c.eng.Demos().Get(fs.Arg(0))
	if err != nil {
		return c.fail(err)
	}
	req := engine.DebriefRequest{Competitors: []string{}}
	if db := d.Debrief; db != nil {
		req = engine.DebriefRequest{Rating: db.Rating, Highlights: db.Highlights, Lowlights: db.Lowlights, EnvironmentNotes: db.EnvironmentNotes,
			Competitors: db.Competitors, NextSteps: db.NextSteps, Outcome: db.Outcome}
		for _, r := range db.Gaps {
			req.Gaps = append(req.Gaps, engine.DebriefGapInput{GapID: r.GapID, Severity: r.Severity, Detail: r.Detail, Quote: r.Quote})
		}
	}
	set := map[string]bool{}
	fs.Visit(func(fl *flag.Flag) { set[fl.Name] = true })
	text := func(name string, in string, out *string) {
		if set[name] {
			*out = in
		}
	}
	if set["rating"] {
		req.Rating = *rating
	}
	text("highlights", *highlights, &req.Highlights)
	text("lowlights", *lowlights, &req.Lowlights)
	text("env-notes", *envNotes, &req.EnvironmentNotes)
	text("next-steps", *nextSteps, &req.NextSteps)
	text("outcome", *outcome, &req.Outcome)
	if set["competitor"] {
		req.Competitors = competitors
	}
	for _, s := range gapIDs {
		in, err := parseGapID(s)
		if err != nil {
			return c.fail(err)
		}
		replaced := false
		for i := range req.Gaps {
			if req.Gaps[i].GapID == in.GapID {
				req.Gaps[i], replaced = in, true
			}
		}
		if !replaced {
			req.Gaps = append(req.Gaps, in)
		}
	}
	for _, s := range newGaps {
		in, err := parseNewGap(s, d.Env)
		if err != nil {
			return c.fail(err)
		}
		req.Gaps = append(req.Gaps, in)
	}
	if _, err := c.eng.SaveDebrief(d.ID, req, "cli"); err != nil {
		return c.fail(err)
	}
	v, err := c.eng.GetDebrief(d.ID)
	if err != nil {
		return c.fail(err)
	}
	if *asJSON {
		c.printJSON(v)
		return 0
	}
	c.printDebrief(v)
	return 0
}

func (c *cli) printDebrief(v *engine.DebriefView) {
	db := v.Debrief
	fmt.Fprintf(c.stdout, "%s: %s, debrief rating %d/5\n", v.Demo.ID, v.Demo.Title, db.Rating)
	row := func(label, value string) {
		if value == "" {
			return
		}
		lines := strings.Split(value, "\n")
		fmt.Fprintf(c.stdout, "  %-12s %s\n", label, lines[0])
		for _, l := range lines[1:] {
			fmt.Fprintf(c.stdout, "  %-12s %s\n", "", l)
		}
	}
	row("outcome", db.Outcome)
	row("landed", db.Highlights)
	row("fell flat", db.Lowlights)
	row("environment", db.EnvironmentNotes)
	row("next steps", db.NextSteps)
	row("competitors", strings.Join(db.Competitors, ", "))
	row("by", db.SubmittedBy+" at "+db.SubmittedAt.Local().Format("2006-01-02 15:04"))
	if len(db.Gaps) == 0 {
		return
	}
	fmt.Fprintln(c.stdout, "  gaps")
	for _, r := range db.Gaps {
		title := r.GapID
		if g := v.Gaps[r.GapID]; g != nil {
			title = g.ID + " " + g.Title + " (" + g.Kind + ")"
		}
		fmt.Fprintf(c.stdout, "    %-8s %s\n", r.Severity, title)
		if r.Detail != "" {
			fmt.Fprintf(c.stdout, "    %-8s %s\n", "", strings.ReplaceAll(r.Detail, "\n", " "))
		}
		if r.Quote != "" {
			fmt.Fprintf(c.stdout, "    %-8s \"%s\"\n", "", strings.ReplaceAll(r.Quote, "\n", " "))
		}
	}
}

func (c *cli) gaps(args []string) int {
	sub := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub, args = args[0], args[1:]
	}
	fs := newFlags("gaps "+sub, c.stderr)
	status := fs.String("status", "", "status filter, or the new status")
	kind := fs.String("kind", "", "kind filter, or the new kind")
	env := fs.String("env", "", "environment filter, or the gap's environment")
	module := fs.String("module", "", "template module of an environment gap")
	owner := fs.String("owner", "", "owner")
	notes := fs.String("notes", "", "notes")
	title := fs.String("title", "", "title")
	merged := fs.Bool("merged", false, "include merged gaps")
	format := fs.String("format", "md", "export format: md or csv")
	outFile := fs.String("out", "", "write the export to a file instead of stdout")
	asJSON := fs.Bool("json", false, "print as JSON")
	if err := fs.Parse(reorder(fs, args)); err != nil {
		return 3
	}
	if err := c.open(true); err != nil {
		return c.fail(err)
	}
	switch sub {
	case "", "list":
		rows, err := c.eng.GapBoard(engine.GapFilter{Status: *status, Kind: *kind, Env: *env, IncludeMerged: *merged})
		if err != nil {
			return c.fail(err)
		}
		if *asJSON {
			c.printJSON(rows)
			return 0
		}
		if len(rows) == 0 {
			fmt.Fprintln(c.stdout, "no gaps (debriefs add them: greenroom debrief <demo-id> --gap \"feature:high:Scheduled exports\")")
			return 0
		}
		c.gapTable(rows, "")
		return 0
	case "show":
		if fs.NArg() < 1 {
			return c.fail(errors.New(debriefUsage))
		}
		v, err := c.eng.GapDetail(fs.Arg(0))
		if err != nil {
			return c.fail(err)
		}
		if *asJSON {
			c.printJSON(v)
			return 0
		}
		c.printGap(v)
		return 0
	case "set":
		if fs.NArg() < 1 {
			return c.fail(errors.New(debriefUsage))
		}
		set := map[string]bool{}
		fs.Visit(func(fl *flag.Flag) { set[fl.Name] = true })
		pick := func(name string, p *string) *string {
			if set[name] {
				return p
			}
			return nil
		}
		ed := engine.GapEdit{Title: pick("title", title), Kind: pick("kind", kind), Status: pick("status", status), Owner: pick("owner", owner),
			Notes: pick("notes", notes), Env: pick("env", env), Module: pick("module", module)}
		if _, err := c.eng.EditGap(fs.Arg(0), ed, "cli"); err != nil {
			return c.fail(err)
		}
		v, err := c.eng.GapDetail(fs.Arg(0))
		if err != nil {
			return c.fail(err)
		}
		if *asJSON {
			c.printJSON(v)
			return 0
		}
		c.printGap(v)
		return 0
	case "merge":
		if fs.NArg() < 2 {
			return c.fail(errors.New(debriefUsage))
		}
		g, n, err := c.eng.MergeGap(fs.Arg(0), fs.Arg(1), "cli")
		if err != nil {
			return c.fail(err)
		}
		fmt.Fprintf(c.stdout, "merged %s (%s) into %s; %s rewritten\n", g.ID, g.Title, g.MergedInto, count(n, "debrief"))
		return 0
	case "export":
		body, _, name, err := c.eng.ExportGaps(*format, *status)
		if err != nil {
			return c.fail(err)
		}
		if *outFile == "" {
			c.stdout.Write(body)
			return 0
		}
		if err := os.WriteFile(*outFile, body, 0o644); err != nil {
			return c.fail(err)
		}
		fmt.Fprintf(c.stderr, "wrote %s (%d bytes; suggested name %s)\n", *outFile, len(body), name)
		return 0
	}
	return c.fail(fmt.Errorf("unknown gaps command %q\n%s", sub, debriefUsage))
}

func (c *cli) printGap(v *engine.GapView) {
	g := v.Gap
	fmt.Fprintf(c.stdout, "%s: %s (%s, %s)\n", g.ID, g.Title, g.Kind, g.Status)
	row := func(label, value string) {
		if value != "" {
			fmt.Fprintf(c.stdout, "  %-10s %s\n", label, value)
		}
	}
	row("score", fmt.Sprintf("%d from %s, %s", g.Score, count(g.Prospects, "prospect"), count(g.Mentions, "mention")))
	row("merged", g.MergedInto)
	row("owner", g.Owner)
	where := g.Env
	if g.Module != "" {
		where += ", module " + g.Module
	}
	row("env", where)
	row("demoed on", strings.Join(g.Envs, ", "))
	row("personas", strings.Join(g.Personas, ", "))
	row("merged in", strings.Join(v.MergedFrom, ", "))
	if g.Notes != "" {
		for i, l := range strings.Split(g.Notes, "\n") {
			label := ""
			if i == 0 {
				label = "notes"
			}
			fmt.Fprintf(c.stdout, "  %-10s %s\n", label, l)
		}
	}
	if len(v.Mentions) == 0 {
		return
	}
	fmt.Fprintln(c.stdout, "  mentions")
	for _, m := range v.Mentions {
		who := firstNonEmpty(m.Prospect, "no prospect")
		fmt.Fprintf(c.stdout, "    %s  %-8s %s, demo %s (%s) on %s\n", m.Start.Local().Format("2006-01-02"), m.Severity, who, m.Demo, m.Title, m.Env)
		if m.Detail != "" {
			fmt.Fprintf(c.stdout, "    %20s %s\n", "", strings.ReplaceAll(m.Detail, "\n", " "))
		}
		if m.Quote != "" {
			fmt.Fprintf(c.stdout, "    %20s \"%s\"\n", "", strings.ReplaceAll(m.Quote, "\n", " "))
		}
	}
}
