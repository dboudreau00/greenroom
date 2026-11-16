package engine

import (
	"context"
	"time"

	"github.com/dboudreau00/greenroom/internal/config"
	"github.com/dboudreau00/greenroom/internal/probe"
	"github.com/dboudreau00/greenroom/internal/seeds"
	"github.com/dboudreau00/greenroom/internal/sqlrun"
	"github.com/dboudreau00/greenroom/internal/state"
)

// EnvSummary is the overview card for one environment.
type EnvSummary struct {
	Name        string         `json:"name"`
	Title       string         `json:"title"`
	Owner       string         `json:"owner,omitempty"`
	Description string         `json:"description,omitempty"`
	Readiness   string         `json:"readiness"`
	Summary     string         `json:"summary"`
	Pinned      string         `json:"pinned"`
	Applied     *state.Applied `json:"applied,omitempty"`
	Deps        int            `json:"deps"`
	Down        int            `json:"down"`
	Warn        int            `json:"warn"`
	Stale       int            `json:"stale"`
	LastProbe   *time.Time     `json:"lastProbe,omitempty"`
	Uptime24h   float64        `json:"uptime24h"`
	Busy        string         `json:"busy,omitempty"`
	SeedSets    int            `json:"seedSets"`
	Personas    []string       `json:"personas"`
	StaleAfter  string         `json:"staleAfter"`
	Problems    []string       `json:"problems,omitempty"`
	// Template is the template and modules the environment was built from.
	Template *config.TemplateRef `json:"template,omitempty"`
}

// DepView is one dependency with its live state.
type DepView struct {
	config.Dependency
	Latest        *probe.Result      `json:"latest,omitempty"`
	Uptime24h     state.Uptime       `json:"uptime24h"`
	Uptime7d      state.Uptime       `json:"uptime7d"`
	ResetPrint    *state.Fingerprint `json:"resetFingerprint,omitempty"`
	HasHeal       bool               `json:"hasHeal"`
	HasReset      bool               `json:"hasReset"`
	CanSnapshot   bool               `json:"canSnapshot"`
	LastSnapshot  *state.SnapshotRecord `json:"lastSnapshot,omitempty"`
}

// SeedView is one seed set with its relation to the environment.
type SeedView struct {
	ID          string             `json:"id"`
	Persona     string             `json:"persona"`
	Version     int                `json:"version"`
	Title       string             `json:"title"`
	Description string             `json:"description,omitempty"`
	Vertical    string             `json:"vertical,omitempty"`
	Author      string             `json:"author,omitempty"`
	Tags        []string           `json:"tags"`
	Hash        string             `json:"hash"`
	Dir         string             `json:"dir"`
	Files       []seeds.FileDigest `json:"files"`
	Steps       []seeds.Step       `json:"steps"`
	Verify      []seeds.Check      `json:"verify"`
	Problems    []string           `json:"problems"`
	Pinned      bool               `json:"pinned"`
	Applied     bool               `json:"applied"`
	Latest      bool               `json:"latest"`
	Changed     bool               `json:"changed"`
}

// EnvDetail is everything the environment page needs.
type EnvDetail struct {
	EnvSummary
	Dependencies []DepView              `json:"dependencies"`
	Seeds        []SeedView             `json:"seeds"`
	Runs         []state.RunSummary     `json:"runs"`
	Snapshots    []state.SnapshotRecord `json:"snapshots"`
	Heals        []state.HealEvent      `json:"heals"`
	Stale        *StaleReport           `json:"stale,omitempty"`
	Targets      []TargetBrief          `json:"targets"`
	Env          map[string]string      `json:"env,omitempty"`
	Dir          string                 `json:"dir"`
}

// TargetBrief is a target's name and kind.
type TargetBrief struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	Host string `json:"host,omitempty"`
}

// Summary builds the overview card. It never touches the network: stale
// detection runs without drift queries and readiness comes from the latest
// probe results.
func (e *Engine) Summary(ctx context.Context, v *Env) EnvSummary {
	latest := e.Latest(v.Config.Name)
	var results []probe.Result
	var last *time.Time
	for _, d := range v.Config.Dependencies {
		if r, ok := latest[d.Name]; ok {
			results = append(results, r)
			if last == nil || r.CheckedAt.After(*last) {
				t := r.CheckedAt
				last = &t
			}
		}
	}
	stale, _ := e.StaleReport(ctx, v, results, false)
	readiness, summary := Readiness(results, stale)
	if len(results) < len(v.Config.Dependencies) {
		if len(results) == 0 {
			readiness, summary = Amber, "not probed yet"
		} else {
			readiness, summary = Amber, "some dependencies not probed yet"
		}
	}
	s := EnvSummary{Name: v.Config.Name, Title: v.Config.DisplayTitle(), Owner: v.Config.Owner, Description: v.Config.Description,
		Readiness: readiness, Summary: summary, Deps: len(v.Config.Dependencies), LastProbe: last, Busy: e.Busy(v),
		SeedSets: len(v.Seeds), Personas: seeds.Personas(v.Seeds), StaleAfter: humanDuration(v.Config.StaleAfter.Or(config.DefaultStaleAfter)),
		Template: v.Config.Template}
	if s.Personas == nil {
		s.Personas = []string{}
	}
	_, s.Pinned = e.PinnedSet(v)
	if stale != nil {
		s.Applied = stale.Applied
		s.Stale = stale.Stale
	}
	for _, r := range results {
		if !r.OK {
			s.Down++
		} else if r.Warn {
			s.Warn++
		}
	}
	samples := e.store.Samples(v.Config.Name, e.Now().Add(-24*time.Hour))
	total, ok := 0, 0
	for _, sm := range samples {
		total++
		if sm.OK {
			ok++
		}
	}
	if total > 0 {
		s.Uptime24h = 100 * float64(ok) / float64(total)
	}
	if v.SeedsErr != nil {
		s.Problems = append(s.Problems, v.SeedsErr.Error())
	}
	return s
}

// Detail builds the environment page. drift controls whether drift queries
// run (they hit the databases).
func (e *Engine) Detail(ctx context.Context, v *Env, drift bool) (*EnvDetail, error) {
	d := &EnvDetail{EnvSummary: e.Summary(ctx, v), Dir: v.Config.Dir}
	latest := e.Latest(v.Config.Name)
	now := e.Now()
	samples := e.store.Samples(v.Config.Name, now.Add(-7*24*time.Hour))
	applied, _ := e.store.LoadApplied(v.Config.Name)
	snaps, _ := e.store.Snapshots(v.Config.Name)
	for _, dep := range v.Config.Dependencies {
		dv := DepView{Dependency: RedactDependency(dep), HasHeal: dep.Heal != nil, HasReset: dep.Reset != nil}
		dv.CanSnapshot = (dep.Kind == config.KindPostgres && (dep.Snapshot == nil || dep.Snapshot.Strategy == "template")) ||
			(dep.Kind == config.KindDocker && (dep.Snapshot == nil || dep.Snapshot.Strategy == "commit"))
		if r, ok := latest[dep.Name]; ok {
			rr := r
			dv.Latest = &rr
		}
		dv.Uptime24h = state.ComputeUptime(dep.Name, samples, 24*time.Hour, now, 48)
		dv.Uptime7d = state.ComputeUptime(dep.Name, samples, 7*24*time.Hour, now, 56)
		if applied != nil {
			if fp, ok := applied.Fingerprints[dep.Name]; ok {
				f := fp
				dv.ResetPrint = &f
			}
		}
		for i := range snaps {
			if snaps[i].Dep == dep.Name {
				sr := snaps[i]
				dv.LastSnapshot = &sr
			}
		}
		d.Dependencies = append(d.Dependencies, dv)
	}
	if d.Dependencies == nil {
		d.Dependencies = []DepView{}
	}
	pinned, _ := e.PinnedSet(v)
	latestSets := seeds.Latest(v.Seeds)
	for _, s := range v.Seeds {
		sv := SeedView{ID: s.ID(), Persona: s.Persona, Version: s.Version, Title: s.DisplayTitle(), Description: s.Description, Vertical: s.Vertical,
			Author: s.Author, Tags: s.Tags, Hash: s.Hash, Dir: s.Dir, Files: s.Files, Steps: s.Steps, Verify: s.Verify, Problems: s.Problems}
		if sv.Tags == nil {
			sv.Tags = []string{}
		}
		if sv.Problems == nil {
			sv.Problems = []string{}
		}
		sv.Pinned = pinned != nil && pinned.ID() == s.ID()
		sv.Latest = latestSets[s.Persona] == s
		if applied != nil && applied.SetID == s.ID() {
			sv.Applied = true
			sv.Changed = applied.SetHash != s.Hash
		}
		d.Seeds = append(d.Seeds, sv)
	}
	if d.Seeds == nil {
		d.Seeds = []SeedView{}
	}
	runs, _ := e.store.ListRuns(v.Config.Name, 30)
	active := e.ActiveRuns(v.Config.Name)
	seen := map[string]bool{}
	for _, r := range active {
		seen[r.ID] = true
	}
	d.Runs = active
	for _, r := range runs {
		if !seen[r.ID] {
			d.Runs = append(d.Runs, r)
		}
	}
	if d.Runs == nil {
		d.Runs = []state.RunSummary{}
	}
	d.Snapshots = snaps
	if d.Snapshots == nil {
		d.Snapshots = []state.SnapshotRecord{}
	}
	d.Heals, _ = e.store.Heals(v.Config.Name, 20)
	if d.Heals == nil {
		d.Heals = []state.HealEvent{}
	}
	var results []probe.Result
	for _, dep := range v.Config.Dependencies {
		if r, ok := latest[dep.Name]; ok {
			results = append(results, r)
		}
	}
	d.Stale, _ = e.StaleReport(ctx, v, results, drift)
	d.Targets = []TargetBrief{}
	for _, n := range v.Registry.Names() {
		t := v.Config.Targets[n]
		host := t.Host
		if t.Kind == config.TargetKubernetes {
			host = firstNonEmpty(t.Context, firstNonEmpty(t.Server, t.Kubeconfig))
		}
		if t.Kind == config.TargetAWS {
			host = t.Region
		}
		d.Targets = append(d.Targets, TargetBrief{Name: n, Kind: t.Kind, Host: host})
	}
	if len(v.Config.Env) > 0 {
		d.Env = map[string]string{}
		for k := range v.Config.Env {
			d.Env[k] = "(set)"
		}
	}
	return d, nil
}

// RedactDependency hides secrets before a dependency leaves the engine.
func RedactDependency(d config.Dependency) config.Dependency {
	if d.DSN != "" {
		d.DSN = sqlrun.Redact(d.DSN)
	}
	if d.Via != nil {
		via := *d.Via
		via.DSN = sqlrun.Redact(via.DSN)
		d.Via = &via
	}
	if len(d.Headers) > 0 {
		h := map[string]string{}
		for k, v := range d.Headers {
			lk := k
			for _, c := range []string{"authorization", "cookie", "token", "key", "secret"} {
				if containsFold(lk, c) {
					v = "(redacted)"
				}
			}
			h[k] = v
		}
		d.Headers = h
	}
	return d
}

func containsFold(s, sub string) bool {
	if len(sub) > len(s) {
		return false
	}
	ls := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		ls[i] = c
	}
	for i := 0; i+len(sub) <= len(ls); i++ {
		if string(ls[i:i+len(sub)]) == sub {
			return true
		}
	}
	return false
}
