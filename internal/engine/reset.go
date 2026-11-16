package engine

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dboudreau00/greenroom/internal/brand"
	"github.com/dboudreau00/greenroom/internal/config"
	"github.com/dboudreau00/greenroom/internal/probe"
	"github.com/dboudreau00/greenroom/internal/seeds"
	"github.com/dboudreau00/greenroom/internal/sqlrun"
	"github.com/dboudreau00/greenroom/internal/state"
	"github.com/dboudreau00/greenroom/internal/targets"
)

// ResetOptions choose what to apply.
type ResetOptions struct {
	Persona string
	Version int
	// Force continues even when the preflight finds dependencies down.
	Force bool
	// SkipReset applies the seed steps without running reset strategies.
	SkipReset bool
	// Brand is the brand kit to personalize the demo for; empty uses the
	// environment's house brand.
	Brand string
	// Override resets even while a booked demo is on stage; the demo's
	// journal records it.
	Override bool
	Actor    string
}

// StartReset launches a reset in the background and returns its run id.
func (e *Engine) StartReset(envName string, opts ResetOptions) (string, error) {
	v, err := e.Env(envName)
	if err != nil {
		return "", err
	}
	set, err := e.ResolveSet(v, opts.Persona, opts.Version)
	if err != nil {
		return "", err
	}
	if len(set.Problems) > 0 {
		return "", fmt.Errorf("seed set %s cannot be applied: %s", set.ID(), strings.Join(set.Problems, "; "))
	}
	kit, err := e.loadKit(opts.Brand)
	if err != nil {
		return "", err
	}
	live, err := e.guardLive(v.Config.Name, opts.Override)
	if err != nil {
		return "", err
	}
	rec := e.newRun(v.Config.Name, state.RunReset, opts.Actor)
	if err := e.acquire(v, rec.ID()); err != nil {
		rec.discard()
		return "", err
	}
	e.noteOverride(live, opts.Actor, "reset", rec.ID())
	go func() {
		defer e.release(v)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
		defer cancel()
		e.runReset(ctx, v, set, kit, opts, rec)
	}()
	return rec.ID(), nil
}

// Reset applies a seed set synchronously.
func (e *Engine) Reset(ctx context.Context, envName string, opts ResetOptions) (*state.Run, error) {
	v, err := e.Env(envName)
	if err != nil {
		return nil, err
	}
	set, err := e.ResolveSet(v, opts.Persona, opts.Version)
	if err != nil {
		return nil, err
	}
	if len(set.Problems) > 0 {
		return nil, fmt.Errorf("seed set %s cannot be applied: %s", set.ID(), strings.Join(set.Problems, "; "))
	}
	kit, err := e.loadKit(opts.Brand)
	if err != nil {
		return nil, err
	}
	live, err := e.guardLive(v.Config.Name, opts.Override)
	if err != nil {
		return nil, err
	}
	rec := e.newRun(v.Config.Name, state.RunReset, opts.Actor)
	if err := e.acquire(v, rec.ID()); err != nil {
		return rec.Finish(err, ""), err
	}
	defer e.release(v)
	e.noteOverride(live, opts.Actor, "reset", rec.ID())
	run := e.runReset(ctx, v, set, kit, opts, rec)
	if run.Status != state.StatusOK {
		return run, errors.New(run.Error)
	}
	return run, nil
}

func (e *Engine) runReset(ctx context.Context, v *Env, set *seeds.Set, kit *brand.Kit, opts ResetOptions, rec *recorder) *state.Run {
	rec.mu.Lock()
	rec.run.Persona, rec.run.Version, rec.run.SetHash = set.Persona, set.Version, set.Hash
	if kit != nil {
		rec.run.Brand = kit.Slug
	}
	rec.mu.Unlock()
	rec.Info("", "reset %s to %s (%s)", v.Config.DisplayTitle(), set.ID(), set.DisplayTitle())
	rec.Info("", "seed set hash %s, %d files, %d steps", set.Hash[:12], len(set.Files), len(set.Steps))
	vals := e.brandValues(v, kit)
	if kit != nil {
		rec.Info("", "personalized for %s (%s, kit %s)", kit.Name, firstNonEmpty(kit.Domain, kit.Slug), kit.Hash())
	} else {
		rec.Info("", "house brand %s", vals["name"])
	}
	start := e.Now()

	// 1. Preflight.
	idx := rec.StartStep("preflight", "check")
	results := v.Prober.ProbeAll(ctx)
	e.setLatest(v.Config.Name, results)
	needed := neededDeps(v.Config, set)
	var down []string
	for _, r := range results {
		if r.OK {
			rec.Info("preflight", "ok %s: %s", r.Dep, r.Message)
			continue
		}
		rec.Warn("preflight", "down %s: %s", r.Dep, r.Message)
		dep, _ := v.Config.Dependency(r.Dep)
		if dep.Heal == nil && !needed[r.Dep] {
			continue
		}
		if dep.Heal != nil {
			rec.Info("preflight", "healing %s (%s)", r.Dep, dep.Heal.Action)
			if err := e.healDep(ctx, v, *dep, rec, "preflight"); err != nil {
				rec.Error("preflight", "heal %s failed: %v", r.Dep, err)
			} else if again := v.Prober.Probe(ctx, *dep); again.OK {
				rec.Info("preflight", "%s is up after heal", r.Dep)
				e.setLatest(v.Config.Name, []probe.Result{again})
				continue
			}
		}
		if needed[r.Dep] {
			down = append(down, r.Dep)
		}
	}
	if len(down) > 0 && !opts.Force {
		err := fmt.Errorf("preflight failed, dependencies down: %s (use force to continue anyway)", strings.Join(down, ", "))
		rec.EndStep(idx, err, "")
		return rec.Finish(err, "preflight failed")
	}
	if len(down) > 0 {
		rec.Warn("preflight", "continuing despite down dependencies (force)")
	}
	rec.EndStep(idx, nil, fmt.Sprintf("%d dependencies probed", len(results)))

	// 2. Reset strategies.
	if !opts.SkipReset {
		for _, dep := range resetTargets(v.Config, set) {
			name := "reset " + dep.Name
			idx := rec.StartStep(name, "reset")
			if dep.Reset == nil {
				rec.SkipStep(idx, "no reset block")
				continue
			}
			detail, err := e.resetDependency(ctx, v, dep, rec, name)
			rec.EndStep(idx, err, detail)
			if err != nil {
				return rec.Finish(fmt.Errorf("reset of %s failed: %w", dep.Name, err), "reset strategy failed")
			}
		}
	} else {
		rec.Info("", "reset strategies skipped on request")
	}

	// 3. Steps.
	for _, st := range set.Steps {
		idx := rec.StartStep(st.Name, st.Kind)
		detail, err := e.runStep(ctx, v, set, st, kit, vals, rec)
		if err != nil && st.ContinueOnError {
			rec.Warn(st.Name, "step failed but continueOnError is set: %v", err)
			rec.EndStep(idx, nil, "failed, continued: "+err.Error())
			continue
		}
		rec.EndStep(idx, err, detail)
		if err != nil {
			e.store.ClearApplied(v.Config.Name)
			return rec.Finish(fmt.Errorf("step %s failed: %w", st.Name, err), "seed step failed")
		}
	}

	// 4. Verify.
	for _, c := range set.Verify {
		name := "verify " + c.Name
		idx := rec.StartStep(name, "verify")
		detail, err := e.runVerify(ctx, v, c, rec, name)
		rec.EndStep(idx, err, detail)
		if err != nil {
			e.store.ClearApplied(v.Config.Name)
			return rec.Finish(fmt.Errorf("verification %s failed: %w", c.Name, err), "verification failed")
		}
	}

	// 5. Fingerprints and baselines.
	idx = rec.StartStep("record", "record")
	after := v.Prober.ProbeAll(ctx)
	e.setLatest(v.Config.Name, after)
	var samples []state.Sample
	applied := &state.Applied{Persona: set.Persona, Version: set.Version, SetID: set.ID(), SetHash: set.Hash, Files: set.Files,
		At: e.Now(), RunID: rec.ID(), Actor: opts.Actor, Fingerprints: map[string]state.Fingerprint{}, Baselines: map[string]map[string]float64{}}
	if kit != nil {
		applied.Brand = &state.AppliedBrand{Slug: kit.Slug, Name: kit.Name, Hash: kit.Hash()}
	}
	var stillDown []string
	for _, r := range after {
		samples = append(samples, r.Sample())
		if r.Fingerprint != nil {
			applied.Fingerprints[r.Dep] = *r.Fingerprint
		}
		if !r.OK {
			stillDown = append(stillDown, r.Dep)
		}
	}
	e.store.AppendSamples(v.Config.Name, samples)
	for _, dep := range v.Config.Dependencies {
		dep = driftFor(dep, set.Persona)
		if len(dep.Drift) == 0 {
			continue
		}
		values, errs := e.driftValues(ctx, v, dep)
		for _, dc := range dep.Drift {
			if err, bad := errs[dc.Name]; bad {
				rec.Warn("record", "drift check %s on %s could not be evaluated: %v", dc.Name, dep.Name, err)
				continue
			}
			val := values[dc.Name]
			if ok, want := compareDrift(val, dc, nil); !ok {
				err := fmt.Errorf("drift check %s on %s is %s right after the reset, expected %s", dc.Name, dep.Name, fmtNum(val), want)
				rec.EndStep(idx, err, "")
				e.store.ClearApplied(v.Config.Name)
				return rec.Finish(err, "drift check failed after reset")
			}
			if applied.Baselines[dep.Name] == nil {
				applied.Baselines[dep.Name] = map[string]float64{}
			}
			applied.Baselines[dep.Name][dc.Name] = val
			rec.Info("record", "baseline %s.%s = %s", dep.Name, dc.Name, fmtNum(val))
		}
	}
	applied.Duration = max(e.Now().Sub(start), 0)
	if err := e.store.SaveApplied(v.Config.Name, applied); err != nil {
		rec.EndStep(idx, err, "")
		return rec.Finish(err, "could not record the applied state")
	}
	rec.EndStep(idx, nil, fmt.Sprintf("%d fingerprints, %d baselines", len(applied.Fingerprints), len(applied.Baselines)))
	summary := fmt.Sprintf("%s applied in %s", set.ID(), applied.Duration.Round(time.Second))
	if len(stillDown) > 0 {
		summary += "; still down: " + strings.Join(stillDown, ", ")
		rec.Warn("", "dependencies still down after the reset: %s", strings.Join(stillDown, ", "))
	}
	rec.Info("", "%s", summary)
	return rec.Finish(nil, summary)
}

// neededDeps lists dependencies a seed set touches plus every critical one.
func neededDeps(env *config.Environment, set *seeds.Set) map[string]bool {
	out := map[string]bool{}
	for _, d := range env.Dependencies {
		if d.Critical {
			out[d.Name] = true
		}
	}
	for _, st := range set.Steps {
		if st.Dependency != "" {
			out[st.Dependency] = true
		}
	}
	for _, d := range resetTargets(env, set) {
		out[d.Name] = true
	}
	return out
}

// resetTargets returns the dependencies whose reset strategy runs before
// the set: the ones the manifest lists, else every dependency with a reset
// block.
func resetTargets(env *config.Environment, set *seeds.Set) []config.Dependency {
	var out []config.Dependency
	if len(set.Dependencies) > 0 {
		for _, name := range set.Dependencies {
			if d, ok := env.Dependency(name); ok {
				out = append(out, *d)
			}
		}
		return out
	}
	for _, d := range env.Dependencies {
		if d.Reset != nil {
			out = append(out, d)
		}
	}
	return out
}

// resetDependency runs one dependency's reset strategy.
func (e *Engine) resetDependency(ctx context.Context, v *Env, dep config.Dependency, rec *recorder, step string) (string, error) {
	r := dep.Reset
	rec.Info(step, "strategy %s on %s (%s)", r.Strategy, dep.Name, dep.Kind)
	switch dep.Kind {
	case config.KindPostgres:
		return e.resetPostgres(ctx, v, dep, rec, step)
	case config.KindMySQL:
		return e.resetMySQL(ctx, v, dep, rec, step)
	case config.KindDocker:
		return e.resetDocker(ctx, v, dep, rec, step)
	case config.KindKubernetes:
		return e.resetKube(ctx, v, dep, rec, step)
	case config.KindEC2:
		return e.resetEC2(ctx, v, dep, rec, step)
	}
	if r.Strategy == "script" || r.Strategy == "command" {
		return e.resetScript(ctx, v, dep, rec, step)
	}
	return "", fmt.Errorf("no reset strategy %q for kind %s", r.Strategy, dep.Kind)
}

func (e *Engine) resetScript(ctx context.Context, v *Env, dep config.Dependency, rec *recorder, step string) (string, error) {
	ex, err := v.Registry.Executor(dep.Target)
	if err != nil {
		return "", err
	}
	spec := targets.RunSpec{Argv: dep.Reset.Command, Container: dep.Container, Pod: dep.Resource, Namespace: dep.Namespace, InstanceID: dep.InstanceID, Env: v.Config.Env}
	if dep.Reset.Script != "" {
		b, err := os.ReadFile(filepath.Join(v.Config.Dir, filepath.FromSlash(dep.Reset.Script)))
		if err != nil {
			return "", err
		}
		spec.Script = string(b)
		spec.Argv = nil
	}
	res, err := ex.Run(ctx, spec, rec.Sink(step))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("exit %d", res.ExitCode), res.Error()
}

func (e *Engine) resetPostgres(ctx context.Context, v *Env, dep config.Dependency, rec *recorder, step string) (string, error) {
	runner, err := v.Prober.SQLRunner(dep)
	if err != nil {
		return "", err
	}
	defer runner.Close()
	switch dep.Reset.Strategy {
	case "template":
		db, err := databaseName(ctx, runner, dep.Reset.Database)
		if err != nil {
			return "", err
		}
		tpl := dep.Reset.Template
		if tpl == "" {
			tpl = db + "_golden"
		}
		snap, _ := e.store.Snapshot(v.Config.Name, dep.Name)
		if snap == nil {
			rec.Warn(step, "no snapshot record for %s; assuming template %s exists", dep.Name, tpl)
		}
		maint, err := runner.WithDatabase("postgres")
		if err != nil {
			return "", err
		}
		defer maint.Close()
		runner.Close()
		rec.Info(step, "dropping %s (terminating its sessions first)", db)
		if err := pgDropDatabase(ctx, maint, db); err != nil {
			return "", err
		}
		rec.Info(step, "creating %s from template %s", db, tpl)
		if err := pgRetry(ctx, func() error {
			return maint.Exec(ctx, fmt.Sprintf("CREATE DATABASE %s TEMPLATE %s", pgIdent(db), pgIdent(tpl)), nil)
		}, func() error { return pgTerminate(ctx, maint, tpl) }); err != nil {
			return "", fmt.Errorf("%w (run: greenroom snapshot %s %s to capture the template)", err, v.Config.Name, dep.Name)
		}
		return "cloned from " + tpl, nil
	case "schema":
		schema := dep.Reset.Schema
		if schema == "" {
			schema = "public"
		}
		rec.Info(step, "dropping and recreating schema %s", schema)
		sql := fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE; CREATE SCHEMA %s;", pgIdent(schema), pgIdent(schema))
		if schema == "public" {
			sql += " GRANT ALL ON SCHEMA public TO public;"
		}
		return "schema " + schema + " recreated", runner.Exec(ctx, sql, rec.Sink(step))
	case "script":
		b, err := os.ReadFile(filepath.Join(v.Config.Dir, filepath.FromSlash(dep.Reset.Script)))
		if err != nil {
			return "", err
		}
		rec.Info(step, "running %s", dep.Reset.Script)
		return dep.Reset.Script, runner.Exec(ctx, string(b), rec.Sink(step))
	}
	return "", fmt.Errorf("postgres reset strategy %q is not one of template, schema, script", dep.Reset.Strategy)
}

func (e *Engine) resetMySQL(ctx context.Context, v *Env, dep config.Dependency, rec *recorder, step string) (string, error) {
	runner, err := v.Prober.SQLRunner(dep)
	if err != nil {
		return "", err
	}
	defer runner.Close()
	switch dep.Reset.Strategy {
	case "schema":
		db, err := databaseName(ctx, runner, dep.Reset.Database)
		if err != nil {
			return "", err
		}
		rec.Info(step, "dropping and recreating database %s", db)
		q := "`" + strings.ReplaceAll(db, "`", "``") + "`"
		sql := fmt.Sprintf("DROP DATABASE IF EXISTS %s; CREATE DATABASE %s;", q, q)
		return "database " + db + " recreated", runner.Exec(ctx, sql, rec.Sink(step))
	case "script":
		b, err := os.ReadFile(filepath.Join(v.Config.Dir, filepath.FromSlash(dep.Reset.Script)))
		if err != nil {
			return "", err
		}
		rec.Info(step, "running %s", dep.Reset.Script)
		return dep.Reset.Script, runner.Exec(ctx, string(b), rec.Sink(step))
	}
	return "", fmt.Errorf("mysql reset strategy %q is not one of schema, script", dep.Reset.Strategy)
}

func (e *Engine) resetDocker(ctx context.Context, v *Env, dep config.Dependency, rec *recorder, step string) (string, error) {
	cli, err := v.Registry.Docker(dep.Target)
	if err != nil {
		return "", err
	}
	switch dep.Reset.Strategy {
	case "restart":
		rec.Info(step, "restarting container %s", dep.Container)
		if err := cli.Restart(ctx, dep.Container, 30); err != nil {
			return "", err
		}
		_, err := cli.WaitHealthy(ctx, dep.Container, 2*time.Minute)
		return "restarted", err
	case "recreate":
		return e.recreateDocker(ctx, v, dep, rec, step)
	case "snapshot":
		return e.restoreDocker(ctx, v, dep, rec, step)
	}
	return "", fmt.Errorf("docker reset strategy %q is not one of restart, recreate, snapshot", dep.Reset.Strategy)
}

func (e *Engine) resetKube(ctx context.Context, v *Env, dep config.Dependency, rec *recorder, step string) (string, error) {
	cli, err := v.Registry.Kube(dep.Target)
	if err != nil {
		return "", err
	}
	switch dep.Reset.Strategy {
	case "rollout-restart":
		rec.Info(step, "rolling out a restart of %s", dep.Resource)
		if err := cli.RolloutRestart(ctx, dep.Namespace, dep.Resource); err != nil {
			return "", err
		}
		time.Sleep(2 * time.Second)
		w, err := cli.WaitReady(ctx, dep.Namespace, dep.Resource, 5*time.Minute)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%d/%d ready", w.Ready, w.Desired), nil
	case "script", "command":
		return e.resetScript(ctx, v, dep, rec, step)
	}
	return "", fmt.Errorf("kubernetes reset strategy %q is not one of rollout-restart, script", dep.Reset.Strategy)
}

func (e *Engine) resetEC2(ctx context.Context, v *Env, dep config.Dependency, rec *recorder, step string) (string, error) {
	cli, err := v.Registry.AWS(dep.Target)
	if err != nil {
		return "", err
	}
	switch dep.Reset.Strategy {
	case "reboot":
		rec.Info(step, "rebooting %s", dep.InstanceID)
		if err := cli.RebootInstances(ctx, dep.InstanceID); err != nil {
			return "", err
		}
		time.Sleep(10 * time.Second)
		if _, err := cli.WaitInstance(ctx, dep.InstanceID, "running", 5*time.Minute); err != nil {
			return "", err
		}
		return "rebooted", nil
	case "script", "command":
		return e.resetScript(ctx, v, dep, rec, step)
	}
	return "", fmt.Errorf("ec2 reset strategy %q is not one of reboot, script", dep.Reset.Strategy)
}

// runStep executes one seed step. Brand values are filled in after ${VAR}
// expansion, so a value from a fetched kit is never expanded itself.
func (e *Engine) runStep(ctx context.Context, v *Env, set *seeds.Set, st seeds.Step, kit *brand.Kit, vals brand.Values, rec *recorder) (string, error) {
	expand := func(s string) string {
		return config.Expand(s, func(k string) string {
			if val, ok := st.Env[k]; ok {
				return val
			}
			if val, ok := v.Config.Env[k]; ok {
				return val
			}
			return os.Getenv(k)
		})
	}
	env := map[string]string{}
	for k, val := range v.Config.Env {
		env[k] = val
	}
	for k, val := range vals.Env() {
		env[k] = val
	}
	for k, val := range st.Env {
		env[k] = expand(val)
	}
	timeout := st.Timeout.D()
	switch st.Kind {
	case seeds.StepSQL:
		dep, ok := v.Config.Dependency(st.Dependency)
		if !ok {
			return "", fmt.Errorf("unknown dependency %q", st.Dependency)
		}
		runner, err := v.Prober.SQLRunner(*dep)
		if err != nil {
			return "", err
		}
		defer runner.Close()
		script := st.SQL
		if st.File != "" {
			b, err := set.ReadFile(st.File)
			if err != nil {
				return "", err
			}
			script = string(b)
		}
		script, err = personalize(st, vals, script, brand.SQL)
		if err != nil {
			return "", err
		}
		rec.Info(st.Name, "applying %s to %s (%d bytes)", firstNonEmpty(st.File, "inline sql"), dep.Name, len(script))
		cctx, cancel := withTimeout(ctx, timeout, 30*time.Minute)
		defer cancel()
		if err := runner.Exec(cctx, script, rec.Sink(st.Name)); err != nil {
			return "", err
		}
		return fmt.Sprintf("%d bytes applied", len(script)), nil
	case seeds.StepShell, seeds.StepCommand:
		ex, err := v.Registry.Executor(st.Target)
		if err != nil {
			return "", err
		}
		spec := targets.RunSpec{Container: st.Container, Pod: st.Pod, Namespace: st.Namespace, InstanceID: st.InstanceID, Env: env, Timeout: timeout}
		if st.Kind == seeds.StepCommand {
			spec.Argv = make([]string, len(st.Command))
			for i, a := range st.Command {
				arg, err := personalize(st, vals, expand(a), brand.Raw)
				if err != nil {
					return "", err
				}
				spec.Argv[i] = arg
			}
			if st.File != "" {
				b, err := set.ReadFile(st.File)
				if err != nil {
					return "", err
				}
				in, err := personalizeFile(st, vals, string(b), st.File)
				if err != nil {
					return "", err
				}
				spec.Stdin = strings.NewReader(in)
				spec.StdinLen = int64(len(in))
			}
			rec.Info(st.Name, "running %s on %s", targets.ShellJoin(spec.Argv), firstNonEmpty(st.Target, "local"))
		} else {
			spec.Script = st.Script
			if st.File != "" {
				b, err := set.ReadFile(st.File)
				if err != nil {
					return "", err
				}
				spec.Script = string(b)
			}
			rec.Info(st.Name, "running script %s on %s", firstNonEmpty(st.File, "(inline)"), firstNonEmpty(st.Target, "local"))
		}
		res, err := ex.Run(ctx, spec, rec.Sink(st.Name))
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("exit %d in %s", res.ExitCode, res.Duration.Round(time.Millisecond)), res.Error()
	case seeds.StepHTTP:
		return e.httpStep(ctx, set, st, expand, vals, rec)
	case seeds.StepAsset:
		return e.assetStep(ctx, v, set, st, kit, expand, rec)
	case seeds.StepCopy:
		cp, err := v.Registry.Copier(st.Target)
		if err != nil {
			return "", err
		}
		b, err := set.ReadFile(st.File)
		if err != nil {
			return "", err
		}
		dest, err := personalize(st, vals, expand(st.Dest), brand.Path)
		if err != nil {
			return "", err
		}
		if st.Personalize {
			out, err := vals.RenderFile(string(b), dest)
			if err != nil {
				return "", err
			}
			b = []byte(out)
		}
		rec.Info(st.Name, "copying %s (%d bytes) to %s:%s", st.File, len(b), st.Target, dest)
		spec := targets.RunSpec{Container: st.Container, Pod: st.Pod, Namespace: st.Namespace, InstanceID: st.InstanceID, Timeout: timeout}
		return fmt.Sprintf("%d bytes", len(b)), cp.CopyTo(ctx, spec, dest, b, 0o644)
	case seeds.StepWait:
		dep, ok := v.Config.Dependency(st.Dependency)
		if !ok {
			return "", fmt.Errorf("unknown dependency %q", st.Dependency)
		}
		limit := timeout
		if limit == 0 {
			limit = 2 * time.Minute
		}
		deadline := e.Now().Add(limit)
		rec.Info(st.Name, "waiting up to %s for %s", limit, dep.Name)
		for {
			r := v.Prober.Probe(ctx, *dep)
			if r.OK {
				return "up after " + humanDuration(limit-time.Until(deadline)), nil
			}
			if e.Now().After(deadline) {
				return "", fmt.Errorf("%s still down after %s: %s", dep.Name, limit, r.Message)
			}
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(2 * time.Second):
			}
		}
	}
	return "", fmt.Errorf("unknown step kind %q", st.Kind)
}

func (e *Engine) httpStep(ctx context.Context, set *seeds.Set, st seeds.Step, expand func(string) string, vals brand.Values, rec *recorder) (string, error) {
	var body []byte
	if st.BodyFile != "" {
		b, err := set.ReadFile(st.BodyFile)
		if err != nil {
			return "", err
		}
		body = b
	} else if st.Body != "" {
		body = []byte(expand(st.Body))
	}
	if body != nil && st.Personalize && bytes.Contains(body, []byte("{{")) {
		mode, err := bodyMode(st.Headers)
		if err != nil {
			return "", err
		}
		out, err := vals.Render(string(body), mode)
		if err != nil {
			return "", err
		}
		body = []byte(out)
	}
	method := st.Method
	if method == "" {
		if body != nil {
			method = http.MethodPost
		} else {
			method = http.MethodGet
		}
	}
	u, err := personalize(st, vals, expand(st.URL), brand.URL)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, method, u, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	if body != nil && st.Headers["Content-Type"] == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, val := range st.Headers {
		hv, err := personalize(st, vals, expand(val), brand.Raw)
		if err != nil {
			return "", err
		}
		req.Header.Set(k, hv)
	}
	rec.Info(st.Name, "%s %s (%d bytes)", method, u, len(body))
	client := &http.Client{Timeout: st.Timeout.Or(60 * time.Second), Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, TLSClientConfig: &tls.Config{InsecureSkipVerify: st.Insecure, MinVersion: tls.VersionTLS12}}}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	snippet := strings.TrimSpace(string(rb))
	if len(snippet) > 300 {
		snippet = snippet[:300] + "..."
	}
	if snippet != "" {
		rec.Sink(st.Name)("stdout", snippet)
	}
	if st.ExpectStatus != 0 && resp.StatusCode != st.ExpectStatus {
		return "", fmt.Errorf("HTTP %d, expected %d", resp.StatusCode, st.ExpectStatus)
	}
	if st.ExpectStatus == 0 && resp.StatusCode >= 400 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return fmt.Sprintf("HTTP %d", resp.StatusCode), nil
}

// runVerify evaluates one post-apply check.
func (e *Engine) runVerify(ctx context.Context, v *Env, c seeds.Check, rec *recorder, step string) (string, error) {
	switch c.Kind {
	case "sql":
		dep, ok := v.Config.Dependency(c.Dependency)
		if !ok {
			return "", fmt.Errorf("unknown dependency %q", c.Dependency)
		}
		runner, err := v.Prober.SQLRunner(*dep)
		if err != nil {
			return "", err
		}
		defer runner.Close()
		text, err := runner.Scalar(ctx, c.Query)
		if err != nil {
			return "", err
		}
		val, err := sqlrun.ParseNumber(text)
		if err != nil {
			return "", fmt.Errorf("result %q is not a number", text)
		}
		dc := config.DriftCheck{Expect: c.Expect, Min: c.Min, Max: c.Max, Tolerance: c.Tolerance}
		ok, want := compareDrift(val, dc, nil)
		rec.Info(step, "%s = %s (want %s)", c.Query, fmtNum(val), want)
		if !ok {
			return "", fmt.Errorf("got %s, expected %s", fmtNum(val), want)
		}
		return fmtNum(val), nil
	case "http":
		u := v.expand(c.URL)
		dep := config.Dependency{Name: c.Name, Kind: config.KindHTTP, URL: u, ExpectStatus: c.ExpectStatus, ExpectBody: c.ExpectBody}
		r := v.Prober.Probe(ctx, dep)
		rec.Info(step, "%s: %s", u, r.Message)
		if !r.OK {
			return "", errors.New(r.Message)
		}
		return r.Message, nil
	case "command":
		ex, err := v.Registry.Executor(c.Target)
		if err != nil {
			return "", err
		}
		argv := make([]string, len(c.Command))
		for i, a := range c.Command {
			argv[i] = v.expand(a)
		}
		res, err := ex.Run(ctx, targets.RunSpec{Argv: argv, Env: v.Config.Env}, rec.Sink(step))
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("exit %d", res.ExitCode), res.Error()
	}
	return "", fmt.Errorf("unknown verify kind %q", c.Kind)
}

// expand substitutes ${VAR} from the environment's env block, then the
// process environment, as steps do. A verify check may run sh -c with its
// own variables, so a ${VAR} that neither knows and that has no default is
// left as written.
func (v *Env) expand(s string) string {
	return config.ExpandKnown(s, func(k string) (string, bool) {
		if val, ok := v.Config.Env[k]; ok {
			return val, true
		}
		return os.LookupEnv(k)
	})
}

// databaseName returns the configured name or asks the server.
func databaseName(ctx context.Context, r sqlrun.Runner, configured string) (string, error) {
	if configured != "" {
		return configured, nil
	}
	q := "SELECT current_database()"
	if r.Kind() == config.KindMySQL {
		q = "SELECT DATABASE()"
	}
	name, err := r.Scalar(ctx, q)
	if err != nil {
		return "", fmt.Errorf("could not determine database name (set reset.database): %w", err)
	}
	name = strings.TrimSpace(name)
	if name == "" || name == "NULL" {
		return "", errors.New("could not determine database name (set reset.database)")
	}
	return name, nil
}

func pgIdent(s string) string  { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
func pgLiteral(s string) string { return `'` + strings.ReplaceAll(s, `'`, `''`) + `'` }

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func withTimeout(ctx context.Context, t, def time.Duration) (context.Context, context.CancelFunc) {
	if t == 0 {
		t = def
	}
	return context.WithTimeout(ctx, t)
}

// pgTerminate kills every other session on a database.
func pgTerminate(ctx context.Context, maint sqlrun.Runner, db string) error {
	return maint.Exec(ctx, fmt.Sprintf("SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = %s AND pid <> pg_backend_pid()", pgLiteral(db)), nil)
}

// pgDropDatabase drops a database, terminating sessions and retrying while
// probes or applications reconnect underneath it.
func pgDropDatabase(ctx context.Context, maint sqlrun.Runner, db string) error {
	return pgRetry(ctx, func() error {
		return maint.Exec(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %s", pgIdent(db)), nil)
	}, func() error { return pgTerminate(ctx, maint, db) })
}

// pgRetry runs before then op, retrying a few times when Postgres reports
// that the database is still being accessed by other users.
func pgRetry(ctx context.Context, op func() error, before func() error) error {
	var err error
	for attempt := 0; attempt < 6; attempt++ {
		if before != nil {
			if err = before(); err != nil {
				return err
			}
		}
		err = op()
		if err == nil {
			return nil
		}
		msg := err.Error()
		if !strings.Contains(msg, "being accessed by other users") && !strings.Contains(msg, "is being used by") {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(300*(attempt+1)) * time.Millisecond):
		}
	}
	return err
}
