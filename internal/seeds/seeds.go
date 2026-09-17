// Package seeds discovers versioned seed sets. A seed set is a directory
// environments/<env>/seeds/<persona>/v<N>/ holding a seed.json manifest and
// the files its steps reference. The set's identity is the content hash of
// every file in the directory, so any edit shows up as drift.
package seeds

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/dboudreau00/greenroom/internal/config"
)

// Step kinds.
const (
	StepSQL     = "sql"     // run a SQL file or inline statement against a dependency
	StepShell   = "shell"   // run a script through a target's executor
	StepCommand = "command" // run argv through a target's executor
	StepHTTP    = "http"    // send an HTTP request, optionally with a body file
	StepCopy    = "copy"    // copy a file to a target (docker, kubernetes, ssh)
	StepWait    = "wait"    // wait for a dependency probe to pass
)

// Step is one unit of work in a seed set.
type Step struct {
	Name            string            `json:"name"`
	Kind            string            `json:"kind"`
	Dependency      string            `json:"dependency,omitempty"`
	File            string            `json:"file,omitempty"`
	SQL             string            `json:"sql,omitempty"`
	Script          string            `json:"script,omitempty"`
	Command         []string          `json:"command,omitempty"`
	Target          string            `json:"target,omitempty"`
	Container       string            `json:"container,omitempty"`
	Pod             string            `json:"pod,omitempty"`
	Namespace       string            `json:"namespace,omitempty"`
	InstanceID      string            `json:"instanceId,omitempty"`
	Method          string            `json:"method,omitempty"`
	URL             string            `json:"url,omitempty"`
	Headers         map[string]string `json:"headers,omitempty"`
	Body            string            `json:"body,omitempty"`
	BodyFile        string            `json:"bodyFile,omitempty"`
	ExpectStatus    int               `json:"expectStatus,omitempty"`
	Insecure        bool              `json:"insecure,omitempty"`
	Dest            string            `json:"dest,omitempty"`
	Timeout         config.Duration   `json:"timeout,omitempty"`
	ContinueOnError bool              `json:"continueOnError,omitempty"`
	Env             map[string]string `json:"env,omitempty"`
}

// Check is a post-apply verification.
type Check struct {
	Name       string   `json:"name"`
	Kind       string   `json:"kind"` // sql | http | command
	Dependency string   `json:"dependency,omitempty"`
	Query      string   `json:"query,omitempty"`
	Expect     *float64 `json:"expect,omitempty"`
	Min        *float64 `json:"min,omitempty"`
	Max        *float64 `json:"max,omitempty"`
	Tolerance  float64  `json:"tolerance,omitempty"`
	URL        string   `json:"url,omitempty"`
	ExpectStatus int    `json:"expectStatus,omitempty"`
	ExpectBody string   `json:"expectBody,omitempty"`
	Target     string   `json:"target,omitempty"`
	Command    []string `json:"command,omitempty"`
}

// Manifest is the seed.json file.
type Manifest struct {
	Persona     string   `json:"persona,omitempty"`
	Version     int      `json:"version,omitempty"`
	Title       string   `json:"title,omitempty"`
	Description string   `json:"description,omitempty"`
	Vertical    string   `json:"vertical,omitempty"`
	Author      string   `json:"author,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Steps       []Step   `json:"steps"`
	Verify      []Check  `json:"verify,omitempty"`
	// Dependencies limits which dependencies get their reset strategy run
	// before this set applies. Empty means every dependency a step touches.
	Dependencies []string `json:"dependencies,omitempty"`
}

// Set is a discovered seed set.
type Set struct {
	Manifest
	Dir   string       `json:"dir"`
	Hash  string       `json:"hash"`
	Files []FileDigest `json:"files"`
	// Problems lists validation issues; a set with problems is listed but
	// cannot be applied.
	Problems []string `json:"problems,omitempty"`
}

// FileDigest records one file inside a seed set.
type FileDigest struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
	Hash string `json:"hash"`
}

// ID is persona@vN.
func (s *Set) ID() string { return fmt.Sprintf("%s@v%d", s.Persona, s.Version) }

// DisplayTitle returns the title or the id.
func (s *Set) DisplayTitle() string {
	if s.Title != "" {
		return s.Title
	}
	return s.ID()
}

// ReadFile reads a file relative to the set directory, refusing to escape it.
func (s *Set) ReadFile(rel string) ([]byte, error) {
	p, err := s.Resolve(rel)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(p)
}

// Resolve returns the absolute path of a file inside the set.
func (s *Set) Resolve(rel string) (string, error) {
	if rel == "" {
		return "", errors.New("empty file name")
	}
	clean := filepath.Clean(filepath.FromSlash(rel))
	if filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
		return "", fmt.Errorf("file %q must be inside the seed set", rel)
	}
	return filepath.Join(s.Dir, clean), nil
}

var versionRe = regexp.MustCompile(`^v(\d+)$`)

// Discover finds every seed set under envDir/seeds.
func Discover(envDir string) ([]*Set, error) {
	root := filepath.Join(envDir, config.SeedsDirName)
	personas, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var sets []*Set
	for _, p := range personas {
		if !p.IsDir() || strings.HasPrefix(p.Name(), ".") {
			continue
		}
		pdir := filepath.Join(root, p.Name())
		versions, err := os.ReadDir(pdir)
		if err != nil {
			return nil, err
		}
		for _, v := range versions {
			if !v.IsDir() {
				continue
			}
			m := versionRe.FindStringSubmatch(v.Name())
			if m == nil {
				continue
			}
			n, _ := strconv.Atoi(m[1])
			set, err := Load(filepath.Join(pdir, v.Name()))
			if err != nil {
				return nil, err
			}
			if set.Persona == "" {
				set.Persona = p.Name()
			}
			if set.Version == 0 {
				set.Version = n
			}
			if set.Persona != p.Name() {
				set.Problems = append(set.Problems, fmt.Sprintf("manifest persona %q does not match directory %q", set.Persona, p.Name()))
			}
			if set.Version != n {
				set.Problems = append(set.Problems, fmt.Sprintf("manifest version %d does not match directory %s", set.Version, v.Name()))
			}
			sets = append(sets, set)
		}
	}
	sort.Slice(sets, func(i, j int) bool {
		if sets[i].Persona != sets[j].Persona {
			return sets[i].Persona < sets[j].Persona
		}
		return sets[i].Version < sets[j].Version
	})
	return sets, nil
}

// Load reads a single seed set directory.
func Load(dir string) (*Set, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	set := &Set{Dir: abs}
	b, err := os.ReadFile(filepath.Join(abs, config.SeedFile))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			set.Problems = append(set.Problems, "missing "+config.SeedFile)
		} else {
			return nil, err
		}
	} else if err := json.Unmarshal(config.StripComments(b), &set.Manifest); err != nil {
		set.Problems = append(set.Problems, fmt.Sprintf("%s: %v", config.SeedFile, err))
	}
	files, hash, err := digest(abs)
	if err != nil {
		return nil, err
	}
	set.Files = files
	set.Hash = hash
	set.Problems = append(set.Problems, set.validate()...)
	return set, nil
}

func (s *Set) validate() []string {
	var problems []string
	if !config.ValidName(s.Persona) && s.Persona != "" {
		problems = append(problems, fmt.Sprintf("persona %q is not a valid name", s.Persona))
	}
	if len(s.Steps) == 0 {
		problems = append(problems, "no steps")
	}
	names := map[string]bool{}
	for i, st := range s.Steps {
		where := fmt.Sprintf("step[%d]", i)
		if st.Name != "" {
			where = "step " + st.Name
		}
		if st.Name == "" {
			problems = append(problems, where+": missing name")
		} else if names[st.Name] {
			problems = append(problems, where+": duplicate name")
		}
		names[st.Name] = true
		switch st.Kind {
		case StepSQL:
			if st.Dependency == "" {
				problems = append(problems, where+": sql step needs dependency")
			}
			if st.File == "" && st.SQL == "" {
				problems = append(problems, where+": sql step needs file or sql")
			}
		case StepShell:
			if st.Script == "" && st.File == "" {
				problems = append(problems, where+": shell step needs script or file")
			}
		case StepCommand:
			if len(st.Command) == 0 {
				problems = append(problems, where+": command step needs command")
			}
		case StepHTTP:
			if st.URL == "" {
				problems = append(problems, where+": http step needs url")
			}
		case StepCopy:
			if st.File == "" || st.Dest == "" {
				problems = append(problems, where+": copy step needs file and dest")
			}
			if st.Target == "" {
				problems = append(problems, where+": copy step needs target")
			}
		case StepWait:
			if st.Dependency == "" {
				problems = append(problems, where+": wait step needs dependency")
			}
		default:
			problems = append(problems, fmt.Sprintf("%s: unknown kind %q", where, st.Kind))
		}
		if st.File != "" {
			if _, err := s.Resolve(st.File); err != nil {
				problems = append(problems, where+": "+err.Error())
			} else if _, err := os.Stat(filepath.Join(s.Dir, filepath.FromSlash(st.File))); err != nil {
				problems = append(problems, fmt.Sprintf("%s: file %q not found", where, st.File))
			}
		}
		if st.BodyFile != "" {
			if _, err := os.Stat(filepath.Join(s.Dir, filepath.FromSlash(st.BodyFile))); err != nil {
				problems = append(problems, fmt.Sprintf("%s: bodyFile %q not found", where, st.BodyFile))
			}
		}
	}
	for i, c := range s.Verify {
		where := fmt.Sprintf("verify[%d]", i)
		switch c.Kind {
		case "sql":
			if c.Dependency == "" || c.Query == "" {
				problems = append(problems, where+": sql check needs dependency and query")
			}
			if c.Expect == nil && c.Min == nil && c.Max == nil {
				problems = append(problems, where+": needs expect, min or max")
			}
		case "http":
			if c.URL == "" {
				problems = append(problems, where+": http check needs url")
			}
		case "command":
			if len(c.Command) == 0 {
				problems = append(problems, where+": command check needs command")
			}
		default:
			problems = append(problems, fmt.Sprintf("%s: unknown kind %q", where, c.Kind))
		}
	}
	return problems
}

// digest hashes every regular file under dir, in sorted path order.
func digest(dir string) ([]FileDigest, string, error) {
	var files []FileDigest
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != dir && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		rel = filepath.ToSlash(rel)
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		h := sha256.New()
		n, err := io.Copy(h, f)
		if err != nil {
			return err
		}
		files = append(files, FileDigest{Path: rel, Size: n, Hash: hex.EncodeToString(h.Sum(nil))})
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	h := sha256.New()
	for _, f := range files {
		fmt.Fprintf(h, "%s\x00%d\x00%s\n", f.Path, f.Size, f.Hash)
	}
	return files, hex.EncodeToString(h.Sum(nil)), nil
}

// Find returns the set for persona at version, or the latest version when
// version is 0.
func Find(sets []*Set, persona string, version int) (*Set, bool) {
	var best *Set
	for _, s := range sets {
		if s.Persona != persona {
			continue
		}
		if version != 0 {
			if s.Version == version {
				return s, true
			}
			continue
		}
		if best == nil || s.Version > best.Version {
			best = s
		}
	}
	return best, best != nil
}

// Latest returns the highest version per persona.
func Latest(sets []*Set) map[string]*Set {
	out := map[string]*Set{}
	for _, s := range sets {
		if cur, ok := out[s.Persona]; !ok || s.Version > cur.Version {
			out[s.Persona] = s
		}
	}
	return out
}

// Personas lists persona names in order.
func Personas(sets []*Set) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range sets {
		if !seen[s.Persona] {
			seen[s.Persona] = true
			out = append(out, s.Persona)
		}
	}
	return out
}

// ParseRef parses "persona", "persona@v3" or "persona@3".
func ParseRef(ref string) (persona string, version int, err error) {
	persona, v, ok := strings.Cut(ref, "@")
	if !ok {
		return persona, 0, nil
	}
	v = strings.TrimPrefix(v, "v")
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return "", 0, fmt.Errorf("bad seed version in %q (want persona@v3)", ref)
	}
	return persona, n, nil
}
