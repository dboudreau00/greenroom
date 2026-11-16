package main

import (
	"bytes"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReorder(t *testing.T) {
	fs := flag.NewFlagSet("x", flag.ContinueOnError)
	fs.Bool("json", false, "")
	fs.String("persona", "", "")
	got := reorder(fs, []string{"env", "--json", "--persona", "cfo", "extra", "--unknown"})
	want := "--json --persona cfo --unknown env extra"
	if strings.Join(got, " ") != want {
		t.Fatalf("got %q want %q", strings.Join(got, " "), want)
	}
	got = reorder(fs, []string{"env", "--", "--json"})
	if strings.Join(got, " ") != "env --json" {
		t.Fatalf("double dash: %q", strings.Join(got, " "))
	}
	got = reorder(fs, []string{"--persona=cfo", "env"})
	if strings.Join(got, " ") != "--persona=cfo env" {
		t.Fatalf("attached value: %q", strings.Join(got, " "))
	}
}

func TestCLIFlow(t *testing.T) {
	dir := t.TempDir()
	var out, errb bytes.Buffer
	code := run([]string{"-C", dir, "init"}, &out, &errb)
	if code != 0 {
		t.Fatalf("init: %d %s %s", code, out.String(), errb.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "environments", "acme-demo", "environment.json")); err != nil {
		t.Fatal(err)
	}
	if code := run([]string{"-C", dir, "init"}, io.Discard, io.Discard); code != 1 {
		t.Fatalf("second init should refuse: %d", code)
	}
	out.Reset()
	if code := run([]string{"-C", dir, "validate"}, &out, &errb); code != 0 || !strings.Contains(out.String(), "3 seed sets, ok") {
		t.Fatalf("validate: %d %s", code, out.String())
	}
	out.Reset()
	if code := run([]string{"-C", dir, "envs"}, &out, &errb); code != 0 || !strings.Contains(out.String(), "acme-demo") || !strings.Contains(out.String(), "cfo@v2") {
		t.Fatalf("envs: %d %s", code, out.String())
	}
	out.Reset()
	if code := run([]string{"-C", dir, "seeds", "acme-demo"}, &out, &errb); code != 0 || !strings.Contains(out.String(), "retail-ops@v1") || !strings.Contains(out.String(), "pinned") {
		t.Fatalf("seeds: %d %s", code, out.String())
	}
	out.Reset()
	if code := run([]string{"-C", dir, "seeds", "acme-demo", "--json"}, &out, &errb); code != 0 || !strings.Contains(out.String(), `"hash"`) {
		t.Fatalf("seeds json: %d", code)
	}
	out.Reset()
	// Point every dependency at ports nobody listens on so the check is RED
	// (exit 2) whether or not the example stack happens to be running.
	t.Setenv("ACME_PG_DSN", "postgres://demo:demo@127.0.0.1:1/demo?sslmode=disable&connect_timeout=2")
	t.Setenv("ACME_PG_ADDR", "127.0.0.1:1")
	t.Setenv("ACME_WEB_URL", "http://127.0.0.1:1/")
	t.Setenv("DOCKER_HOST", "tcp://127.0.0.1:1")
	code = run([]string{"-C", dir, "check", "acme-demo", "--no-drift"}, &out, &errb)
	if code != 2 || !strings.HasPrefix(out.String(), "RED") {
		t.Fatalf("check: %d %s %s", code, out.String(), errb.String())
	}
	out.Reset()
	if code := run([]string{"-C", dir, "stale", "acme-demo", "--no-drift"}, &out, &errb); code != 1 || !strings.Contains(out.String(), "never been reset") {
		t.Fatalf("stale: %d %s", code, out.String())
	}
	out.Reset()
	if code := run([]string{"version"}, &out, &errb); code != 0 || !strings.HasPrefix(out.String(), "greenroom ") {
		t.Fatalf("version: %d %s", code, out.String())
	}
	if code := run([]string{"bogus"}, io.Discard, io.Discard); code != 3 {
		t.Fatalf("unknown command: %d", code)
	}
	out.Reset()
	if code := run(nil, &out, &errb); code != 0 || !strings.Contains(out.String(), "Usage:") {
		t.Fatalf("help: %d", code)
	}
	if code := run([]string{"-C", t.TempDir(), "envs"}, io.Discard, &errb); code != 3 || !strings.Contains(errb.String(), "no environments") {
		t.Fatalf("empty workspace: %d %s", code, errb.String())
	}
}

func TestTemplatesAndNew(t *testing.T) {
	dir := t.TempDir()
	var out, errb bytes.Buffer
	if code := run([]string{"-C", dir, "templates"}, &out, &errb); code != 0 || !strings.Contains(out.String(), "saas-web") || !strings.Contains(out.String(), "k8s-microservices") {
		t.Fatalf("templates: %d %s %s", code, out.String(), errb.String())
	}
	out.Reset()
	if code := run([]string{"-C", dir, "templates", "warehouse-bastion"}, &out, &errb); code != 0 || !strings.Contains(out.String(), "instanceId") {
		t.Fatalf("templates detail: %d %s", code, out.String())
	}
	out.Reset()
	code := run([]string{"-C", dir, "new", "crm-demo", "--template", "saas-web", "--modules", "billing,support", "--persona", "cfo", "--set", "webPort=8123", "--title", "CRM demo"}, &out, &errb)
	if code != 0 || !strings.Contains(out.String(), "created crm-demo") || !strings.Contains(out.String(), "pinned to cfo@v1") {
		t.Fatalf("new: %d %s %s", code, out.String(), errb.String())
	}
	b, err := os.ReadFile(filepath.Join(dir, "environments", "crm-demo", "docker-compose.yml"))
	if err != nil || !strings.Contains(string(b), "8123:80") {
		t.Fatalf("compose: %v %s", err, b)
	}
	out.Reset()
	if code := run([]string{"-C", dir, "validate"}, &out, &errb); code != 0 || !strings.Contains(out.String(), "crm-demo: 5 dependencies") {
		t.Fatalf("validate after new: %d %s", code, out.String())
	}
	errb.Reset()
	if code := run([]string{"-C", dir, "new", "crm-demo", "--template", "saas-web"}, io.Discard, &errb); code != 3 || !strings.Contains(errb.String(), "already exists") {
		t.Fatalf("second new: %d %s", code, errb.String())
	}
	errb.Reset()
	if code := run([]string{"-C", dir, "new", "x", "--template", "saas-web", "--set", "webPort=80;id"}, io.Discard, &errb); code != 3 || !strings.Contains(errb.String(), "does not match") {
		t.Fatalf("bad param: %d %s", code, errb.String())
	}
	out.Reset()
	if code := run([]string{"-C", dir, "new", "dry", "--template", "k8s-microservices", "--modules", "payments", "--dry-run"}, &out, &errb); code != 0 || !strings.Contains(out.String(), "catalog, orders, payments") {
		t.Fatalf("dry run: %d %s", code, out.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "environments", "dry")); err == nil {
		t.Fatal("dry run wrote files")
	}
}
