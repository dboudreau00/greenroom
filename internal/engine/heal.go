package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dboudreau00/greenroom/internal/config"
	"github.com/dboudreau00/greenroom/internal/probe"
	"github.com/dboudreau00/greenroom/internal/state"
	"github.com/dboudreau00/greenroom/internal/targets"
)

// Heal runs a dependency's heal action and re-probes it.
func (e *Engine) Heal(ctx context.Context, envName, depName string, auto bool, actor string) (*state.Run, error) {
	v, err := e.Env(envName)
	if err != nil {
		return nil, err
	}
	dep, ok := v.Config.Dependency(depName)
	if !ok {
		return nil, fmt.Errorf("unknown dependency %q", depName)
	}
	if dep.Heal == nil {
		return nil, fmt.Errorf("dependency %s has no heal action", depName)
	}
	rec := e.newRun(v.Config.Name, state.RunHeal, actor)
	if err := e.acquire(v, rec.ID()); err != nil {
		return rec.Finish(err, ""), err
	}
	defer e.release(v)
	return e.runHeal(ctx, v, *dep, rec, auto)
}

// StartHeal runs a heal in the background and returns the run id.
func (e *Engine) StartHeal(envName, depName string, actor string) (string, error) {
	v, err := e.Env(envName)
	if err != nil {
		return "", err
	}
	dep, ok := v.Config.Dependency(depName)
	if !ok {
		return "", fmt.Errorf("unknown dependency %q", depName)
	}
	if dep.Heal == nil {
		return "", fmt.Errorf("dependency %s has no heal action", depName)
	}
	rec := e.newRun(v.Config.Name, state.RunHeal, actor)
	if err := e.acquire(v, rec.ID()); err != nil {
		rec.discard()
		return "", err
	}
	go func() {
		defer e.release(v)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
		defer cancel()
		e.runHeal(ctx, v, *dep, rec, false)
	}()
	return rec.ID(), nil
}

func (e *Engine) runHeal(ctx context.Context, v *Env, dep config.Dependency, rec *recorder, auto bool) (*state.Run, error) {
	step := "heal " + dep.Name
	idx := rec.StartStep(step, "heal")
	rec.Info(step, "%s on %s (%s)", dep.Heal.Action, dep.Name, dep.Kind)
	ev := state.HealEvent{At: e.Now(), Dep: dep.Name, Action: dep.Heal.Action, Auto: auto, RunID: rec.ID()}
	if err := e.healDep(ctx, v, dep, rec, step); err != nil {
		ev.Error = err.Error()
		e.store.AppendHeal(v.Config.Name, ev)
		rec.EndStep(idx, err, "")
		run := rec.Finish(err, "heal failed")
		return run, err
	}
	r := v.Prober.Probe(ctx, dep)
	e.setLatest(v.Config.Name, []probe.Result{r})
	e.store.AppendSample(v.Config.Name, r.Sample())
	if !r.OK {
		err := fmt.Errorf("heal ran but %s is still down: %s", dep.Name, r.Message)
		ev.Error = err.Error()
		e.store.AppendHeal(v.Config.Name, ev)
		rec.EndStep(idx, err, "")
		run := rec.Finish(err, "still down after heal")
		return run, err
	}
	ev.OK = true
	e.store.AppendHeal(v.Config.Name, ev)
	rec.EndStep(idx, nil, r.Message)
	summary := fmt.Sprintf("%s healed with %s: %s", dep.Name, dep.Heal.Action, r.Message)
	rec.Info(step, "%s", summary)
	return rec.Finish(nil, summary), nil
}

// healDep performs the heal action without recording a run of its own.
func (e *Engine) healDep(ctx context.Context, v *Env, dep config.Dependency, rec *recorder, step string) error {
	h := dep.Heal
	if h == nil {
		return errors.New("no heal action configured")
	}
	if h.Action == "command" {
		target := h.Target
		if target == "" {
			target = dep.Target
		}
		ex, err := v.Registry.Executor(target)
		if err != nil {
			return err
		}
		spec := targets.RunSpec{Argv: h.Command, Script: h.Script, Container: dep.Container, Pod: dep.Resource, Namespace: dep.Namespace, InstanceID: dep.InstanceID, Env: v.Config.Env, Timeout: 5 * time.Minute}
		if h.Script != "" && looksLikePath(h.Script) {
			if b, err := os.ReadFile(filepath.Join(v.Config.Dir, filepath.FromSlash(h.Script))); err == nil {
				spec.Script = string(b)
			}
		}
		res, err := ex.Run(ctx, spec, rec.Sink(step))
		if err != nil {
			return err
		}
		return res.Error()
	}
	switch dep.Kind {
	case config.KindDocker:
		cli, err := v.Registry.Docker(dep.Target)
		if err != nil {
			return err
		}
		switch h.Action {
		case "start":
			if err := cli.Start(ctx, dep.Container); err != nil {
				return err
			}
		case "restart", "reboot":
			if err := cli.Restart(ctx, dep.Container, 30); err != nil {
				return err
			}
		default:
			return fmt.Errorf("heal action %q does not apply to docker (use start, restart or command)", h.Action)
		}
		_, err = cli.WaitHealthy(ctx, dep.Container, 3*time.Minute)
		return err
	case config.KindKubernetes:
		cli, err := v.Registry.Kube(dep.Target)
		if err != nil {
			return err
		}
		switch h.Action {
		case "rollout-restart", "restart":
			if err := cli.RolloutRestart(ctx, dep.Namespace, dep.Resource); err != nil {
				return err
			}
			time.Sleep(2 * time.Second)
		case "scale", "start":
			n := h.Replicas
			if n == 0 {
				n = 1
			}
			rec.Info(step, "scaling %s to %d", dep.Resource, n)
			if err := cli.Scale(ctx, dep.Namespace, dep.Resource, n); err != nil {
				return err
			}
		default:
			return fmt.Errorf("heal action %q does not apply to kubernetes (use rollout-restart, scale or command)", h.Action)
		}
		_, err = cli.WaitReady(ctx, dep.Namespace, dep.Resource, 5*time.Minute)
		return err
	case config.KindEC2:
		cli, err := v.Registry.AWS(dep.Target)
		if err != nil {
			return err
		}
		switch h.Action {
		case "start":
			inst, err := cli.Instance(ctx, dep.InstanceID)
			if err != nil {
				return err
			}
			switch inst.State {
			case "stopped":
				rec.Info(step, "starting %s", dep.InstanceID)
				if err := cli.StartInstances(ctx, dep.InstanceID); err != nil {
					return err
				}
			case "stopping":
				rec.Info(step, "instance is stopping, waiting before start")
				if _, err := cli.WaitInstance(ctx, dep.InstanceID, "stopped", 5*time.Minute); err != nil {
					return err
				}
				if err := cli.StartInstances(ctx, dep.InstanceID); err != nil {
					return err
				}
			case "running", "pending":
				rec.Info(step, "instance already %s, waiting for status checks", inst.State)
			default:
				return fmt.Errorf("instance is %s and cannot be started", inst.State)
			}
		case "reboot", "restart":
			rec.Info(step, "rebooting %s", dep.InstanceID)
			if err := cli.RebootInstances(ctx, dep.InstanceID); err != nil {
				return err
			}
			time.Sleep(10 * time.Second)
		default:
			return fmt.Errorf("heal action %q does not apply to ec2 (use start, reboot or command)", h.Action)
		}
		_, err = cli.WaitInstance(ctx, dep.InstanceID, "running", 6*time.Minute)
		return err
	}
	return fmt.Errorf("heal action %q needs a command block for %s dependencies", h.Action, dep.Kind)
}

// looksLikePath reports whether a heal script value names a file rather
// than holding inline shell.
func looksLikePath(s string) bool {
	if len(s) > 200 || strings.ContainsAny(s, "\n|;&$ ") {
		return false
	}
	return strings.HasSuffix(s, ".sh") || strings.Contains(s, "/")
}
