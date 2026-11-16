package engine

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/dboudreau00/greenroom/internal/config"
	"github.com/dboudreau00/greenroom/internal/probe"
	"github.com/dboudreau00/greenroom/internal/seeds"
	"github.com/dboudreau00/greenroom/internal/sqlrun"
	"github.com/dboudreau00/greenroom/internal/state"
	"github.com/dboudreau00/greenroom/internal/targets"
)

// Readiness levels.
const (
	Green = "green"
	Amber = "amber"
	Red   = "red"
)

// CheckReport is the outcome of a pre-call check.
type CheckReport struct {
	Env       string         `json:"env"`
	At        time.Time      `json:"at"`
	Duration  time.Duration  `json:"duration"`
	Readiness string         `json:"readiness"`
	Summary   string         `json:"summary"`
	Results   []probe.Result `json:"results"`
	Stale     *StaleReport   `json:"stale,omitempty"`
	RunID     string         `json:"runId,omitempty"`
}

// StaleItem is one reason the environment is not exactly what the seed
// set says it should be.
type StaleItem struct {
	Severity string `json:"severity"` // stale | warn | info
	Dep      string `json:"dep,omitempty"`
	Code     string `json:"code"`
	Message  string `json:"message"`
	Fix      string `json:"fix,omitempty"`
}

// StaleReport lists everything stale about an environment.
type StaleReport struct {
	Env      string         `json:"env"`
	At       time.Time      `json:"at"`
	Pinned   string         `json:"pinned"`
	Applied  *state.Applied `json:"applied,omitempty"`
	Fresh    bool           `json:"fresh"`
	Stale    int            `json:"stale"`
	Warnings int            `json:"warnings"`
	Items    []StaleItem    `json:"items"`
	Latest   map[string]int `json:"latestVersions"`
	// DriftChecked is false when drift queries were skipped (overview mode).
	DriftChecked bool `json:"driftChecked"`
}

// CheckOptions tune a check.
type CheckOptions struct {
	// Record saves the check as a run and records uptime samples.
	Record bool
	// Drift runs the drift queries as part of the stale report.
	Drift bool
	Actor string
}

// Check runs every probe, records samples and computes readiness.
func (e *Engine) Check(ctx context.Context, envName string, opts CheckOptions) (*CheckReport, error) {
	v, err := e.Env(envName)
	if err != nil {
		return nil, err
	}
	start := e.Now()
	var rec *recorder
	if opts.Record {
		rec = e.newRun(v.Config.Name, state.RunCheck, opts.Actor)
		rec.Info("", "pre-call check of %s (%d dependencies)", v.Config.DisplayTitle(), len(v.Config.Dependencies))
	}
	results := v.Prober.ProbeAll(ctx)
	e.setLatest(v.Config.Name, results)
	var samples []state.Sample
	for _, r := range results {
		samples = append(samples, r.Sample())
		if rec != nil {
			switch {
			case !r.OK:
				rec.Error(r.Dep, "DOWN %s (%s)", r.Message, r.Latency.Round(time.Millisecond))
			case r.Warn:
				rec.Warn(r.Dep, "WARN %s (%s)", r.Message, r.Latency.Round(time.Millisecond))
			default:
				rec.Info(r.Dep, "ok %s (%s)", r.Message, r.Latency.Round(time.Millisecond))
			}
		}
	}
	if err := e.store.AppendSamples(v.Config.Name, samples); err != nil {
		e.log.Warn("append samples", "env", v.Config.Name, "err", err)
	}
	stale, err := e.StaleReport(ctx, v, results, opts.Drift)
	if err != nil {
		if rec != nil {
			rec.Warn("", "stale report incomplete: %v", err)
		}
	}
	readiness, summary := Readiness(results, stale)
	report := &CheckReport{Env: v.Config.Name, At: start, Duration: e.Now().Sub(start), Readiness: readiness, Summary: summary, Results: results, Stale: stale}
	if rec != nil {
		for _, it := range stale.Items {
			switch it.Severity {
			case "stale":
				rec.Warn(it.Dep, "STALE %s", it.Message)
			case "warn":
				rec.Warn(it.Dep, "%s", it.Message)
			default:
				rec.Info(it.Dep, "%s", it.Message)
			}
		}
		var ferr error
		if readiness == Red {
			ferr = fmt.Errorf("%s", summary)
		}
		run := rec.Finish(ferr, strings.ToUpper(readiness)+": "+summary)
		report.RunID = run.ID
	}
	return report, nil
}

// Readiness folds probe results and staleness into a traffic light.
func Readiness(results []probe.Result, stale *StaleReport) (string, string) {
	var down, critical, warn []string
	for _, r := range results {
		switch {
		case !r.OK && r.Critical:
			critical = append(critical, r.Dep)
		case !r.OK:
			down = append(down, r.Dep)
		case r.Warn:
			warn = append(warn, r.Dep)
		}
	}
	staleCount := 0
	if stale != nil {
		staleCount = stale.Stale
	}
	switch {
	case len(critical) > 0:
		return Red, fmt.Sprintf("%s down: %s", plural(len(critical), "critical dependency", "critical dependencies"), strings.Join(critical, ", "))
	case len(down) > 0 || len(warn) > 0 || staleCount > 0:
		var parts []string
		if len(down) > 0 {
			parts = append(parts, "down: "+strings.Join(down, ", "))
		}
		if len(warn) > 0 {
			parts = append(parts, "warnings: "+strings.Join(warn, ", "))
		}
		if staleCount > 0 {
			parts = append(parts, plural(staleCount, "stale item", "stale items"))
		}
		return Amber, strings.Join(parts, "; ")
	}
	if len(results) == 0 {
		return Amber, "no dependencies probed yet"
	}
	msg := fmt.Sprintf("all %d dependencies up", len(results))
	if stale != nil && stale.Fresh {
		msg += ", seed fresh"
	}
	return Green, msg
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// StaleReport compares the pinned seed set, the applied record, live
// fingerprints and drift queries. results may be nil to use the latest
// probe results.
func (e *Engine) StaleReport(ctx context.Context, v *Env, results []probe.Result, drift bool) (*StaleReport, error) {
	now := e.Now()
	rep := &StaleReport{Env: v.Config.Name, At: now, Items: []StaleItem{}, Latest: map[string]int{}, DriftChecked: drift}
	for p, s := range seeds.Latest(v.Seeds) {
		rep.Latest[p] = s.Version
	}
	pinned, pinnedID := e.PinnedSet(v)
	rep.Pinned = pinnedID
	add := func(sev, dep, code, msg, fix string) {
		rep.Items = append(rep.Items, StaleItem{Severity: sev, Dep: dep, Code: code, Message: msg, Fix: fix})
	}

	if v.SeedsErr != nil {
		add("warn", "", "seeds-error", "seed sets could not all be read: "+v.SeedsErr.Error(), "fix the seed directory")
	}
	for _, s := range v.Seeds {
		if len(s.Problems) > 0 {
			add("warn", "", "seed-problems", fmt.Sprintf("seed set %s has problems: %s", s.ID(), strings.Join(s.Problems, "; ")), "fix "+s.Dir)
		}
	}
	if pinned == nil && pinnedID != "" {
		add("stale", "", "pin-missing", fmt.Sprintf("pinned seed set %s does not exist", pinnedID), "fix pin in environment.json or add the seed set")
	}

	applied, err := e.store.LoadApplied(v.Config.Name)
	if err != nil {
		return rep, err
	}
	rep.Applied = applied
	// The most recent reset attempt, successful or not.
	var lastReset *state.RunSummary
	if runs, err := e.store.ListRuns(v.Config.Name, 25); err == nil {
		for i := range runs {
			if runs[i].Kind == state.RunReset {
				lastReset = &runs[i]
				break
			}
		}
	}
	failedSince := func(t time.Time) bool {
		return lastReset != nil && lastReset.Status == state.StatusFailed && (t.IsZero() || lastReset.StartedAt.After(t))
	}
	if applied == nil {
		if failedSince(time.Time{}) {
			add("stale", "", "last-reset-failed", fmt.Sprintf("no successful reset yet; the last attempt (%s ago) failed: %s", humanDuration(now.Sub(lastReset.StartedAt)), lastReset.Error), "run: greenroom reset "+v.Config.Name)
		} else {
			add("stale", "", "never-reset", "environment has never been reset from a seed set", "run: greenroom reset "+v.Config.Name)
		}
	} else {
		if failedSince(applied.At) {
			add("stale", "", "last-reset-failed", fmt.Sprintf("the last reset (%s ago) failed after %s was applied: %s", humanDuration(now.Sub(lastReset.StartedAt)), applied.SetID, lastReset.Error), "run: greenroom reset "+v.Config.Name)
		}
		if pinned != nil && applied.SetID != pinned.ID() {
			add("stale", "", "wrong-version", fmt.Sprintf("pinned %s but %s is applied", pinned.ID(), applied.SetID), "run: greenroom reset "+v.Config.Name)
		}
		if cur, ok := seeds.Find(v.Seeds, applied.Persona, applied.Version); ok {
			if cur.Hash != applied.SetHash {
				changed := diffFiles(applied.Files, cur.Files)
				msg := fmt.Sprintf("seed files of %s changed since the reset", applied.SetID)
				if len(changed) > 0 {
					msg += ": " + strings.Join(changed, ", ")
				}
				add("stale", "", "seed-changed", msg, "run: greenroom reset "+v.Config.Name)
			}
		} else {
			add("warn", "", "applied-missing", fmt.Sprintf("applied seed set %s no longer exists on disk", applied.SetID), "")
		}
		limit := v.Config.StaleAfter.Or(config.DefaultStaleAfter)
		age := now.Sub(applied.At)
		if age > limit {
			add("stale", "", "too-old", fmt.Sprintf("last reset %s ago, limit is %s", humanDuration(age), humanDuration(limit)), "run: greenroom reset "+v.Config.Name)
		}
		if pinned != nil {
			if latest, ok := seeds.Find(v.Seeds, pinned.Persona, 0); ok && latest.Version > pinned.Version && v.Config.Pin.Version != 0 {
				add("info", "", "newer-version", fmt.Sprintf("%s@v%d is available, pin is v%d", pinned.Persona, latest.Version, pinned.Version), "bump pin.version in environment.json")
			}
		}
	}

	// Live fingerprints versus the ones taken right after the reset.
	latest := map[string]probe.Result{}
	if results != nil {
		for _, r := range results {
			latest[r.Dep] = r
		}
	} else {
		latest = e.Latest(v.Config.Name)
	}
	for _, dep := range v.Config.Dependencies {
		r, ok := latest[dep.Name]
		if !ok {
			continue
		}
		if r.Warn && r.OK {
			add("warn", dep.Name, "probe-warning", r.Message, "")
		}
		if applied == nil || r.Fingerprint == nil {
			continue
		}
		fp, ok := applied.Fingerprints[dep.Name]
		if !ok || fp.Kind != r.Fingerprint.Kind || fp.Value == r.Fingerprint.Value {
			continue
		}
		switch r.Fingerprint.Kind {
		case "docker-container":
			add("stale", dep.Name, "recreated", fmt.Sprintf("container was recreated since the reset (%s, now %s)", fp.Value, r.Fingerprint.Value), "run: greenroom reset "+v.Config.Name)
		case "k8s-pod":
			add("stale", dep.Name, "recreated", "pod was replaced since the reset", "run: greenroom reset "+v.Config.Name)
		case "ec2-launch":
			add("stale", dep.Name, "relaunched", fmt.Sprintf("instance was launched after the reset (%s)", r.Fingerprint.Value), "run: greenroom reset "+v.Config.Name)
		case "k8s-generation":
			add("warn", dep.Name, "redeployed", fmt.Sprintf("workload was redeployed since the reset (generation %s, now %s)", fp.Value, r.Fingerprint.Value), "")
		case "sql-version":
			add("warn", dep.Name, "server-changed", fmt.Sprintf("database server changed: %s", r.Fingerprint.Value), "")
		case "http-version", "http-etag":
			add("info", dep.Name, "app-changed", fmt.Sprintf("application version changed: %s", r.Fingerprint.Value), "")
		case "tls-serial":
			add("info", dep.Name, "cert-rotated", "certificate was rotated since the reset", "")
		case "command-output":
			add("warn", dep.Name, "output-changed", fmt.Sprintf("probe output changed: %s", r.Fingerprint.Value), "")
		}
	}

	if drift {
		persona := ""
		if applied != nil {
			persona = applied.Persona
		}
		for _, dep := range v.Config.Dependencies {
			dep = driftFor(dep, persona)
			if len(dep.Drift) == 0 {
				continue
			}
			if r, ok := latest[dep.Name]; ok && !r.OK {
				add("info", dep.Name, "drift-skipped", "drift checks skipped because the dependency is down", "")
				continue
			}
			values, errs := e.driftValues(ctx, v, dep)
			for _, dc := range dep.Drift {
				if err, bad := errs[dc.Name]; bad {
					msg := fmt.Sprintf("drift check %s failed: %v", dc.Name, firstLine(err.Error()))
					sev, fix := "warn", ""
					if applied != nil {
						if _, hadBaseline := applied.Baselines[dep.Name][dc.Name]; hadBaseline {
							// It worked right after the reset, so the seeded data is gone.
							sev, fix = "stale", "run: greenroom reset "+v.Config.Name
							msg += " (it passed right after the reset)"
						}
					}
					add(sev, dep.Name, "drift-error", msg, fix)
					continue
				}
				val := values[dc.Name]
				var baseline *float64
				if applied != nil {
					if b, ok := applied.Baselines[dep.Name][dc.Name]; ok {
						baseline = &b
					}
				}
				ok, want := compareDrift(val, dc, baseline)
				if !ok {
					add("stale", dep.Name, "drift", fmt.Sprintf("%s is %s, expected %s", dc.Name, fmtNum(val), want), "run: greenroom reset "+v.Config.Name)
				}
			}
		}
	}

	sort.SliceStable(rep.Items, func(i, j int) bool { return sevRank(rep.Items[i].Severity) < sevRank(rep.Items[j].Severity) })
	for _, it := range rep.Items {
		switch it.Severity {
		case "stale":
			rep.Stale++
		case "warn":
			rep.Warnings++
		}
	}
	rep.Fresh = rep.Stale == 0 && applied != nil
	return rep, nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func sevRank(s string) int {
	switch s {
	case "stale":
		return 0
	case "warn":
		return 1
	}
	return 2
}

// driftFor returns the dependency with only the drift checks that apply to
// the given persona's seed set.
func driftFor(dep config.Dependency, persona string) config.Dependency {
	var kept []config.DriftCheck
	for _, dc := range dep.Drift {
		if dc.Applies(persona) {
			kept = append(kept, dc)
		}
	}
	dep.Drift = kept
	return dep
}

// driftValues evaluates every drift check of a dependency.
func (e *Engine) driftValues(ctx context.Context, v *Env, dep config.Dependency) (map[string]float64, map[string]error) {
	values := map[string]float64{}
	errs := map[string]error{}
	var runner sqlrun.Runner
	if dep.Kind == config.KindPostgres || dep.Kind == config.KindMySQL {
		var err error
		runner, err = v.Prober.SQLRunner(dep)
		if err != nil {
			for _, dc := range dep.Drift {
				errs[dc.Name] = err
			}
			return values, errs
		}
		defer runner.Close()
	}
	for _, dc := range dep.Drift {
		cctx, cancel := context.WithTimeout(ctx, dep.Timeout.Or(config.DefaultTimeout)*2)
		var text string
		var err error
		switch {
		case dc.Query != "":
			if runner == nil {
				err = fmt.Errorf("drift query on a %s dependency needs a database", dep.Kind)
			} else {
				text, err = runner.Scalar(cctx, dc.Query)
			}
		case len(dc.Command) > 0:
			ex, xerr := v.Registry.Executor(dep.Target)
			if xerr != nil {
				err = xerr
				break
			}
			res, rerr := ex.Run(cctx, targets.RunSpec{Argv: dc.Command, Container: dep.Container, Pod: dep.Resource, Namespace: dep.Namespace, InstanceID: dep.InstanceID, Env: v.Config.Env}, nil)
			if rerr != nil {
				err = rerr
			} else if !res.OK() {
				err = res.Error()
			} else {
				text = strings.TrimSpace(res.Stdout)
				if i := strings.IndexByte(text, '\n'); i >= 0 {
					text = text[:i]
				}
			}
		}
		cancel()
		if err != nil {
			errs[dc.Name] = err
			continue
		}
		n, err := sqlrun.ParseNumber(text)
		if err != nil {
			errs[dc.Name] = fmt.Errorf("result %q is not a number", text)
			continue
		}
		values[dc.Name] = n
	}
	return values, errs
}

// compareDrift applies expect/min/max with tolerance, or the baseline.
func compareDrift(val float64, dc config.DriftCheck, baseline *float64) (bool, string) {
	tol := dc.Tolerance
	switch {
	case dc.Expect != nil:
		return math.Abs(val-*dc.Expect) <= tol, describeExpect(*dc.Expect, tol)
	case dc.Min != nil || dc.Max != nil:
		ok := true
		var parts []string
		if dc.Min != nil {
			ok = ok && val >= *dc.Min
			parts = append(parts, "at least "+fmtNum(*dc.Min))
		}
		if dc.Max != nil {
			ok = ok && val <= *dc.Max
			parts = append(parts, "at most "+fmtNum(*dc.Max))
		}
		return ok, strings.Join(parts, " and ")
	case baseline != nil:
		return math.Abs(val-*baseline) <= tol, describeExpect(*baseline, tol) + " (value after the reset)"
	}
	return true, ""
}

func describeExpect(v, tol float64) string {
	if tol > 0 {
		return fmtNum(v) + " (tolerance " + fmtNum(tol) + ")"
	}
	return fmtNum(v)
}

func fmtNum(f float64) string {
	if f == math.Trunc(f) && math.Abs(f) < 1e15 {
		return fmt.Sprintf("%d", int64(f))
	}
	return fmt.Sprintf("%g", f)
}

func diffFiles(old, cur []seeds.FileDigest) []string {
	om := map[string]string{}
	for _, f := range old {
		om[f.Path] = f.Hash
	}
	cm := map[string]string{}
	for _, f := range cur {
		cm[f.Path] = f.Hash
	}
	var out []string
	for _, f := range cur {
		if h, ok := om[f.Path]; !ok {
			out = append(out, f.Path+" (added)")
		} else if h != f.Hash {
			out = append(out, f.Path+" (modified)")
		}
	}
	for _, f := range old {
		if _, ok := cm[f.Path]; !ok {
			out = append(out, f.Path+" (removed)")
		}
	}
	if len(out) > 6 {
		out = append(out[:6], fmt.Sprintf("and %d more", len(out)-6))
	}
	return out
}

func humanDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		h := int(d.Hours())
		m := int(d.Minutes()) % 60
		if m == 0 {
			return fmt.Sprintf("%dh", h)
		}
		return fmt.Sprintf("%dh%02dm", h, m)
	default:
		days := int(d.Hours() / 24)
		h := int(d.Hours()) % 24
		if h == 0 {
			return fmt.Sprintf("%dd", days)
		}
		return fmt.Sprintf("%dd %dh", days, h)
	}
}
