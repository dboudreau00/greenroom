package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dboudreau00/greenroom/internal/config"
	"github.com/dboudreau00/greenroom/internal/seeds"
	"github.com/dboudreau00/greenroom/internal/templates"
)

func TestCreateEnvironmentFromTemplate(t *testing.T) {
	f := newFixture(t)
	plan, err := f.eng.CreateEnvironment("saas-web", templates.Request{Name: "crm", Title: "CRM", Modules: []string{"support"}}, false)
	if err != nil {
		t.Fatal(err)
	}
	v, err := f.eng.Env("crm")
	if err != nil {
		t.Fatalf("not loaded after create: %v", err)
	}
	if v.Config.Template == nil || strings.Join(v.Config.Template.Modules, ",") != "accounts,support" {
		t.Fatalf("template ref: %+v", v.Config.Template)
	}
	if len(v.Seeds) != len(plan.Personas) {
		t.Fatalf("seed sets %d, personas %v", len(v.Seeds), plan.Personas)
	}
	if s := f.eng.Summary(context.Background(), v); s.Template == nil || s.Template.ProductType != "SaaS web app" {
		t.Fatalf("summary template: %+v", s.Template)
	}
	// No hidden staging directory is left behind.
	entries, _ := os.ReadDir(filepath.Dir(v.Config.Dir))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			t.Fatalf("leftover %s", e.Name())
		}
	}
	if _, err := f.eng.CreateEnvironment("saas-web", templates.Request{Name: "crm"}, false); !errors.Is(err, ErrExists) {
		t.Fatalf("duplicate: %v", err)
	}
	if _, err := f.eng.CreateEnvironment("saas-web", templates.Request{Name: "demo"}, false); !errors.Is(err, ErrExists) {
		t.Fatalf("existing name: %v", err)
	}
	if _, err := f.eng.CreateEnvironment("nope", templates.Request{Name: "x"}, false); err == nil {
		t.Fatal("unknown template accepted")
	}
	plan, err = f.eng.CreateEnvironment("k8s-microservices", templates.Request{Name: "dry"}, true)
	if err != nil || plan.Name != "dry" {
		t.Fatalf("dry run: %v", err)
	}
	if _, err := f.eng.Env("dry"); err == nil {
		t.Fatal("dry run created an environment")
	}
}

// Verify checks expand ${VAR} in their url and command like steps do.
func TestVerifyExpandsVariables(t *testing.T) {
	f := newFixture(t)
	os.WriteFile(f.workerFile, []byte("x"), 0o644)
	env, err := config.LoadEnvironment(f.envDir)
	if err != nil {
		t.Fatal(err)
	}
	env.Env["API_URL"] = f.api.srv.URL
	if err := config.WriteJSON(env.Path, env); err != nil {
		t.Fatal(err)
	}
	f.writeSeed(t, "vars", 1, seeds.Manifest{
		Steps: []seeds.Step{{Name: "noop", Kind: seeds.StepCommand, Target: "local", Command: helper("echo", "ok")}},
		Verify: []seeds.Check{
			{Name: "home", Kind: "http", URL: "${API_URL}/healthz", ExpectStatus: 200},
			{Name: "cmd", Kind: "command", Target: "local", Command: helper("echo", "${API_URL}")},
		},
	}, nil)
	if err := f.eng.Reload(); err != nil {
		t.Fatal(err)
	}
	run, err := f.eng.Reset(context.Background(), "demo", ResetOptions{Persona: "vars", Actor: "test"})
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
	found := false
	for _, l := range run.Log {
		if l.Step == "verify cmd" && strings.Contains(l.Text, f.api.srv.URL) {
			found = true
		}
	}
	if !found {
		t.Fatal("command verify did not see the expanded variable")
	}
}

// Drift checks scoped to personas are only evaluated when one of those
// personas' seed sets is applied.
func TestScopedDriftChecks(t *testing.T) {
	f := newFixture(t)
	os.WriteFile(f.workerFile, []byte("x"), 0o644)
	env, err := config.LoadEnvironment(f.envDir)
	if err != nil {
		t.Fatal(err)
	}
	env.Dependencies[1].Drift = append(env.Dependencies[1].Drift, config.DriftCheck{Name: "slow-only", Command: helper("fail"), Personas: []string{"slow"}})
	if err := config.WriteJSON(env.Path, env); err != nil {
		t.Fatal(err)
	}
	if err := f.eng.Reload(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	run, err := f.eng.Reset(ctx, "demo", ResetOptions{Persona: "cfo", Version: 1, Actor: "test"})
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
	for _, l := range run.Log {
		if strings.Contains(l.Text, "slow-only") {
			t.Fatalf("scoped check evaluated during the cfo reset: %s", l.Text)
		}
	}
	v, _ := f.eng.Env("demo")
	rep, err := f.eng.StaleReport(ctx, v, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(itemCodes(rep), "drift-error") {
		t.Fatalf("scoped check reported for cfo: %+v", rep.Items)
	}
	if _, err := f.eng.Reset(ctx, "demo", ResetOptions{Persona: "slow", Actor: "test"}); err != nil {
		t.Fatalf("reset slow: %v", err)
	}
	rep, _ = f.eng.StaleReport(ctx, v, nil, true)
	if !strings.Contains(itemCodes(rep), "drift-error") {
		t.Fatalf("scoped check not evaluated for slow: %s", itemCodes(rep))
	}
	if !(config.DriftCheck{}).Applies("") || (config.DriftCheck{Personas: []string{"a"}}).Applies("") || !(config.DriftCheck{Personas: []string{"a"}}).Applies("a") {
		t.Fatal("Applies")
	}
}
