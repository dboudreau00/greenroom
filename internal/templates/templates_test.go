package templates

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dboudreau00/greenroom/internal/config"
	"github.com/dboudreau00/greenroom/internal/seeds"
)

func builtinByName(t *testing.T, name string) *Template {
	t.Helper()
	list, err := Builtin()
	if err != nil {
		t.Fatal(err)
	}
	tpl, ok := Find(list, name)
	if !ok {
		t.Fatalf("template %s not found", name)
	}
	return tpl
}

// Every built-in template, with every module, must produce an environment
// and seed sets that load without a single problem.
func TestBuiltinTemplatesRenderClean(t *testing.T) {
	list, err := Builtin()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) < 3 {
		t.Fatalf("expected at least 3 built-in templates, got %d", len(list))
	}
	for _, tpl := range list {
		if len(tpl.Problems) > 0 {
			t.Errorf("%s: %v", tpl.Name, tpl.Problems)
			continue
		}
		var all []string
		for _, m := range tpl.Modules {
			all = append(all, m.Name)
		}
		for _, mods := range [][]string{nil, all} {
			plan, err := tpl.Render(Request{Name: "t-" + tpl.Name, Title: "Test " + tpl.Name, Modules: mods})
			if err != nil {
				t.Errorf("%s %v: %v", tpl.Name, mods, err)
				continue
			}
			dir := filepath.Join(t.TempDir(), plan.Name)
			if err := plan.Write(dir); err != nil {
				t.Fatal(err)
			}
			env, err := config.LoadEnvironment(dir)
			if err != nil {
				t.Errorf("%s %v: environment does not load: %v", tpl.Name, mods, err)
				continue
			}
			if env.Template == nil || env.Template.Name != tpl.Name {
				t.Errorf("%s: template ref not recorded: %+v", tpl.Name, env.Template)
			}
			sets, err := seeds.Discover(dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(sets) != len(plan.Personas) {
				t.Errorf("%s: %d seed sets for %d personas", tpl.Name, len(sets), len(plan.Personas))
			}
			for _, s := range sets {
				if len(s.Problems) > 0 {
					t.Errorf("%s %s: %v", tpl.Name, s.ID(), s.Problems)
				}
				for _, st := range s.Steps {
					if st.Kind == seeds.StepSQL {
						if _, ok := env.Dependency(st.Dependency); !ok {
							t.Errorf("%s %s: step %s uses unknown dependency %s", tpl.Name, s.ID(), st.Name, st.Dependency)
						}
					}
				}
			}
			if _, ok := seeds.Find(sets, env.Pin.Persona, env.Pin.Version); !ok {
				t.Errorf("%s: pin %s@v%d has no seed set", tpl.Name, env.Pin.Persona, env.Pin.Version)
			}
			for _, rel := range plan.Paths() {
				if strings.Contains(string(plan.Files[rel]), "{{") && !strings.Contains(string(plan.Files[rel]), "{{brand.") {
					t.Errorf("%s: %s still has a placeholder", tpl.Name, rel)
				}
			}
		}
	}
}

func TestModulesResolveRequiresAndDefaults(t *testing.T) {
	tpl := builtinByName(t, "saas-web")
	mods, err := tpl.ResolveModules(nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(mods, ",") != "accounts,billing,analytics" {
		t.Fatalf("defaults: %v", mods)
	}
	mods, _ = tpl.ResolveModules([]string{"support"})
	if strings.Join(mods, ",") != "accounts,support" {
		t.Fatalf("support pulls in the required module only: %v", mods)
	}
	if _, err := tpl.ResolveModules([]string{"nope"}); err == nil {
		t.Fatal("unknown module accepted")
	}
	k := builtinByName(t, "k8s-microservices")
	mods, _ = k.ResolveModules([]string{"payments"})
	if strings.Join(mods, ",") != "catalog,orders,payments" {
		t.Fatalf("payments requires orders requires catalog: %v", mods)
	}
}

func TestPersonasFollowModules(t *testing.T) {
	tpl := builtinByName(t, "saas-web")
	plan, err := tpl.Render(Request{Name: "ops-only", Modules: []string{"inventory"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(plan.Personas, ",") != "overview,ops" {
		t.Fatalf("personas for inventory only: %v", plan.Personas)
	}
	if plan.Pin != "overview@v1" {
		t.Fatalf("pin: %s", plan.Pin)
	}
	// The ops seed set carries the required accounts module too.
	m := string(plan.Files["seeds/ops/v1/seed.json"])
	if !strings.Contains(m, "accounts/001_schema.sql") || !strings.Contains(m, "inventory/002_data.sql") || strings.Contains(m, "billing") {
		t.Fatalf("ops manifest: %s", m)
	}
	// Inventory drift checks only apply to the seed sets that create stock;
	// the required accounts module is in every seed set and stays unscoped.
	mixed, err := tpl.Render(Request{Name: "mixed", Modules: []string{"support", "inventory"}})
	if err != nil {
		t.Fatal(err)
	}
	var env config.Environment
	if err := json.Unmarshal(mixed.Files[config.EnvironmentFile], &env); err != nil {
		t.Fatal(err)
	}
	pg, _ := env.Dependency("postgres")
	scopes := map[string]string{}
	for _, dc := range pg.Drift {
		scopes[dc.Name] = strings.Join(dc.Personas, ",")
	}
	if scopes["stock-units"] != "overview,ops" || scopes["tickets"] != "overview,support-lead" || scopes["accounts"] != "" || scopes["sessions"] != "" {
		t.Fatalf("drift scopes: %v", scopes)
	}
	if _, err := tpl.Render(Request{Name: "x", Modules: []string{"inventory"}, Persona: "cfo"}); err == nil {
		t.Fatal("cfo persona allowed without billing or analytics")
	}
	plan, err = tpl.Render(Request{Name: "fin", Modules: []string{"billing"}, Persona: "cfo"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Pin != "cfo@v1" {
		t.Fatalf("pin: %s", plan.Pin)
	}
}

func TestParamsValidatedAndSubstituted(t *testing.T) {
	tpl := builtinByName(t, "saas-web")
	plan, err := tpl.Render(Request{Name: "demo.one", Title: "Demo & Co", Params: map[string]string{"webPort": "9100"}})
	if err != nil {
		t.Fatal(err)
	}
	compose := string(plan.Files["docker-compose.yml"])
	if !strings.Contains(compose, `"9100:80"`) || !strings.Contains(compose, "container_name: demo-one-web") {
		t.Fatalf("compose not rendered: %s", compose)
	}
	page := string(plan.Files["web/index.html"])
	if !strings.Contains(page, "<h1>Demo &amp; Co</h1>") {
		t.Fatalf("title not escaped in html: %s", page)
	}
	for _, bad := range []map[string]string{
		{"webPort": "80; rm -rf /"},
		{"webPort": "${AWS_SECRET_ACCESS_KEY}"},
		{"nope": "1"},
	} {
		if _, err := tpl.Render(Request{Name: "x", Params: bad}); err == nil {
			t.Fatalf("params %v accepted", bad)
		}
	}
	for _, title := range []string{`Quote "me"`, "<script>", "new\nline"} {
		if _, err := tpl.Render(Request{Name: "x", Title: title}); err == nil {
			t.Fatalf("title %q accepted", title)
		}
	}
	if _, err := tpl.Render(Request{Name: "../escape"}); err == nil {
		t.Fatal("bad environment name accepted")
	}
}

func TestWorkspaceTemplateOverridesAndFileRefsStayInside(t *testing.T) {
	dir := t.TempDir()
	tdir := filepath.Join(dir, "saas-web")
	os.MkdirAll(filepath.Join(tdir, "modules", "core"), 0o755)
	os.WriteFile(filepath.Join(tdir, "modules", "core", "a.sql"), []byte("SELECT 1;"), 0o644)
	os.WriteFile(filepath.Join(tdir, ManifestFile), []byte(`{
	  "name": "saas-web", "title": "Local override",
	  "environment": {"dependencies": [{"name": "db", "kind": "postgres", "dsn": "postgres://x@h/db"}]},
	  "modules": [{"name": "core", "required": true, "steps": [{"name": "a", "kind": "sql", "dependency": "db", "file": "modules/core/a.sql"}]}],
	  "personas": [{"name": "all", "modules": ["*"]}]
	}`), 0o644)
	list, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	tpl, _ := Find(list, "saas-web")
	if tpl.Source != "workspace" || tpl.Title != "Local override" {
		t.Fatalf("workspace template did not override: %+v", tpl.Describe())
	}
	if _, ok := Find(list, "k8s-microservices"); !ok {
		t.Fatal("built-ins missing from the merged list")
	}
	plan, err := tpl.Render(Request{Name: "local"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := plan.Files["seeds/all/v1/core/a.sql"]; !ok {
		t.Fatalf("module file not copied: %v", plan.Paths())
	}

	// A step pointing outside its module is refused.
	os.WriteFile(filepath.Join(tdir, ManifestFile), []byte(`{
	  "name": "saas-web",
	  "environment": {"dependencies": [{"name": "db", "kind": "postgres", "dsn": "postgres://x@h/db"}]},
	  "modules": [{"name": "core", "required": true, "steps": [{"name": "a", "kind": "sql", "dependency": "db", "file": "modules/core/../../template.json"}]}],
	  "personas": [{"name": "all", "modules": ["*"]}]
	}`), 0o644)
	list, _ = List(dir)
	tpl, _ = Find(list, "saas-web")
	if _, err := tpl.Render(Request{Name: "local"}); err == nil || !strings.Contains(err.Error(), "must live under") {
		t.Fatalf("escaping file ref: %v", err)
	}
}

func TestBrokenTemplateReportsProblems(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "broken"), 0o755)
	os.WriteFile(filepath.Join(dir, "broken", ManifestFile), []byte(`{"name":"broken","environment":{},
	  "modules":[{"name":"a","requires":["missing"]}],"personas":[{"name":"p","modules":["zzz"]}],
	  "params":[{"name":"env"}]}`), 0o644)
	list, _ := List(dir)
	tpl, ok := Find(list, "broken")
	if !ok {
		t.Fatal("broken template not listed")
	}
	joined := strings.Join(tpl.Problems, "; ")
	for _, want := range []string{"requires unknown module", "unknown module \"zzz\"", "reserved name"} {
		if !strings.Contains(joined, want) {
			t.Errorf("problems %q lack %q", joined, want)
		}
	}
	if _, err := tpl.Render(Request{Name: "x"}); err == nil {
		t.Fatal("broken template rendered")
	}
}

// Private demo addresses are fine; link-local and cloud metadata hosts are
// refused in any param, as a URL, host:port or bare host.
func TestParamsRefuseMetadataHosts(t *testing.T) {
	tpl := builtinByName(t, "warehouse-bastion")
	ok := []map[string]string{
		{"appUrl": "http://127.0.0.1:8080/healthz"},
		{"appUrl": "https://10.0.4.7/healthz", "certAddress": "10.0.4.7:443"},
		{"warehouseHost": "warehouse.internal"},
		{"warehouseHost": "192.168.1.20"},
		{"warehouseHost": "metadata-cache.internal"},
	}
	for _, p := range ok {
		if _, err := tpl.Render(Request{Name: "wh", Params: p}); err != nil {
			t.Errorf("%v refused: %v", p, err)
		}
	}
	bad := []map[string]string{
		{"appUrl": "http://169.254.169.254/latest/meta-data/"},
		{"appUrl": "http://metadata.google.internal/computeMetadata/v1/"},
		{"appUrl": "http://METADATA.Google.Internal./x"},
		{"appUrl": "http://0xa9fea9fe/latest"},
		{"appUrl": "http://2852039166/latest"},
		{"appUrl": "http://0.0.0.0:8080/x"},
		{"certAddress": "169.254.170.2:80"},
		{"certAddress": "instance-data.ec2.internal:80"},
		{"warehouseHost": "169.254.169.254"},
		{"warehouseHost": "metadata"},
		{"warehouseHost": "instance-data."},
	}
	for _, p := range bad {
		_, err := tpl.Render(Request{Name: "wh", Params: p})
		if err == nil || !strings.Contains(err.Error(), "metadata") {
			t.Errorf("%v accepted: %v", p, err)
		}
	}
	for _, v := range []string{"fe80::1", "[fe80::1%eth0]:80", "::", "fd00:ec2::254", "http://[fd00:ec2::254]/latest", "::ffff:169.254.169.254", "0.0.0.0"} {
		if !metadataHost(v) {
			t.Errorf("%s not caught", v)
		}
	}
	for _, v := range []string{"9100", "0.9", "1.5.2", "10:30", "postgres:16", "fd00::1", "https://shop.demo.example.com/healthz", "redis://user@10.1.2.3:6379/0"} {
		if metadataHost(v) {
			t.Errorf("%s caught", v)
		}
	}
}
