package brand

import (
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
	if ModeForFile("/srv/brand/brand.css") != CSS || ModeForFile("x.HTML") != HTML || ModeForFile("a.js") != JSON || ModeForFile("seed.sql") != SQL || ModeForFile("notes") != Raw {
		t.Fatal("mode for file")
	}
}
