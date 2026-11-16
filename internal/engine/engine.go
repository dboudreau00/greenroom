// Package engine ties configuration, seed sets, targets, probes and state
// together into the operations Greenroom exposes: check, stale, reset,
// snapshot and heal, plus the scheduler that keeps uptime samples flowing.
package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/dboudreau00/greenroom/internal/brand"
	"github.com/dboudreau00/greenroom/internal/config"
	"github.com/dboudreau00/greenroom/internal/demos"
	"github.com/dboudreau00/greenroom/internal/probe"
	"github.com/dboudreau00/greenroom/internal/registry"
	"github.com/dboudreau00/greenroom/internal/seeds"
	"github.com/dboudreau00/greenroom/internal/state"
	"github.com/dboudreau00/greenroom/internal/webfetch"
)

// Env is a loaded environment with its live helpers.
type Env struct {
	Config   *config.Environment
	Seeds    []*seeds.Set
	SeedsErr error
	Registry *registry.Registry
	Prober   *probe.Prober

	// opMu is shared across reloads of the same environment so that a run
	// holding it keeps excluding others after the config is re-read.
	opMu *sync.Mutex
	busy string // id of the run currently mutating this environment
}

// Name returns the environment name.
func (v *Env) Name() string { return v.Config.Name }

// Engine is the application core shared by the CLI and the portal.
type Engine struct {
	ws    *config.Workspace
	store *state.Store
	log   *slog.Logger
	Now   func() time.Time

	mu       sync.RWMutex
	createMu sync.Mutex
	envs     map[string]*Env
	loadErr  error
	latest   map[string]map[string]probe.Result
	active   map[string]*recorder
	brands   *brand.Store
	demos    *demos.Store
	fetcher  *webfetch.Client
	// Version is shown in the UI and the CLI.
	Version string
}

// New creates an engine. Call Reload before use.
func New(ws *config.Workspace, store *state.Store, logger *slog.Logger) *Engine {
	if logger == nil {
		logger = slog.Default()
	}
	return &Engine{ws: ws, store: store, log: logger, Now: time.Now,
		envs: map[string]*Env{}, latest: map[string]map[string]probe.Result{}, active: map[string]*recorder{}, Version: "dev",
		brands:  brand.Open(store.Dir()),
		demos:   demos.Open(store.Dir()),
		fetcher: webfetch.New(webfetch.Options{AllowPrivate: ws.LinkFetch.AllowPrivate, Timeout: ws.LinkFetch.Timeout.Or(15 * time.Second)})}
}

// Workspace returns the workspace config.
func (e *Engine) Workspace() *config.Workspace { return e.ws }

// Store returns the state store.
func (e *Engine) Store() *state.Store { return e.store }

// Reload re-reads every environment and its seed sets. Environments that
// fail to load are dropped and the error is returned (and kept for the UI).
func (e *Engine) Reload() error {
	cfgs, err := config.LoadEnvironments(e.ws.EnvironmentsDir)
	envs := map[string]*Env{}
	var errs []error
	if err != nil {
		errs = append(errs, err)
	}
	for _, c := range cfgs {
		v := &Env{Config: c, opMu: &sync.Mutex{}}
		v.Registry = registry.New(c)
		v.Prober = probe.New(c, v.Registry)
		v.Seeds, v.SeedsErr = seeds.Discover(c.Dir)
		if v.SeedsErr != nil {
			errs = append(errs, fmt.Errorf("%s seeds: %w", c.Name, v.SeedsErr))
		}
		envs[c.Name] = v
	}
	e.mu.Lock()
	// An environment that already exists keeps its lock and busy marker, so
	// a run in flight (which holds the old *Env) still excludes new runs.
	for name, old := range e.envs {
		if nv, ok := envs[name]; ok {
			nv.opMu = old.opMu
			nv.busy = old.busy
		}
	}
	e.envs = envs
	e.loadErr = errors.Join(errs...)
	e.mu.Unlock()
	return e.loadErr
}

// LoadError returns the error from the last reload, if any.
func (e *Engine) LoadError() error {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.loadErr
}

// Env returns a loaded environment.
func (e *Engine) Env(name string) (*Env, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	v, ok := e.envs[name]
	if !ok {
		return nil, fmt.Errorf("environment %q not found", name)
	}
	return v, nil
}

// Envs lists environments sorted by name.
func (e *Engine) Envs() []*Env {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]*Env, 0, len(e.envs))
	for _, v := range e.envs {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Config.Name < out[j].Config.Name })
	return out
}

func (e *Engine) setLatest(env string, results []probe.Result) {
	e.mu.Lock()
	defer e.mu.Unlock()
	m := e.latest[env]
	if m == nil {
		m = map[string]probe.Result{}
		e.latest[env] = m
	}
	for _, r := range results {
		m[r.Dep] = r
	}
}

// Latest returns the most recent probe result per dependency.
func (e *Engine) Latest(env string) map[string]probe.Result {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := map[string]probe.Result{}
	for k, v := range e.latest[env] {
		out[k] = v
	}
	return out
}

// acquire marks the environment busy for a mutating run. It fails fast
// instead of queueing so two people cannot reset the same demo at once.
func (e *Engine) acquire(v *Env, runID string) error {
	if !v.opMu.TryLock() {
		e.mu.RLock()
		busy := v.busy
		e.mu.RUnlock()
		return fmt.Errorf("environment %s is %w with run %s", v.Config.Name, ErrBusy, busy)
	}
	e.mu.Lock()
	v.busy = runID
	e.mu.Unlock()
	return nil
}

func (e *Engine) release(v *Env) {
	e.mu.Lock()
	v.busy = ""
	e.mu.Unlock()
	v.opMu.Unlock()
}

// Busy returns the id of the run mutating the environment, if any.
func (e *Engine) Busy(v *Env) string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return v.busy
}

// PinnedSet resolves the environment's pin to a seed set.
func (e *Engine) PinnedSet(v *Env) (*seeds.Set, string) {
	pin := v.Config.Pin
	if pin.Persona == "" {
		if len(v.Seeds) == 0 {
			return nil, ""
		}
		// No pin: the first persona's latest version is the implicit choice.
		personas := seeds.Personas(v.Seeds)
		set, _ := seeds.Find(v.Seeds, personas[0], 0)
		return set, set.ID()
	}
	set, ok := seeds.Find(v.Seeds, pin.Persona, pin.Version)
	if !ok {
		if pin.Version == 0 {
			return nil, pin.Persona + "@latest"
		}
		return nil, fmt.Sprintf("%s@v%d", pin.Persona, pin.Version)
	}
	return set, set.ID()
}

// ResolveSet picks a seed set from an optional persona/version, defaulting
// to the pin.
func (e *Engine) ResolveSet(v *Env, persona string, version int) (*seeds.Set, error) {
	if persona == "" {
		set, want := e.PinnedSet(v)
		if set == nil {
			if want == "" {
				return nil, fmt.Errorf("environment %s has no seed sets", v.Config.Name)
			}
			return nil, fmt.Errorf("pinned seed set %s does not exist", want)
		}
		if version != 0 && version != set.Version {
			s, ok := seeds.Find(v.Seeds, set.Persona, version)
			if !ok {
				return nil, fmt.Errorf("seed set %s@v%d does not exist", set.Persona, version)
			}
			return s, nil
		}
		return set, nil
	}
	set, ok := seeds.Find(v.Seeds, persona, version)
	if !ok {
		if version == 0 {
			return nil, fmt.Errorf("no seed sets for persona %q", persona)
		}
		return nil, fmt.Errorf("seed set %s@v%d does not exist", persona, version)
	}
	return set, nil
}

// TargetStatus is the connectivity of one target.
type TargetStatus struct {
	Name    string        `json:"name"`
	Kind    string        `json:"kind"`
	OK      bool          `json:"ok"`
	Message string        `json:"message"`
	Latency time.Duration `json:"latency"`
}

// Targets pings every target of an environment in parallel.
func (e *Engine) Targets(ctx context.Context, v *Env) []TargetStatus {
	names := v.Registry.Names()
	out := make([]TargetStatus, len(names))
	var wg sync.WaitGroup
	for i, n := range names {
		wg.Add(1)
		go func(i int, n string) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
			defer cancel()
			start := e.Now()
			msg, err := v.Registry.Ping(ctx, n)
			ts := TargetStatus{Name: n, Kind: v.Registry.Kind(n), Latency: e.Now().Sub(start)}
			if err != nil {
				ts.Message = err.Error()
			} else {
				ts.OK = true
				ts.Message = msg
			}
			out[i] = ts
		}(i, n)
	}
	wg.Wait()
	return out
}
