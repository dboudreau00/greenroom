package engine

import (
	"errors"
	"fmt"

	"github.com/dboudreau00/greenroom/internal/seeds"
)

// ErrBadRef is wrapped by DiffSets when a seed set reference does not parse.
var ErrBadRef = errors.New("bad seed set reference")

// DiffSets compares two seed sets of an environment given as persona@vN
// references (a bare persona means its latest version). Unknown sets wrap
// seeds.ErrNotFound.
func (e *Engine) DiffSets(v *Env, from, to string) (*seeds.SetDiff, error) {
	a, err := e.findRef(v, from)
	if err != nil {
		return nil, err
	}
	b, err := e.findRef(v, to)
	if err != nil {
		return nil, err
	}
	return seeds.Diff(a, b)
}

func (e *Engine) findRef(v *Env, ref string) (*seeds.Set, error) {
	if ref == "" {
		return nil, fmt.Errorf("%w: empty (want persona@v3)", ErrBadRef)
	}
	persona, version, err := seeds.ParseRef(ref)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadRef, err)
	}
	set, ok := seeds.Find(v.Seeds, persona, version)
	if !ok {
		return nil, fmt.Errorf("%w: seed set %s in %s", seeds.ErrNotFound, ref, v.Config.Name)
	}
	return set, nil
}

// BumpRequest asks for the next version of a persona's seed set.
type BumpRequest struct {
	Persona string
	From    int    // 0 copies the latest version
	Title   string // empty keeps the source title
	Pin     bool   // point the environment's pin at the new version
	Actor   string
}

// BumpResult names the new seed set.
type BumpResult struct {
	ID     string `json:"id"`
	From   string `json:"from"`
	Dir    string `json:"dir"`
	Pinned bool   `json:"pinned"`
}

// BumpSet copies a seed set to the next version, optionally pins the
// environment to it, and reloads. It only writes files: a reset in flight
// keeps the set it loaded, so a busy environment is not refused.
func (e *Engine) BumpSet(env string, req BumpRequest) (*BumpResult, error) {
	v, err := e.Env(env)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", seeds.ErrNotFound, err)
	}
	e.createMu.Lock()
	defer e.createMu.Unlock()
	from := req.From
	if from == 0 {
		versions, err := seeds.Versions(v.Config.Dir, req.Persona)
		if err == nil && len(versions) > 0 {
			from = versions[len(versions)-1]
		}
	}
	dir, version, err := seeds.Bump(v.Config.Dir, req.Persona, from, req.Title)
	if err != nil {
		return nil, err
	}
	res := &BumpResult{ID: fmt.Sprintf("%s@v%d", req.Persona, version), From: fmt.Sprintf("%s@v%d", req.Persona, from), Dir: dir}
	var pinErr error
	if req.Pin {
		if pinErr = seeds.Pin(v.Config.Dir, req.Persona, version); pinErr == nil {
			res.Pinned = true
		}
	}
	if err := e.Reload(); err != nil {
		e.log.Warn("reload after seed bump", "env", env, "err", err)
	}
	e.log.Info("seed set bumped", "env", env, "from", res.From, "to", res.ID, "pinned", res.Pinned, "actor", req.Actor)
	if pinErr != nil {
		return res, fmt.Errorf("created %s but could not pin it: %w", res.ID, pinErr)
	}
	return res, nil
}
