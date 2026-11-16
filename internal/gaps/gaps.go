// Package gaps keeps the workspace gap registry: the things prospects asked
// for that the product or the demo environment does not have. Each gap is
// one file in the state directory:
//
//	state/_gaps/<id>.json
//
// Mentions are not stored here. They live in demo debriefs, and the engine
// derives them on every read; this package scores, matches and exports
// them.
package gaps

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

// Kinds, statuses and severities.
var (
	Kinds      = []string{"feature", "data", "integration", "environment", "content", "performance", "security", "other"}
	Statuses   = []string{"open", "planned", "in-progress", "done", "wont-do"}
	Severities = []string{"low", "medium", "high", "blocker"}
)

// KindEnvironment is the kind of gap that points back at a Greenroom
// environment and, optionally, one of its template modules.
const KindEnvironment = "environment"

// StatusOpen is the status of a new gap; StatusWontDo is set on a gap that
// was merged into another.
const (
	StatusOpen   = "open"
	StatusWontDo = "wont-do"
)

// Limits on free text.
const (
	MaxTitle = 120
	MaxOwner = 80
	MaxNotes = 4000
)

// Weight is the score a severity adds for one prospect.
func Weight(severity string) int {
	switch severity {
	case "low":
		return 1
	case "medium":
		return 2
	case "high":
		return 3
	case "blocker":
		return 5
	}
	return 0
}

// Active reports whether a status still asks for work: suggestions and the
// default merge picker only offer these.
func Active(status string) bool {
	return status == "open" || status == "planned" || status == "in-progress"
}

// Gap is one entry of the registry.
type Gap struct {
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	Kind       string    `json:"kind"`
	Status     string    `json:"status"`
	Owner      string    `json:"owner,omitempty"`
	Notes      string    `json:"notes,omitempty"`
	Env        string    `json:"env,omitempty"`
	Module     string    `json:"module,omitempty"`
	MergedInto string    `json:"mergedInto,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
	CreatedBy  string    `json:"createdBy,omitempty"`
}

// ErrNotFound is returned for an unknown gap.
var ErrNotFound = errors.New("gap not found")

// ErrState is returned when a gap can not change the way asked, such as
// editing or merging a gap that was already merged.
var ErrState = errors.New("not allowed in this state")

// ErrUnchanged tells Update that fn decided not to change anything.
var ErrUnchanged = errors.New("unchanged")

// DirName is the gaps directory inside the state directory. It does not
// match config.ValidName, so it can never collide with an environment.
const DirName = "_gaps"

var idRe = regexp.MustCompile(`^gap-[0-9a-f]{8}$`)

// ValidID reports whether s can name a gap file.
func ValidID(s string) bool { return idRe.MatchString(s) }

// Store is the gaps directory. The mutex serializes writes inside one
// process; every update reads the file fresh before changing it.
type Store struct {
	dir string
	mu  sync.Mutex
}

// Open returns the store under stateDir.
func Open(stateDir string) *Store { return &Store{dir: filepath.Join(stateDir, DirName)} }

func (s *Store) path(id string) (string, error) {
	if !ValidID(id) {
		return "", fmt.Errorf("%w: bad gap id %q", ErrNotFound, id)
	}
	return filepath.Join(s.dir, id+".json"), nil
}

// Get loads one gap.
func (s *Store) Get(id string) (*Gap, error) {
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
	g := &Gap{}
	if err := json.Unmarshal(b, g); err != nil {
		return nil, fmt.Errorf("gap %s: %w", id, err)
	}
	if g.ID != id {
		return nil, fmt.Errorf("gap %s: file names another id", id)
	}
	return g, nil
}

// List reads every gap, sorted by creation time.
func (s *Store) List() ([]*Gap, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []*Gap{}, nil
		}
		return nil, err
	}
	out := []*Gap{}
	for _, e := range entries {
		id := strings.TrimSuffix(e.Name(), ".json")
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") || !ValidID(id) {
			continue
		}
		g, err := s.Get(id)
		if err != nil {
			continue
		}
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// Map returns every gap by id.
func (s *Store) Map() (map[string]*Gap, error) {
	list, err := s.List()
	if err != nil {
		return nil, err
	}
	m := make(map[string]*Gap, len(list))
	for _, g := range list {
		m[g.ID] = g
	}
	return m, nil
}

func (s *Store) save(g *Gap) error {
	p, err := s.path(g.ID)
	if err != nil {
		return err
	}
	return config.WriteJSON(p, g)
}

// Create assigns a fresh id and writes a new gap.
func (s *Store) Create(g *Gap) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for range 8 {
		id := "gap-" + newHex(4)
		p, err := s.path(id)
		if err != nil {
			return err
		}
		if _, err := os.Stat(p); err == nil {
			continue
		}
		g.ID = id
		return s.save(g)
	}
	return errors.New("could not pick a free gap id")
}

// Update loads a gap, applies fn and writes it back. Returning ErrUnchanged
// from fn skips the write.
func (s *Store) Update(id string, fn func(g *Gap) error) (*Gap, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, err := s.Get(id)
	if err != nil {
		return nil, err
	}
	if err := fn(g); err != nil {
		if errors.Is(err, ErrUnchanged) {
			return g, nil
		}
		return nil, err
	}
	if err := s.save(g); err != nil {
		return nil, err
	}
	return g, nil
}

// Delete removes a gap file. The engine only uses it to roll back gaps it
// created for a debrief that then failed to save.
func (s *Store) Delete(id string) error {
	p, err := s.path(id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Resolve follows mergedInto from id to the gap that absorbed it. A chain
// that loops or runs past eight hops resolves to the last gap reached.
func Resolve(all map[string]*Gap, id string) string {
	for range 8 {
		g, ok := all[id]
		if !ok || g.MergedInto == "" || g.MergedInto == id {
			return id
		}
		id = g.MergedInto
	}
	return id
}

// AppendNote adds a line to notes, dropping the oldest text when the result
// would pass MaxNotes.
func AppendNote(notes, line string) string {
	if notes != "" {
		notes += "\n"
	}
	notes += line
	if r := []rune(notes); len(r) > MaxNotes {
		notes = strings.TrimLeft(string(r[len(r)-MaxNotes:]), "\n")
	}
	return notes
}

func newHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}
