package engine

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dboudreau00/greenroom/internal/brand"
	"github.com/dboudreau00/greenroom/internal/seeds"
	"github.com/dboudreau00/greenroom/internal/webfetch"
)

func brandSite(t *testing.T) *httptest.Server {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 40, 20))
	for y := 0; y < 20; y++ {
		for x := 0; x < 40; x++ {
			img.Set(x, y, color.RGBA{0x7c, 0x3a, 0xed, 255})
		}
	}
	var logo bytes.Buffer
	png.Encode(&logo, img)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html")
			io.WriteString(w, `<title>Larkspur &amp; Co</title><meta name="theme-color" content="#7c3aed"><img class="logo" src="/logo.png">`)
		case "/logo.png":
			w.Header().Set("Content-Type", "image/png")
			w.Write(logo.Bytes())
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestPersonalizedReset(t *testing.T) {
	f := newFixture(t)
	os.WriteFile(f.workerFile, []byte("x"), 0o644)
	f.eng.fetcher = webfetch.New(webfetch.Options{AllowPrivate: true})
	site := brandSite(t)
	ctx := context.Background()
	kit, err := f.eng.FetchBrand(ctx, brand.FetchRequest{URL: site.URL, Slug: "larkspur"}, "test")
	if err != nil {
		t.Fatal(err)
	}
	if kit.Name != "Larkspur & Co" || kit.Primary != "#7c3aed" || kit.Assets["logo"].File != "logo.png" {
		t.Fatalf("kit: %+v", kit)
	}
	out := filepath.Join(f.dir, "out")
	f.writeSeed(t, "brandy", 1, seeds.Manifest{Steps: []seeds.Step{
		{Name: "hello", Kind: seeds.StepCommand, Target: "local", Command: helper("echo", "hello {{brand.name}}"), Personalize: true},
		{Name: "literal", Kind: seeds.StepCommand, Target: "local", Command: helper("echo", "untouched {{brand.name}}")},
		{Name: "page", Kind: seeds.StepCopy, Target: "local", File: "page.html", Dest: filepath.ToSlash(filepath.Join(out, "{{brand.slug}}.html")), Personalize: true},
		{Name: "logo", Kind: seeds.StepAsset, Target: "local", Asset: "logo", File: "fallback.svg", Dest: filepath.ToSlash(out) + "/"},
		{Name: "hero", Kind: seeds.StepAsset, Target: "local", Asset: "hero", Dest: filepath.ToSlash(out) + "/"},
	}}, map[string]string{
		"page.html":    `<h1>{{brand.name}}</h1><i style="color:{{brand.primary}}">{{brand.logo}}</i>`,
		"fallback.svg": `<svg xmlns="http://www.w3.org/2000/svg"/>`,
	})
	if err := f.eng.Reload(); err != nil {
		t.Fatal(err)
	}
	run, err := f.eng.Reset(ctx, "demo", ResetOptions{Persona: "brandy", Brand: "larkspur", Actor: "test"})
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
	logText := ""
	for _, l := range run.Log {
		logText += l.Text + "\n"
	}
	if !strings.Contains(logText, "hello Larkspur & Co") || !strings.Contains(logText, "untouched {{brand.name}}") {
		t.Fatalf("command personalization:\n%s", logText)
	}
	if run.Brand != "larkspur" {
		t.Fatalf("run brand %q", run.Brand)
	}
	page, err := os.ReadFile(filepath.Join(out, "larkspur.html"))
	if err != nil || string(page) != `<h1>Larkspur &amp; Co</h1><i style="color:#7c3aed">logo.png</i>` {
		t.Fatalf("page: %v %s", err, page)
	}
	if b, err := os.ReadFile(filepath.Join(out, "logo.png")); err != nil || !bytes.HasPrefix(b, []byte("\x89PNG")) {
		t.Fatalf("logo not copied from the kit: %v", err)
	}
	if !strings.Contains(logText, "no hero in the brand kit") {
		t.Fatalf("missing asset without fallback should be skipped:\n%s", logText)
	}
	applied, _ := f.eng.Store().LoadApplied("demo")
	if applied.Brand == nil || applied.Brand.Slug != "larkspur" || applied.Brand.Hash != kit.Hash() {
		t.Fatalf("applied brand: %+v", applied.Brand)
	}
	v, _ := f.eng.Env("demo")
	rep, _ := f.eng.StaleReport(ctx, v, nil, false)
	if strings.Contains(itemCodes(rep), "brand-") {
		t.Fatalf("fresh kit reported: %s", itemCodes(rep))
	}
	tag := "Freight, on time"
	if _, err := f.eng.Brands().Update("larkspur", brand.Edit{Tagline: &tag}, "test"); err != nil {
		t.Fatal(err)
	}
	rep, _ = f.eng.StaleReport(ctx, v, nil, false)
	if !strings.Contains(itemCodes(rep), "brand-changed") {
		t.Fatalf("edit not reported: %s", itemCodes(rep))
	}
	f.eng.Brands().Delete("larkspur")
	rep, _ = f.eng.StaleReport(ctx, v, nil, false)
	if !strings.Contains(itemCodes(rep), "brand-missing") {
		t.Fatalf("deletion not reported: %s", itemCodes(rep))
	}

	// Without a kit: house brand, fallback asset.
	os.RemoveAll(out)
	run, err = f.eng.Reset(ctx, "demo", ResetOptions{Persona: "brandy", Actor: "test"})
	if err != nil {
		t.Fatalf("house reset: %v", err)
	}
	page, _ = os.ReadFile(filepath.Join(out, "default.html"))
	if string(page) != `<h1>Demo</h1><i style="color:#22c55e"></i>` {
		t.Fatalf("house page: %s", page)
	}
	if _, err := os.Stat(filepath.Join(out, "fallback.svg")); err != nil {
		t.Fatalf("fallback asset not copied: %v", err)
	}
	if _, err := f.eng.StartReset("demo", ResetOptions{Persona: "brandy", Brand: "nope"}); err == nil {
		t.Fatal("unknown brand accepted")
	}
}

func TestPersonalizeTypoFailsStep(t *testing.T) {
	f := newFixture(t)
	os.WriteFile(f.workerFile, []byte("x"), 0o644)
	f.writeSeed(t, "typo", 1, seeds.Manifest{Steps: []seeds.Step{
		{Name: "say", Kind: seeds.StepCommand, Target: "local", Command: helper("echo", "{{brand.nmae}}"), Personalize: true},
	}}, nil)
	f.eng.Reload()
	if _, err := f.eng.Reset(context.Background(), "demo", ResetOptions{Persona: "typo", Actor: "test"}); err == nil || !strings.Contains(err.Error(), "brand.nmae") {
		t.Fatalf("typo: %v", err)
	}
}
