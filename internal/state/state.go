// Package state persists what Greenroom knows about each environment: which
// seed set was last applied and the fingerprints taken at that moment, the
// probe samples that feed uptime, the runs (checks, resets, heals) with
// their logs, and the snapshots that were captured. Everything is a file
// under the state directory so the CLI and the portal can share it.
package state

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dboudreau00/greenroom/internal/config"
	"github.com/dboudreau00/greenroom/internal/seeds"
)

// Fingerprint is a value captured from a dependency at reset time. When the
// live value differs later, the dependency has changed underneath the seed.
type Fingerprint struct {
	Kind   string    `json:"kind"`
	Value  string    `json:"value"`
	Detail string    `json:"detail,omitempty"`
	At     time.Time `json:"at"`
}

// Applied records the last successful reset of an environment.
type Applied struct {
	Persona      string                 `json:"persona"`
	Version      int                    `json:"version"`
	SetID        string                 `json:"setId"`
	SetHash      string                 `json:"setHash"`
	Files        []seeds.FileDigest     `json:"files,omitempty"`
	At           time.Time              `json:"at"`
	Duration     time.Duration          `json:"duration"`
	RunID        string                 `json:"runId"`
	Actor        string                 `json:"actor,omitempty"`
	Fingerprints map[string]Fingerprint `json:"fingerprints,omitempty"`
	// Baselines holds the drift check values observed right after the reset,
	// keyed by dependency then check name, for checks with no explicit expect.
	Baselines map[string]map[string]float64 `json:"baselines,omitempty"`
	// Brand is the brand kit the reset personalized the demo for.
	Brand *AppliedBrand `json:"brand,omitempty"`
}

// AppliedBrand records the brand kit used by a reset.
type AppliedBrand struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
	Hash string `json:"hash"`
}

// Sample is one probe observation.
type Sample struct {
	At  time.Time `json:"at"`
	Dep string    `json:"dep"`
	OK  bool      `json:"ok"`
	MS  int64     `json:"ms"`
	Msg string    `json:"msg,omitempty"`
}

// Incident is a run of consecutive failing samples.
type Incident struct {
	Dep      string        `json:"dep"`
	Start    time.Time     `json:"start"`
	End      time.Time     `json:"end,omitempty"`
	Open     bool          `json:"open"`
	Duration time.Duration `json:"duration"`
	Samples  int           `json:"samples"`
	Message  string        `json:"message,omitempty"`
}

// Uptime summarises samples for one dependency over a window.
type Uptime struct {
	Dep       string     `json:"dep"`
	Window    string     `json:"window"`
	Total     int        `json:"total"`
	OK        int        `json:"ok"`
	Percent   float64    `json:"percent"`
	AvgMS     int64      `json:"avgMs"`
	P95MS     int64      `json:"p95Ms"`
	Latest    *Sample    `json:"latest,omitempty"`
	LastOK    *time.Time `json:"lastOk,omitempty"`
	LastFail  *time.Time `json:"lastFail,omitempty"`
	Incidents []Incident `json:"incidents"`
	// Points is a downsampled series for sparklines: 1 = ok, 0 = fail, -1 = no data.
	Points []int8 `json:"points"`
}

// Run kinds and statuses.
const (
	RunCheck    = "check"
	RunReset    = "reset"
	RunHeal     = "heal"
	RunSnapshot = "snapshot"

	StatusRunning   = "running"
	StatusOK        = "ok"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"
	StatusSkipped   = "skipped"
)

// LogLine is one line of run output.
type LogLine struct {
	At    time.Time `json:"at"`
	Level string    `json:"level"` // info | warn | error | out
	Step  string    `json:"step,omitempty"`
	Text  string    `json:"text"`
}

// StepResult is the outcome of one step in a run.
type StepResult struct {
	Name      string        `json:"name"`
	Kind      string        `json:"kind"`
	Status    string        `json:"status"`
	StartedAt time.Time     `json:"startedAt"`
	EndedAt   time.Time     `json:"endedAt,omitempty"`
	Duration  time.Duration `json:"duration"`
	Error     string        `json:"error,omitempty"`
	Detail    string        `json:"detail,omitempty"`
}

// Run is a check, reset, heal or snapshot execution.
type Run struct {
	ID        string        `json:"id"`
	Env       string        `json:"env"`
	Kind      string        `json:"kind"`
	Persona   string        `json:"persona,omitempty"`
	Version   int           `json:"version,omitempty"`
	SetHash   string        `json:"setHash,omitempty"`
	Brand     string        `json:"brand,omitempty"`
	Actor     string        `json:"actor,omitempty"`
	Status    string        `json:"status"`
	StartedAt time.Time     `json:"startedAt"`
	EndedAt   time.Time     `json:"endedAt,omitempty"`
	Duration  time.Duration `json:"duration"`
	Error     string        `json:"error,omitempty"`
	Summary   string        `json:"summary,omitempty"`
	Steps     []StepResult  `json:"steps"`
	Log       []LogLine     `json:"log"`
}

// RunSummary is a Run without its log, for listings.
type RunSummary struct {
	ID        string        `json:"id"`
	Env       string        `json:"env"`
	Kind      string        `json:"kind"`
	Persona   string        `json:"persona,omitempty"`
	Version   int           `json:"version,omitempty"`
	Brand     string        `json:"brand,omitempty"`
	Actor     string        `json:"actor,omitempty"`
	Status    string        `json:"status"`
	StartedAt time.Time     `json:"startedAt"`
	EndedAt   time.Time     `json:"endedAt,omitempty"`
	Duration  time.Duration `json:"duration"`
	Error     string        `json:"error,omitempty"`
	Summary   string        `json:"summary,omitempty"`
	Steps     int           `json:"steps"`
	Failed    int           `json:"failed"`
}

// Brief strips the log from a run.
func (r *Run) Brief() RunSummary {
	s := RunSummary{ID: r.ID, Env: r.Env, Kind: r.Kind, Persona: r.Persona, Version: r.Version, Brand: r.Brand, Actor: r.Actor,
		Status: r.Status, StartedAt: r.StartedAt, EndedAt: r.EndedAt, Duration: r.Duration, Error: r.Error, Summary: r.Summary, Steps: len(r.Steps)}
	for _, st := range r.Steps {
		if st.Status == StatusFailed {
			s.Failed++
		}
	}
	return s
}

// SnapshotRecord describes a captured baseline.
type SnapshotRecord struct {
	Dep      string    `json:"dep"`
	Strategy string    `json:"strategy"`
	Ref      string    `json:"ref"`
	At       time.Time `json:"at"`
	Persona  string    `json:"persona,omitempty"`
	Version  int       `json:"version,omitempty"`
	SetHash  string    `json:"setHash,omitempty"`
	Note     string    `json:"note,omitempty"`
}

// HealEvent records an automatic or manual heal attempt.
type HealEvent struct {
	At     time.Time `json:"at"`
	Dep    string    `json:"dep"`
	Action string    `json:"action"`
	Auto   bool      `json:"auto"`
	OK     bool      `json:"ok"`
	Error  string    `json:"error,omitempty"`
	RunID  string    `json:"runId,omitempty"`
}

// Store is the on-disk state directory.
type Store struct {
	dir     string
	mu      sync.Mutex
	samples map[string][]Sample // env -> samples in memory, oldest first
	loaded  map[string]bool
}

// Open creates the store rooted at dir.
func Open(dir string) (*Store, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, err
	}
	return &Store{dir: abs, samples: map[string][]Sample{}, loaded: map[string]bool{}}, nil
}

// Dir returns the state directory.
func (s *Store) Dir() string { return s.dir }

func (s *Store) envDir(env string) string { return filepath.Join(s.dir, env) }

// LoadApplied returns the last applied record, or nil when there is none.
func (s *Store) LoadApplied(env string) (*Applied, error) {
	b, err := os.ReadFile(filepath.Join(s.envDir(env), "applied.json"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var a Applied
	if err := json.Unmarshal(b, &a); err != nil {
		return nil, err
	}
	return &a, nil
}

// SaveApplied writes the last applied record.
func (s *Store) SaveApplied(env string, a *Applied) error {
	return config.WriteJSON(filepath.Join(s.envDir(env), "applied.json"), a)
}

// ClearApplied forgets the applied record (used when a reset fails midway).
func (s *Store) ClearApplied(env string) error {
	err := os.Remove(filepath.Join(s.envDir(env), "applied.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// AppendSample records a probe observation.
func (s *Store) AppendSample(env string, sm Sample) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLoadedLocked(env)
	all := s.samples[env]
	// Keep the in-memory slice sorted so range queries can binary search.
	if n := len(all); n == 0 || !sm.At.Before(all[n-1].At) {
		all = append(all, sm)
	} else {
		i := sort.Search(n, func(i int) bool { return all[i].At.After(sm.At) })
		all = append(all, Sample{})
		copy(all[i+1:], all[i:])
		all[i] = sm
	}
	s.samples[env] = all
	dir := s.envDir(env)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, "samples.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	b, _ := json.Marshal(sm)
	_, err = f.Write(append(b, '\n'))
	return err
}

// AppendSamples records several observations taken at once.
func (s *Store) AppendSamples(env string, sms []Sample) error {
	for _, sm := range sms {
		if err := s.AppendSample(env, sm); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) ensureLoadedLocked(env string) {
	if s.loaded[env] {
		return
	}
	s.loaded[env] = true
	f, err := os.Open(filepath.Join(s.envDir(env), "samples.jsonl"))
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var out []Sample
	for sc.Scan() {
		var sm Sample
		if json.Unmarshal(sc.Bytes(), &sm) == nil && !sm.At.IsZero() {
			out = append(out, sm)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	s.samples[env] = out
}

// Samples returns samples for env since a time (all when zero), oldest first.
func (s *Store) Samples(env string, since time.Time) []Sample {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLoadedLocked(env)
	all := s.samples[env]
	i := sort.Search(len(all), func(i int) bool { return !all[i].At.Before(since) })
	out := make([]Sample, len(all)-i)
	copy(out, all[i:])
	return out
}

// Latest returns the most recent sample per dependency.
func (s *Store) Latest(env string) map[string]Sample {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLoadedLocked(env)
	out := map[string]Sample{}
	for _, sm := range s.samples[env] {
		out[sm.Dep] = sm
	}
	return out
}

// Compact drops samples older than retention from memory and disk.
func (s *Store) Compact(env string, retention time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLoadedLocked(env)
	cut := time.Now().Add(-retention)
	all := s.samples[env]
	i := sort.Search(len(all), func(i int) bool { return !all[i].At.Before(cut) })
	if i == 0 {
		return nil
	}
	kept := append([]Sample(nil), all[i:]...)
	s.samples[env] = kept
	path := filepath.Join(s.envDir(env), "samples.jsonl")
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	for _, sm := range kept {
		b, _ := json.Marshal(sm)
		w.Write(append(b, '\n'))
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	f.Close()
	return os.Rename(tmp, path)
}

// ComputeUptime folds samples for one dependency into an Uptime.
func ComputeUptime(dep string, samples []Sample, window time.Duration, now time.Time, points int) Uptime {
	u := Uptime{Dep: dep, Window: window.String(), Incidents: []Incident{}}
	since := now.Add(-window)
	var latencies []int64
	var cur *Incident
	for i := range samples {
		sm := samples[i]
		if sm.Dep != dep || sm.At.Before(since) {
			continue
		}
		u.Total++
		if sm.OK {
			u.OK++
			latencies = append(latencies, sm.MS)
			t := sm.At
			u.LastOK = &t
			if cur != nil {
				cur.End = sm.At
				cur.Open = false
				cur.Duration = cur.End.Sub(cur.Start)
				u.Incidents = append(u.Incidents, *cur)
				cur = nil
			}
		} else {
			t := sm.At
			u.LastFail = &t
			if cur == nil {
				cur = &Incident{Dep: dep, Start: sm.At, Open: true, Message: sm.Msg}
			}
			cur.Samples++
			if sm.Msg != "" {
				cur.Message = sm.Msg
			}
		}
		last := sm
		u.Latest = &last
	}
	if cur != nil {
		cur.Duration = now.Sub(cur.Start)
		u.Incidents = append(u.Incidents, *cur)
	}
	if u.Total > 0 {
		u.Percent = 100 * float64(u.OK) / float64(u.Total)
	}
	if len(latencies) > 0 {
		var sum int64
		for _, l := range latencies {
			sum += l
		}
		u.AvgMS = sum / int64(len(latencies))
		sorted := append([]int64(nil), latencies...)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
		idx := int(float64(len(sorted)-1) * 0.95)
		u.P95MS = sorted[idx]
	}
	u.Points = bucketise(dep, samples, since, now, points)
	return u
}

// bucketise splits the window into n buckets and marks each 1 (all ok),
// 0 (any failure) or -1 (no samples).
func bucketise(dep string, samples []Sample, since, now time.Time, n int) []int8 {
	if n <= 0 {
		n = 48
	}
	out := make([]int8, n)
	for i := range out {
		out[i] = -1
	}
	span := now.Sub(since)
	if span <= 0 {
		return out
	}
	for _, sm := range samples {
		if sm.Dep != dep || sm.At.Before(since) || sm.At.After(now) {
			continue
		}
		idx := int(float64(n) * float64(sm.At.Sub(since)) / float64(span))
		if idx >= n {
			idx = n - 1
		}
		if !sm.OK {
			out[idx] = 0
		} else if out[idx] == -1 {
			out[idx] = 1
		}
	}
	return out
}

// NewRunID returns a sortable unique id: timestamp plus random suffix.
func NewRunID(now time.Time) string {
	var b [4]byte
	rand.Read(b[:])
	return now.UTC().Format("20060102-150405") + "-" + hex.EncodeToString(b[:])
}

// SaveRun writes a run file.
func (s *Store) SaveRun(r *Run) error {
	if r.ID == "" || r.Env == "" {
		return errors.New("run needs id and env")
	}
	return config.WriteJSON(filepath.Join(s.envDir(r.Env), "runs", r.ID+".json"), r)
}

// DeleteRun removes a run file (used when a run is refused before it starts).
func (s *Store) DeleteRun(env, id string) error {
	if !config.ValidName(env) || strings.ContainsAny(id, `/\`) || strings.Contains(id, "..") {
		return errors.New("bad run reference")
	}
	err := os.Remove(filepath.Join(s.envDir(env), "runs", id+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// LoadRun reads a run file.
func (s *Store) LoadRun(env, id string) (*Run, error) {
	if !config.ValidName(env) {
		return nil, errors.New("bad environment name")
	}
	if strings.ContainsAny(id, `/\`) || strings.Contains(id, "..") {
		return nil, errors.New("bad run id")
	}
	b, err := os.ReadFile(filepath.Join(s.envDir(env), "runs", id+".json"))
	if err != nil {
		return nil, err
	}
	var r Run
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// ListRuns returns the most recent runs, newest first.
func (s *Store) ListRuns(env string, limit int) ([]RunSummary, error) {
	if !config.ValidName(env) {
		return nil, errors.New("bad environment name")
	}
	dir := filepath.Join(s.envDir(env), "runs")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []RunSummary{}, nil
		}
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			names = append(names, strings.TrimSuffix(e.Name(), ".json"))
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	if limit > 0 && len(names) > limit {
		names = names[:limit]
	}
	out := make([]RunSummary, 0, len(names))
	for _, n := range names {
		r, err := s.LoadRun(env, n)
		if err != nil {
			continue
		}
		out = append(out, r.Brief())
	}
	return out, nil
}

// PruneRuns keeps at most keep run files per environment.
func (s *Store) PruneRuns(env string, keep int) error {
	dir := filepath.Join(s.envDir(env), "runs")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for len(names) > keep {
		os.Remove(filepath.Join(dir, names[0]))
		names = names[1:]
	}
	return nil
}

// Snapshots lists captured snapshot records.
func (s *Store) Snapshots(env string) ([]SnapshotRecord, error) {
	b, err := os.ReadFile(filepath.Join(s.envDir(env), "snapshots.json"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []SnapshotRecord{}, nil
		}
		return nil, err
	}
	var out []SnapshotRecord
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// SaveSnapshot records a snapshot, replacing any earlier one for the same
// dependency and strategy.
func (s *Store) SaveSnapshot(env string, rec SnapshotRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.Snapshots(env)
	if err != nil {
		return err
	}
	kept := list[:0]
	for _, r := range list {
		if !(r.Dep == rec.Dep && r.Strategy == rec.Strategy) {
			kept = append(kept, r)
		}
	}
	kept = append(kept, rec)
	return config.WriteJSON(filepath.Join(s.envDir(env), "snapshots.json"), kept)
}

// Snapshot finds the record for a dependency.
func (s *Store) Snapshot(env, dep string) (*SnapshotRecord, error) {
	list, err := s.Snapshots(env)
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].Dep == dep {
			return &list[i], nil
		}
	}
	return nil, nil
}

// AppendHeal records a heal attempt (bounded to the last 200).
func (s *Store) AppendHeal(env string, ev HealEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := filepath.Join(s.envDir(env), "heals.json")
	var list []HealEvent
	if b, err := os.ReadFile(path); err == nil {
		json.Unmarshal(b, &list)
	}
	list = append(list, ev)
	if len(list) > 200 {
		list = list[len(list)-200:]
	}
	return config.WriteJSON(path, list)
}

// Heals lists recorded heal attempts, newest first.
func (s *Store) Heals(env string, limit int) ([]HealEvent, error) {
	b, err := os.ReadFile(filepath.Join(s.envDir(env), "heals.json"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []HealEvent{}, nil
		}
		return nil, err
	}
	var list []HealEvent
	if err := json.Unmarshal(b, &list); err != nil {
		return nil, err
	}
	for i, j := 0, len(list)-1; i < j; i, j = i+1, j-1 {
		list[i], list[j] = list[j], list[i]
	}
	if limit > 0 && len(list) > limit {
		list = list[:limit]
	}
	return list, nil
}

// LastHeal returns the time of the most recent heal for a dependency.
func (s *Store) LastHeal(env, dep string) time.Time {
	list, _ := s.Heals(env, 0)
	for _, h := range list {
		if h.Dep == dep {
			return h.At
		}
	}
	return time.Time{}
}

// String renders a fingerprint for display.
func (f Fingerprint) String() string {
	if f.Detail != "" {
		return fmt.Sprintf("%s %s (%s)", f.Kind, f.Value, f.Detail)
	}
	return fmt.Sprintf("%s %s", f.Kind, f.Value)
}
