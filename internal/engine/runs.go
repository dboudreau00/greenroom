package engine

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/dboudreau00/greenroom/internal/state"
	"github.com/dboudreau00/greenroom/internal/targets"
)

// Event is a live update from a running operation.
type Event struct {
	Type   string            `json:"type"` // log | step | status | done
	RunID  string            `json:"runId"`
	At     time.Time         `json:"at"`
	Line   *state.LogLine    `json:"line,omitempty"`
	Step   *state.StepResult `json:"step,omitempty"`
	Status string            `json:"status,omitempty"`
	Run    *state.RunSummary `json:"run,omitempty"`
}

const maxLogLines = 5000

// recorder accumulates a run, persists it and fans events out to
// subscribers.
type recorder struct {
	e        *Engine
	run      *state.Run
	mu       sync.Mutex
	subs     map[chan Event]struct{}
	lastSave time.Time
	dropped  int
}

func (e *Engine) newRun(env, kind, actor string) *recorder {
	now := e.Now()
	r := &recorder{e: e, subs: map[chan Event]struct{}{}}
	r.run = &state.Run{ID: state.NewRunID(now), Env: env, Kind: kind, Actor: actor, Status: state.StatusRunning,
		StartedAt: now, Steps: []state.StepResult{}, Log: []state.LogLine{}}
	e.mu.Lock()
	e.active[r.run.ID] = r
	e.mu.Unlock()
	r.save(true)
	return r
}

// ID returns the run id.
func (r *recorder) ID() string { return r.run.ID }

func (r *recorder) logf(level, step, format string, args ...any) {
	text := fmt.Sprintf(format, args...)
	line := state.LogLine{At: r.e.Now(), Level: level, Step: step, Text: text}
	r.mu.Lock()
	if len(r.run.Log) >= maxLogLines {
		r.dropped++
		if r.dropped == 1 {
			r.run.Log = append(r.run.Log, state.LogLine{At: line.At, Level: "warn", Step: step, Text: "log truncated: further output lines are not stored"})
		}
		r.mu.Unlock()
		return
	}
	r.run.Log = append(r.run.Log, line)
	r.mu.Unlock()
	r.publish(Event{Type: "log", RunID: r.run.ID, At: line.At, Line: &line})
	r.save(false)
}

// Info logs an informational line.
func (r *recorder) Info(step, format string, args ...any) { r.logf("info", step, format, args...) }

// Warn logs a warning line.
func (r *recorder) Warn(step, format string, args ...any) { r.logf("warn", step, format, args...) }

// Error logs an error line.
func (r *recorder) Error(step, format string, args ...any) { r.logf("error", step, format, args...) }

// Sink returns a line sink that records command output under a step.
func (r *recorder) Sink(step string) targets.LineSink {
	return func(stream, line string) {
		level := "out"
		if stream == "stderr" {
			level = "err"
		}
		r.logf(level, step, "%s", line)
	}
}

// StartStep appends a running step and returns its index.
func (r *recorder) StartStep(name, kind string) int {
	st := state.StepResult{Name: name, Kind: kind, Status: state.StatusRunning, StartedAt: r.e.Now()}
	r.mu.Lock()
	r.run.Steps = append(r.run.Steps, st)
	idx := len(r.run.Steps) - 1
	r.mu.Unlock()
	r.publish(Event{Type: "step", RunID: r.run.ID, At: st.StartedAt, Step: &st})
	r.save(true)
	return idx
}

// EndStep closes a step with an outcome.
func (r *recorder) EndStep(idx int, err error, detail string) {
	r.mu.Lock()
	st := &r.run.Steps[idx]
	st.EndedAt = r.e.Now()
	st.Duration = st.EndedAt.Sub(st.StartedAt)
	st.Detail = detail
	if err != nil {
		st.Status = state.StatusFailed
		st.Error = err.Error()
	} else if st.Status == state.StatusRunning {
		st.Status = state.StatusOK
	}
	cp := *st
	r.mu.Unlock()
	r.publish(Event{Type: "step", RunID: r.run.ID, At: cp.EndedAt, Step: &cp})
	r.save(true)
}

// SkipStep marks a step as skipped.
func (r *recorder) SkipStep(idx int, reason string) {
	r.mu.Lock()
	st := &r.run.Steps[idx]
	st.Status = state.StatusSkipped
	st.EndedAt = r.e.Now()
	st.Detail = reason
	cp := *st
	r.mu.Unlock()
	r.publish(Event{Type: "step", RunID: r.run.ID, At: cp.EndedAt, Step: &cp})
	r.save(true)
}

// Finish closes the run.
func (r *recorder) Finish(err error, summary string) *state.Run {
	r.mu.Lock()
	r.run.EndedAt = r.e.Now()
	r.run.Duration = r.run.EndedAt.Sub(r.run.StartedAt)
	r.run.Summary = summary
	if err != nil {
		r.run.Status = state.StatusFailed
		r.run.Error = err.Error()
		if strings.Contains(err.Error(), "context canceled") {
			r.run.Status = state.StatusCancelled
		}
	} else {
		r.run.Status = state.StatusOK
	}
	brief := r.run.Brief()
	r.mu.Unlock()
	if err != nil {
		r.logf("error", "", "%s", err.Error())
	}
	r.save(true)
	r.publish(Event{Type: "done", RunID: r.run.ID, At: brief.EndedAt, Status: brief.Status, Run: &brief})
	r.e.mu.Lock()
	delete(r.e.active, r.run.ID)
	r.e.mu.Unlock()
	r.mu.Lock()
	for ch := range r.subs {
		close(ch)
		delete(r.subs, ch)
	}
	cp := r.snapshotLocked()
	r.mu.Unlock()
	return cp
}

func (r *recorder) publish(ev Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for ch := range r.subs {
		select {
		case ch <- ev:
		default:
			// Slow subscriber: drop the event rather than block the run.
		}
	}
}

func (r *recorder) save(force bool) {
	r.mu.Lock()
	if !force && r.e.Now().Sub(r.lastSave) < 500*time.Millisecond {
		r.mu.Unlock()
		return
	}
	r.lastSave = r.e.Now()
	cp := r.snapshotLocked()
	r.mu.Unlock()
	if err := r.e.store.SaveRun(cp); err != nil {
		r.e.log.Error("save run", "run", cp.ID, "err", err)
	}
}

func (r *recorder) snapshotLocked() *state.Run {
	cp := *r.run
	cp.Steps = append([]state.StepResult(nil), r.run.Steps...)
	cp.Log = append([]state.LogLine(nil), r.run.Log...)
	return &cp
}

// Snapshot returns a copy of the run so far.
func (r *recorder) Snapshot() *state.Run {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.snapshotLocked()
}

// Subscribe attaches to a live run. It returns the run so far, a channel of
// subsequent events (closed when the run ends) and a cancel function. ok is
// false when the run is not active.
func (e *Engine) Subscribe(runID string) (snapshot *state.Run, events <-chan Event, cancel func(), ok bool) {
	e.mu.RLock()
	r, ok := e.active[runID]
	e.mu.RUnlock()
	if !ok {
		return nil, nil, nil, false
	}
	ch := make(chan Event, 512)
	r.mu.Lock()
	if r.run.Status != state.StatusRunning {
		r.mu.Unlock()
		return nil, nil, nil, false
	}
	r.subs[ch] = struct{}{}
	snap := r.snapshotLocked()
	r.mu.Unlock()
	cancel = func() {
		r.mu.Lock()
		if _, still := r.subs[ch]; still {
			delete(r.subs, ch)
			close(ch)
		}
		r.mu.Unlock()
	}
	return snap, ch, cancel, true
}

// Run loads a run, live or from disk.
func (e *Engine) Run(env, id string) (*state.Run, error) {
	e.mu.RLock()
	r, ok := e.active[id]
	e.mu.RUnlock()
	if ok && r.run.Env == env {
		return r.Snapshot(), nil
	}
	return e.store.LoadRun(env, id)
}

// ActiveRuns lists runs in flight for an environment.
func (e *Engine) ActiveRuns(env string) []state.RunSummary {
	e.mu.RLock()
	defer e.mu.RUnlock()
	var out []state.RunSummary
	for _, r := range e.active {
		if r.run.Env == env {
			out = append(out, r.Snapshot().Brief())
		}
	}
	return out
}
