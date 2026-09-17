package engine

import (
	"context"
	"sync"
	"time"

	"github.com/dboudreau00/greenroom/internal/state"
)

// RunScheduler probes every environment on the workspace interval, records
// uptime samples, triggers automatic heals and compacts old samples. It
// returns when ctx is cancelled.
func (e *Engine) RunScheduler(ctx context.Context) {
	interval := e.ws.ProbeInterval.D()
	if interval < 5*time.Second {
		interval = 5 * time.Second
	}
	e.log.Info("scheduler started", "interval", interval)
	lastCompact := time.Time{}
	for {
		e.tick(ctx)
		if e.Now().Sub(lastCompact) > time.Hour {
			lastCompact = e.Now()
			for _, v := range e.Envs() {
				if err := e.store.Compact(v.Config.Name, e.ws.SampleRetention.D()); err != nil {
					e.log.Warn("compact samples", "env", v.Config.Name, "err", err)
				}
				e.store.PruneRuns(v.Config.Name, 500)
			}
		}
		select {
		case <-ctx.Done():
			e.log.Info("scheduler stopped")
			return
		case <-time.After(interval):
		}
	}
}

// tick probes every environment once.
func (e *Engine) tick(ctx context.Context) {
	var wg sync.WaitGroup
	for _, v := range e.Envs() {
		wg.Add(1)
		go func(v *Env) {
			defer wg.Done()
			e.probeEnv(ctx, v)
		}(v)
	}
	wg.Wait()
}

func (e *Engine) probeEnv(ctx context.Context, v *Env) {
	pctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	results := v.Prober.ProbeAll(pctx)
	e.setLatest(v.Config.Name, results)
	var samples []state.Sample
	for _, r := range results {
		samples = append(samples, r.Sample())
	}
	if err := e.store.AppendSamples(v.Config.Name, samples); err != nil {
		e.log.Warn("append samples", "env", v.Config.Name, "err", err)
	}
	if e.Busy(v) != "" {
		return
	}
	for _, r := range results {
		if r.OK {
			continue
		}
		dep, ok := v.Config.Dependency(r.Dep)
		if !ok || !dep.AutoHeal || dep.Heal == nil {
			continue
		}
		last := e.store.LastHeal(v.Config.Name, dep.Name)
		if !last.IsZero() && e.Now().Sub(last) < e.ws.HealCooldown.D() {
			continue
		}
		e.log.Warn("auto heal", "env", v.Config.Name, "dep", dep.Name, "action", dep.Heal.Action, "reason", r.Message)
		go func(env, dep string) {
			hctx, hcancel := context.WithTimeout(ctx, 15*time.Minute)
			defer hcancel()
			if _, err := e.Heal(hctx, env, dep, true, "scheduler"); err != nil {
				e.log.Error("auto heal failed", "env", env, "dep", dep, "err", err)
			} else {
				e.log.Info("auto heal succeeded", "env", env, "dep", dep)
			}
		}(v.Config.Name, dep.Name)
	}
}
