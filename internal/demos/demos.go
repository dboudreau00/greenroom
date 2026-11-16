// Package demos keeps demo records: a prospect, an environment, a seed set,
// an optional brand kit and a time slot, with a status and a journal. Each
// record is one file in the state directory:
//
//	state/_demos/<id>.json
//
// The engine owns the booking rules and the scheduler that prepares a
// booked demo; this package only stores, cleans and lists records.
package demos

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dboudreau00/greenroom/internal/config"
)

// Statuses. A booked demo moves scheduled, preparing, ready (or
// prep-failed), live, done; a demo nobody prepared before its slot ended is
// missed; any demo that is not final can be cancelled.
const (
	Scheduled  = "scheduled"
	Preparing  = "preparing"
	Ready      = "ready"
	PrepFailed = "prep-failed"
	Live       = "live"
	Done       = "done"
	Missed     = "missed"
	Cancelled  = "cancelled"
)

// Cleanup states of a demo booked with resetAfter.
const (
	CleanupPending = "pending"
	CleanupStarted = "started"
	CleanupSkipped = "skipped"
	CleanupFailed  = "failed"
)

// Stages, outcomes and journal kinds.
var (
	Stages   = []string{"intro", "discovery", "deep-dive", "poc", "executive", "other"}
	Outcomes = []string{"positive", "neutral", "negative", "no-show", "rescheduled"}
	Kinds    = []string{"note", "issue", "question", "follow-up", "feedback", "system"}
)

// Limits on free text and lists.
const (
	MaxTitle     = 120
	MaxProspect  = 120
	MaxOwner     = 80
	MaxAttendee  = 80
	MaxAttendees = 20
	MaxNextStep  = 500
	MaxEntryText = 4000
	MaxEntries   = 500
)

// DefaultStage is the stage of a demo booked without one.
const DefaultStage = "discovery"

// Entry is one journal line.
type Entry struct {
	ID     string    `json:"id"`
	At     time.Time `json:"at"`
	Author string    `json:"author"`
	Kind   string    `json:"kind"`
	Text   string    `json:"text"`
	RunID  string    `json:"runId,omitempty"`
}

// Demo is one booked or logged demo.
type Demo struct {
	ID         string          `json:"id"`
	Title      string          `json:"title"`
	Env        string          `json:"env"`
	Prospect   string          `json:"prospect,omitempty"`
	Brand      string          `json:"brand,omitempty"`
	Persona    string          `json:"persona"`
	Version    int             `json:"version"`
	Owner      string          `json:"owner,omitempty"`
	Attendees  []string        `json:"attendees"`
	Booked     bool            `json:"booked"`
	Start      time.Time       `json:"start"`
	End        time.Time       `json:"end"`
	PrepLead   config.Duration `json:"prepLead"`
	ResetAfter bool            `json:"resetAfter"`
	Status     string          `json:"status"`
	Stage      string          `json:"stage"`
	Outcome    string          `json:"outcome,omitempty"`
	NextStep   string          `json:"nextStep,omitempty"`
	PrepRunID  string          `json:"prepRunId,omitempty"`
	CheckRunID string          `json:"checkRunId,omitempty"`
	// CleanupRunID is the reset to the pin after the demo, when resetAfter
	// is set; Cleanup says where that stands.
	CleanupRunID string    `json:"cleanupRunId,omitempty"`
	Cleanup      string    `json:"cleanup,omitempty"`
	Readiness    string    `json:"readiness,omitempty"`
	Error        string    `json:"error,omitempty"`
	Journal      []Entry   `json:"journal"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
	CreatedBy    string    `json:"createdBy,omitempty"`
}

// SetID is the seed set reference, persona@vN.
func (d *Demo) SetID() string {
	if d.Persona == "" {
		return ""
	}
	return fmt.Sprintf("%s@v%d", d.Persona, d.Version)
}

// Final reports whether the demo can no longer change status.
func (d *Demo) Final() bool { return Final(d.Status) }

// Final reports whether a status is final.
func Final(status string) bool { return status == Done || status == Missed || status == Cancelled }

// Holds reports whether a booked demo holds its slot on the environment,
// so another booking can not overlap it.
func (d *Demo) Holds() bool { return d.Booked && !d.Final() }

// Protects reports whether the demo is on stage at now: a reset or
// snapshot of its environment would pull the floor out from under it.
func (d *Demo) Protects(now time.Time) bool { return OnStage(d.Booked, d.Status, d.Start, d.End, now) }

// Protects is Demo.Protects for the short form.
func (b Brief) Protects(now time.Time) bool { return OnStage(b.Booked, b.Status, b.Start, b.End, now) }

// OnStage reports whether a booked demo with this status and slot is
// running at now. Prepared demos count from their start time even before
// the scheduler marks them live, and nothing counts after the slot ends, so
// a demo left live by a stopped portal never blocks resets for good.
func OnStage(booked bool, status string, start, end, now time.Time) bool {
	if !booked || now.Before(start) || !now.Before(end) {
		return false
	}
	return status == Live || status == Ready || status == PrepFailed
}

// Overlaps reports whether two slots share any time.
func (d *Demo) Overlaps(start, end time.Time) bool { return d.Start.Before(end) && start.Before(d.End) }

// AddEntry appends a journal entry with a fresh id.
func (d *Demo) AddEntry(e Entry) Entry {
	e.ID = newHex(4)
	d.Journal = append(d.Journal, e)
	return e
}

// Brief is the short form used in listings, cards and headers.
type Brief struct {
	ID           string    `json:"id"`
	Title        string    `json:"title"`
	Env          string    `json:"env"`
	Prospect     string    `json:"prospect,omitempty"`
	Brand        string    `json:"brand,omitempty"`
	Persona      string    `json:"persona"`
	Version      int       `json:"version"`
	Owner        string    `json:"owner,omitempty"`
	Booked       bool      `json:"booked"`
	Start        time.Time `json:"start"`
	End          time.Time `json:"end"`
	Status       string    `json:"status"`
	Stage        string    `json:"stage"`
	Outcome      string    `json:"outcome,omitempty"`
	NextStep     string    `json:"nextStep,omitempty"`
	Readiness    string    `json:"readiness,omitempty"`
	JournalCount int       `json:"journalCount"`
}

// Brief returns the short form.
func (d *Demo) Brief() Brief {
	return Brief{ID: d.ID, Title: d.Title, Env: d.Env, Prospect: d.Prospect, Brand: d.Brand, Persona: d.Persona, Version: d.Version,
		Owner: d.Owner, Booked: d.Booked, Start: d.Start, End: d.End, Status: d.Status, Stage: d.Stage, Outcome: d.Outcome,
		NextStep: d.NextStep, Readiness: d.Readiness, JournalCount: len(d.Journal)}
}

// ErrNotFound is returned for an unknown demo or journal entry.
var ErrNotFound = errors.New("demo not found")

// ErrConflict is returned when a booking overlaps another one.
var ErrConflict = errors.New("booking conflict")

// ErrState is returned when the demo's status does not allow a change.
var ErrState = errors.New("not allowed in this state")

// DirName is the demos directory inside the state directory. It does not
// match config.ValidName, so it can never collide with an environment.
const DirName = "_demos"

var idRe = regexp.MustCompile(`^[0-9]{8}-[0-9]{4}-[0-9a-f]{4}$`)

var entryIDRe = regexp.MustCompile(`^[0-9a-f]{8}$`)

// ValidID reports whether s can name a demo file.
func ValidID(s string) bool { return idRe.MatchString(s) }

// ValidEntryID reports whether s can name a journal entry.
func ValidEntryID(s string) bool { return entryIDRe.MatchString(s) }

// Store is the demos directory. The mutex serializes writes inside one
// process; the CLI and the portal may both write, and each update reads the
// file fresh before changing it.
type Store struct {
	dir string
	mu  sync.Mutex

	cacheMu   sync.Mutex
	cache     []Brief
	cachedAt  time.Time
	cacheTerm time.Duration
}

// Open returns the store under stateDir.
func Open(stateDir string) *Store {
	return &Store{dir: filepath.Join(stateDir, DirName), cacheTerm: 2 * time.Second}
}

func (s *Store) path(id string) (string, error) {
	if !ValidID(id) {
		return "", fmt.Errorf("%w: bad demo id %q", ErrNotFound, id)
	}
	return filepath.Join(s.dir, id+".json"), nil
}

// Get loads one demo.
func (s *Store) Get(id string) (*Demo, error) {
	p, err := s.path(id)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		return nil, err
	}
	d := &Demo{}
	if err := json.Unmarshal(b, d); err != nil {
		return nil, fmt.Errorf("demo %s: %w", id, err)
	}
	if d.ID != id {
		return nil, fmt.Errorf("demo %s: file names another id", id)
	}
	normalize(d)
	return d, nil
}

func normalize(d *Demo) {
	if d.Attendees == nil {
		d.Attendees = []string{}
	}
	if d.Journal == nil {
		d.Journal = []Entry{}
	}
}

// List reads every demo from disk, sorted by start time. It never serves
// from a cache, so the scheduler sees what the CLI wrote a moment ago.
func (s *Store) List() ([]*Demo, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []*Demo{}, nil
		}
		return nil, err
	}
	out := []*Demo{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") || !ValidID(strings.TrimSuffix(name, ".json")) {
			continue
		}
		d, err := s.Get(strings.TrimSuffix(name, ".json"))
		if err != nil {
			continue
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Start.Equal(out[j].Start) {
			return out[i].Start.Before(out[j].Start)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// Briefs returns the short form of every demo, sorted by start. Results
// are cached for two seconds so an overview of many environments reads the
// directory once; writes from this process clear the cache.
func (s *Store) Briefs() ([]Brief, error) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	if s.cache != nil && time.Since(s.cachedAt) < s.cacheTerm {
		return s.cache, nil
	}
	list, err := s.List()
	if err != nil {
		return nil, err
	}
	out := make([]Brief, 0, len(list))
	for _, d := range list {
		out = append(out, d.Brief())
	}
	s.cache, s.cachedAt = out, time.Now()
	return out, nil
}

func (s *Store) invalidate() {
	s.cacheMu.Lock()
	s.cache = nil
	s.cacheMu.Unlock()
}

func (s *Store) save(d *Demo) error {
	p, err := s.path(d.ID)
	if err != nil {
		return err
	}
	normalize(d)
	err = config.WriteJSON(p, d)
	s.invalidate()
	return err
}

// Create assigns an id from the start time and writes a new demo. check
// runs under the store lock with every stored demo, so an overlap test and
// the write can not interleave with another booking in this process.
func (s *Store) Create(d *Demo, check func(all []*Demo) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if check != nil {
		all, err := s.List()
		if err != nil {
			return err
		}
		if err := check(all); err != nil {
			return err
		}
	}
	for range 8 {
		id := d.Start.UTC().Format("20060102-1504") + "-" + newHex(2)
		p, err := s.path(id)
		if err != nil {
			return err
		}
		if _, err := os.Stat(p); err == nil {
			continue
		}
		d.ID = id
		return s.save(d)
	}
	return errors.New("could not pick a free demo id")
}

// ErrUnchanged tells Update that fn decided not to change anything.
var ErrUnchanged = errors.New("unchanged")

// Update loads a demo, applies fn and writes it back. fn gets every stored
// demo when it needs to test for overlaps. Returning ErrUnchanged from fn
// skips the write and returns the demo as loaded.
func (s *Store) Update(id string, fn func(d *Demo, all func() ([]*Demo, error)) error) (*Demo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.Get(id)
	if err != nil {
		return nil, err
	}
	if err := fn(d, s.List); err != nil {
		if errors.Is(err, ErrUnchanged) {
			return d, nil
		}
		return nil, err
	}
	if err := s.save(d); err != nil {
		return nil, err
	}
	return d, nil
}

// Overlapping returns the first demo in all that holds a slot on env
// overlapping start..end, other than the demo with id skip.
func Overlapping(all []*Demo, env string, start, end time.Time, skip string) *Demo {
	for _, o := range all {
		if o.ID == skip || o.Env != env || !o.Holds() {
			continue
		}
		if o.Overlaps(start, end) {
			return o
		}
	}
	return nil
}

func newHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// OneOf reports whether s is in list.
func OneOf(s string, list []string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// CleanLine makes a single-line field safe to store and show: control and
// invisible separator characters are dropped, whitespace is collapsed and
// the result is capped at max characters.
func CleanLine(s string, max int) string {
	var b strings.Builder
	space := false
	for _, r := range s {
		switch {
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			space = true
			continue
		case badRune(r):
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(r)
	}
	return capRunes(b.String(), max)
}

// CleanText is CleanLine for journal text: newlines are kept (CRLF becomes
// LF), tabs become spaces, and nothing is capped; callers check the length.
func CleanText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\n':
			b.WriteRune(r)
		case r == '\t' || r == '\r':
			b.WriteByte(' ')
		case badRune(r):
		default:
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}

func badRune(r rune) bool {
	return r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) || r == 0x2028 || r == 0x2029 || r == 0xfeff || (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069)
}

func capRunes(s string, max int) string {
	if max > 0 && len([]rune(s)) > max {
		s = strings.TrimSpace(string([]rune(s)[:max]))
	}
	return s
}
