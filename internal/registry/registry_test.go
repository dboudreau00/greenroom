package registry

import (
	"context"
	"strings"
	"testing"

	"github.com/dboudreau00/greenroom/internal/config"
)

func TestRegistry(t *testing.T) {
	env := &config.Environment{Name: "t", Targets: map[string]config.Target{
		"docker": {Kind: config.TargetDocker, Host: "tcp://127.0.0.1:1"},
		"bad":    {Kind: config.TargetDocker, Host: "ftp://nope"},
		"local":  {Kind: config.TargetLocal},
		"box":    {Kind: config.TargetSSH, Host: "h"},
		"aws":    {Kind: config.TargetAWS, Region: "us-east-1"},
	}}
	r := New(env)
	if got := strings.Join(r.Names(), ","); got != "aws,bad,box,docker,local" {
		t.Fatalf("names %s", got)
	}
	if ex, err := r.Executor(""); err != nil || ex == nil {
		t.Fatalf("empty target should be local: %v", err)
	}
	if _, err := r.Executor("docker"); err != nil {
		t.Fatal(err)
	}
	d1, err := r.Docker("docker")
	if err != nil {
		t.Fatal(err)
	}
	d2, _ := r.Docker("docker")
	if d1 != d2 {
		t.Fatal("client not cached")
	}
	if _, err := r.Kube("docker"); err == nil || !strings.Contains(err.Error(), "not a kubernetes target") {
		t.Fatalf("kube on docker: %v", err)
	}
	if _, err := r.AWS("aws"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Docker("aws"); err == nil {
		t.Fatal("docker on aws accepted")
	}
	if _, err := r.Executor("nope"); err == nil || !strings.Contains(err.Error(), "unknown target") {
		t.Fatalf("unknown: %v", err)
	}
	if _, err := r.Executor("bad"); err == nil || !strings.Contains(err.Error(), "target bad") {
		t.Fatalf("bad host: %v", err)
	}
	if _, err := r.Executor("bad"); err == nil {
		t.Fatal("construction error should be cached and repeated")
	}
	if _, err := r.Copier("box"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Copier("aws"); err != nil {
		t.Fatal(err)
	}
	if r.Kind("aws") != "aws" || r.Kind("nope") != "" {
		t.Fatal("kind")
	}
	if _, err := r.Target("nope"); err == nil {
		t.Fatal("unknown target block")
	}
	if msg, err := r.Ping(context.Background(), "local"); err != nil || !strings.HasPrefix(msg, "local ") {
		t.Fatalf("ping %q %v", msg, err)
	}
	if _, err := r.Ping(context.Background(), "docker"); err == nil {
		t.Fatal("dead docker endpoint pinged ok")
	}
	if r.Local() == nil {
		t.Fatal("local executor")
	}
}
