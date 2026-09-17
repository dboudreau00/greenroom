// Package registry turns the target blocks of an environment into live
// clients, lazily and once per target, and exposes them behind the
// Executor, Copier and Pinger contracts.
package registry

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/dboudreau00/greenroom/internal/config"
	"github.com/dboudreau00/greenroom/internal/targets"
	"github.com/dboudreau00/greenroom/internal/targets/aws"
	"github.com/dboudreau00/greenroom/internal/targets/docker"
	"github.com/dboudreau00/greenroom/internal/targets/kube"
	"github.com/dboudreau00/greenroom/internal/targets/local"
	"github.com/dboudreau00/greenroom/internal/targets/ssh"
)

type entry struct {
	once   sync.Once
	client any
	err    error
}

// Registry holds the clients for one environment.
type Registry struct {
	env     *config.Environment
	mu      sync.Mutex
	entries map[string]*entry
	local   *local.Client
}

// New creates a registry for env.
func New(env *config.Environment) *Registry {
	return &Registry{env: env, entries: map[string]*entry{}, local: local.New("")}
}

// Names lists target names in order.
func (r *Registry) Names() []string {
	var out []string
	for n := range r.env.Targets {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Target returns the config block.
func (r *Registry) Target(name string) (config.Target, error) {
	t, ok := r.env.Targets[name]
	if !ok {
		return config.Target{}, fmt.Errorf("unknown target %q", name)
	}
	return t, nil
}

// Kind returns the target kind, or "" when unknown.
func (r *Registry) Kind(name string) string {
	return r.env.Targets[name].Kind
}

func (r *Registry) get(name string) (any, error) {
	if name == "" {
		return r.local, nil
	}
	t, ok := r.env.Targets[name]
	if !ok {
		return nil, fmt.Errorf("unknown target %q", name)
	}
	r.mu.Lock()
	e, ok := r.entries[name]
	if !ok {
		e = &entry{}
		r.entries[name] = e
	}
	r.mu.Unlock()
	e.once.Do(func() {
		e.client, e.err = build(t)
		if e.err != nil {
			e.err = fmt.Errorf("target %s: %w", name, e.err)
		}
	})
	return e.client, e.err
}

func build(t config.Target) (any, error) {
	switch t.Kind {
	case config.TargetDocker:
		return docker.New(docker.Options{Host: t.Host, CertFile: t.CertFile, KeyFile: t.KeyFile, CAFile: t.CAFile})
	case config.TargetKubernetes:
		return kube.New(kube.Options{Kubeconfig: t.Kubeconfig, Context: t.Context, Namespace: t.Namespace, Server: t.Server,
			Token: t.Token, TokenFile: t.TokenFile, TokenCommand: t.TokenCommand, CAFile: t.CAFile, Insecure: t.Insecure})
	case config.TargetAWS:
		return aws.NewClient(t.Region, t.Profile)
	case config.TargetSSH:
		return ssh.New(ssh.Options{Host: t.Host, User: t.User, Port: t.Port, IdentityFile: t.IdentityFile, Args: t.Args, Shell: t.Shell})
	case config.TargetLocal:
		return local.New(t.Shell), nil
	}
	return nil, fmt.Errorf("unknown target kind %q", t.Kind)
}

// Executor returns the executor for a target ("" means local).
func (r *Registry) Executor(name string) (targets.Executor, error) {
	c, err := r.get(name)
	if err != nil {
		return nil, err
	}
	ex, ok := c.(targets.Executor)
	if !ok {
		return nil, fmt.Errorf("target %s cannot run commands", name)
	}
	return ex, nil
}

// Copier returns the copier for a target.
func (r *Registry) Copier(name string) (targets.Copier, error) {
	c, err := r.get(name)
	if err != nil {
		return nil, err
	}
	cp, ok := c.(targets.Copier)
	if !ok {
		return nil, fmt.Errorf("target %s cannot receive files", name)
	}
	return cp, nil
}

// Docker returns the docker client for a target.
func (r *Registry) Docker(name string) (*docker.Client, error) {
	c, err := r.get(name)
	if err != nil {
		return nil, err
	}
	d, ok := c.(*docker.Client)
	if !ok {
		return nil, fmt.Errorf("target %s is not a docker target", name)
	}
	return d, nil
}

// Kube returns the kubernetes client for a target.
func (r *Registry) Kube(name string) (*kube.Client, error) {
	c, err := r.get(name)
	if err != nil {
		return nil, err
	}
	k, ok := c.(*kube.Client)
	if !ok {
		return nil, fmt.Errorf("target %s is not a kubernetes target", name)
	}
	return k, nil
}

// AWS returns the aws client for a target.
func (r *Registry) AWS(name string) (*aws.Client, error) {
	c, err := r.get(name)
	if err != nil {
		return nil, err
	}
	a, ok := c.(*aws.Client)
	if !ok {
		return nil, fmt.Errorf("target %s is not an aws target", name)
	}
	return a, nil
}

// Ping checks a target is reachable and describes it.
func (r *Registry) Ping(ctx context.Context, name string) (string, error) {
	c, err := r.get(name)
	if err != nil {
		return "", err
	}
	p, ok := c.(targets.Pinger)
	if !ok {
		return "reachable", nil
	}
	return p.Ping(ctx)
}

// Local returns the local executor.
func (r *Registry) Local() *local.Client { return r.local }
