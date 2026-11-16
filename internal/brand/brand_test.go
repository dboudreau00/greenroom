package brand

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/dboudreau00/greenroom/internal/webfetch"
)

func pngBytes(t *testing.T, c color.RGBA, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			switch {
			case x < w/8:
				img.Set(x, y, color.RGBA{255, 255, 255, 255})
			case y < h/4:
				img.Set(x, y, color.RGBA{0xf5, 0x9e, 0x0b, 255})
			default:
				img.Set(x, y, c)
			}
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

const page = `<!doctype html><html><head>
<base href="/assets/">
<title>Home | Larkspur Freight</title>
<meta name="description" content="Freight that &amp; arrives.\ On time.">
<meta property="og:image" content="https://cdn.example.com/banner.jpg">
<link rel="apple-touch-icon" sizes="180x180" href="touch.png">
<link rel="icon" sizes="32x32" href="/favicon-32.png">
<script type="application/ld+json">{"@context":"https://schema.org","@graph":[{"@type":"WebSite","name":"x"},{"@type":"Organization","name":"Larkspur Freight Ltd","logo":{"@type":"ImageObject","url":"/img/logo.png"}}]}</script>
</head><body><img class="site-logo" src="javascript:alert(1)"><img alt="Larkspur logo" src="/img/header-logo.svg"></body></html>`

func TestParsePage(t *testing.T) {
	base, _ := url.Parse("https://www.larkspur.example/")
	p := ParsePage(base, []byte(page))
	if p.Name != "Larkspur Freight Ltd" {
		t.Errorf("name %q", p.Name)
	}
	if p.Description != "Freight that & arrives. On time." {
		t.Errorf("description %q", p.Description)
	}
	want := []string{
		"https://www.larkspur.example/img/logo.png",
		"https://www.larkspur.example/img/header-logo.svg",
		"https://www.larkspur.example/assets/touch.png",
		"https://www.larkspur.example/favicon-32.png",
		"https://cdn.example.com/banner.jpg",
		"https://www.larkspur.example/favicon.ico",
	}
	if strings.Join(p.Logos, " ") != strings.Join(want, " ") {
		t.Errorf("logos\n got %v\nwant %v", p.Logos, want)
	}
	if p.Icons[0] != "https://www.larkspur.example/assets/touch.png" {
		t.Errorf("icons %v", p.Icons)
	}
	p = ParsePage(base, []byte(`<title>Quillmark - Clinical analytics</title><meta name="theme-color" content="#0F62FE"><meta name="msapplication-TileColor" content="rgb(20, 30, 40)">`))
	if p.Name != "Quillmark" || strings.Join(p.Colors, ",") != "#0f62fe,#141e28" {
		t.Errorf("title name %q colors %v", p.Name, p.Colors)
	}
}

func TestCheckSVG(t *testing.T) {
	ok := []string{
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10"><defs><linearGradient id="g"/></defs><rect fill="url(#g)" width="10" height="10"/><use href="#g"/></svg>`,
		`<?xml version="1.0"?><!DOCTYPE svg PUBLIC "-//W3C//DTD SVG 1.1//EN" "http://www.w3.org/Graphics/SVG/1.1/DTD/svg11.dtd"><svg><image href="data:image/png;base64,AAAA"/></svg>`,
	}
	for _, s := range ok {
		if _, _, err := ImageType([]byte(s)); err != nil {
			t.Errorf("safe svg refused: %v\n%s", err, s)
		}
	}
	bad := []string{
		`<svg><script>alert(1)</script></svg>`,
		`<svg onload="alert(1)"></svg>`,
		`<svg><a href="javascript:alert(1)"><text>x</text></a></svg>`,
		`<svg><foreignObject><iframe src="https://x"/></foreignObject></svg>`,
		`<svg><image href="https://tracker.example/p.png"/></svg>`,
		`<svg><style>@import url(https://x/y.css);</style></svg>`,
		`<svg><rect style="fill:url(https://x/#a)"/></svg>`,
		`<!DOCTYPE svg [<!ENTITY x "y">]><svg>&x;</svg>`,
		`<svg><set attributeName="href" to="javascript:alert(1)"/></svg>`,
		`<svg><image xlink:href="data:text/html;base64,PHNjcmlwdD4="/></svg>`,
	}
	for _, s := range bad {
		if _, _, err := ImageType([]byte(s)); err == nil {
			t.Errorf("unsafe svg accepted: %s", s)
		}
	}
	if _, _, err := ImageType([]byte("<html><body>hi</body></html>")); err == nil {
		t.Error("html accepted as an image")
	}
}

func TestDominantColorsAndShade(t *testing.T) {
	b := pngBytes(t, color.RGBA{0x0f, 0x62, 0xfe, 255}, 64, 64)
	got := DominantColors(b, 2)
	if len(got) != 2 || !strings.HasPrefix(got[0], "#0") || got[1] == got[0] {
		t.Fatalf("colors %v", got)
	}
	if Shade("#ffffff", 0.5) != "#808080" || Shade("bad", 0.5) != "bad" {
		t.Fatal("shade")
	}
	if c, ok := NormalizeColor("#ABC"); !ok || c != "#aabbcc" {
		t.Fatal("normalize short hex")
	}
	if _, ok := NormalizeColor("red; background:url(x)"); ok {
		t.Fatal("bad color accepted")
	}
}

func TestSlugAndClean(t *testing.T) {
	for in, want := range map[string]string{"www.tidewell.com": "tidewell", "shop.larkspur.co.uk": "shop-larkspur", "localhost": "localhost", "x_y.io": "xy", "": "brand"} {
		if got := SlugFor(in); got != want {
			t.Errorf("SlugFor(%q) = %q want %q", in, got, want)
		}
	}
	if got := Clean("  A\\B\x00\x1b[31m  C\nD \u2028", 0); got != "AB[31m C D" {
		t.Errorf("clean %q", got)
	}
	if got := Clean(strings.Repeat("x", 100), 10); got != strings.Repeat("x", 10) {
		t.Errorf("cap %q", got)
	}
}

func TestFetchStoreAndEdit(t *testing.T) {
	logo := pngBytes(t, color.RGBA{0x0f, 0x62, 0xfe, 255}, 96, 48)
	icon := pngBytes(t, color.RGBA{0x10, 0xb9, 0x81, 255}, 32, 32)
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html")
			io.WriteString(w, `<title>Larkspur</title><meta name="description" content="Freight on time">
<script type="application/ld+json">{"@type":"Organization","name":"Larkspur","logo":"/evil.svg"}</script>
<img class="logo" src="/logo.png"><link rel="apple-touch-icon" href="/icon.png">`)
		case "/evil.svg":
			w.Header().Set("Content-Type", "image/svg+xml")
			io.WriteString(w, `<svg onload="alert(1)"/>`)
		case "/logo.png":
			w.Header().Set("Content-Type", "image/png")
			w.Write(logo)
		case "/icon.png":
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Write(icon)
		case "/hero.png":
			w.Write(logo)
		case "/notimage":
			w.Header().Set("Content-Type", "text/html")
			io.WriteString(w, "<html></html>")
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	ctx := context.Background()
	st := Open(t.TempDir())
	client := webfetch.New(webfetch.Options{AllowPrivate: true})

	if _, err := st.Fetch(ctx, webfetch.New(webfetch.Options{}), FetchRequest{URL: srv.URL}, "test"); err == nil {
		t.Fatal("fetched a loopback site without allowPrivate")
	}
	k, err := st.Fetch(ctx, client, FetchRequest{URL: srv.URL, Slug: "larkspur"}, "test")
	if err != nil {
		t.Fatal(err)
	}
	if k.Name != "Larkspur" || k.Tagline != "Freight on time" {
		t.Fatalf("kit text: %+v", k)
	}
	if a := k.Assets["logo"]; a.Type != "image/png" || a.Width != 96 || !strings.HasSuffix(a.Source, "/logo.png") {
		t.Fatalf("logo should skip the unsafe svg and use the png: %+v notes %v", a, k.Notes)
	}
	if k.Assets["icon"].Type != "image/png" {
		t.Fatalf("icon: %+v", k.Assets["icon"])
	}
	if !strings.HasPrefix(k.Primary, "#") || k.Accent == "" {
		t.Fatalf("colors from logo: %q %q", k.Primary, k.Accent)
	}
	if !strings.Contains(strings.Join(k.Notes, ";"), "event handler") {
		t.Fatalf("notes should record the refused svg: %v", k.Notes)
	}
	if _, err := st.Fetch(ctx, client, FetchRequest{URL: srv.URL, Slug: "larkspur"}, "test"); !errors.Is(err, ErrExists) {
		t.Fatalf("second fetch without replace: %v", err)
	}
	h1 := k.Hash()
	k, err = st.AddAsset(ctx, client, "larkspur", "hero", srv.URL+"/hero.png", "test")
	if err != nil || k.Assets["hero"].Size == 0 {
		t.Fatalf("add asset: %v", err)
	}
	if _, err := st.AddAsset(ctx, client, "larkspur", "page", srv.URL+"/notimage", "test"); err == nil {
		t.Fatal("html stored as an asset")
	}
	if _, err := st.AddAsset(ctx, client, "larkspur", "../x", srv.URL+"/hero.png", "test"); err == nil {
		t.Fatal("bad asset name accepted")
	}
	name, primary := "Larkspur Freight", "#123"
	k, err = st.Update("larkspur", Edit{Name: &name, Primary: &primary}, "test")
	if err != nil || k.Name != "Larkspur Freight" || k.Primary != "#112233" || k.Hash() == h1 {
		t.Fatalf("update: %v %+v", err, k)
	}
	bad := "url(x)"
	if _, err := st.Update("larkspur", Edit{Accent: &bad}, "test"); err == nil {
		t.Fatal("bad color accepted")
	}
	a, b, err := st.Asset("larkspur", "logo")
	if err != nil || a.Type != "image/png" || !bytes.Equal(b, logo) {
		t.Fatalf("asset read: %v", err)
	}
	list, _ := st.List()
	if len(list) != 1 {
		t.Fatalf("list %d", len(list))
	}
	if _, err := st.RemoveAsset("larkspur", "hero"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(st.dir, "larkspur", "hero.png")); err == nil {
		t.Fatal("asset file left behind")
	}
	if err := st.Delete("larkspur"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Get("larkspur"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}
	if _, err := st.Get("../etc"); err == nil {
		t.Fatal("path escape accepted")
	}
}

// Namespace prefixes, foreign namespaces and tricks the string checks
// alone missed: elements are matched by namespace URI and local name.
func TestCheckSVGTokenWalk(t *testing.T) {
	bad := map[string]string{
		"prefixed script":    `<svg xmlns="http://www.w3.org/2000/svg" xmlns:s="http://www.w3.org/2000/svg"><s:script>alert(document.domain)</s:script></svg>`,
		"xhtml script":       `<svg xmlns="http://www.w3.org/2000/svg" xmlns:h="http://www.w3.org/1999/xhtml"><h:script>alert(1)</h:script></svg>`,
		"animateMotion":      `<svg xmlns="http://www.w3.org/2000/svg"><a><animateMotion/></a></svg>`,
		"set tab":            "<svg xmlns=\"http://www.w3.org/2000/svg\"><a id=\"x\"><text>x</text></a><set\tattributeName=\"href\" to=\"javascript&#58;alert(1)\" href=\"#x\"/></svg>",
		"anchor":             `<svg xmlns="http://www.w3.org/2000/svg"><a href="#x"><text>x</text></a></svg>`,
		"xhtml div":          `<svg xmlns="http://www.w3.org/2000/svg"><foo:div xmlns:foo="http://www.w3.org/1999/xhtml">x</foo:div></svg>`,
		"mathml":             `<svg xmlns="http://www.w3.org/2000/svg"><m:math xmlns:m="http://www.w3.org/1998/Math/MathML"/></svg>`,
		"unknown namespace":  `<svg xmlns="http://www.w3.org/2000/svg"><x:thing xmlns:x="urn:example"/></svg>`,
		"undeclared prefix":  `<svg xmlns="http://www.w3.org/2000/svg"><h:iframe/></svg>`,
		"html root":          `<html xmlns="http://www.w3.org/1999/xhtml"><svg xmlns="http://www.w3.org/2000/svg"/></html>`,
		"svg root elsewhere": `<s:svg xmlns:s="urn:not-svg"/>`,
		"xml:base":           `<svg xmlns="http://www.w3.org/2000/svg" xml:base="https://x.example/"><use href="#a"/></svg>`,
		"src attribute":      `<svg xmlns="http://www.w3.org/2000/svg"><image src="https://x.example/p.png"/></svg>`,
		"namespaced href":    `<svg xmlns="http://www.w3.org/2000/svg" xmlns:q="urn:q"><use q:href="https://x.example/s.svg#a"/></svg>`,
		"empty href":         `<svg xmlns="http://www.w3.org/2000/svg"><image href=""/></svg>`,
		"encoded js href":    `<svg xmlns="http://www.w3.org/2000/svg"><use href="java&#x09;script&#58;alert(1)"/></svg>`,
		"uppercase on":       `<svg xmlns="http://www.w3.org/2000/svg"><rect ONCLICK="x()"/></svg>`,
		"fill url":           `<svg xmlns="http://www.w3.org/2000/svg"><rect fill="url( 'https://x.example/a.svg#g' )"/></svg>`,
		"css escape":         `<svg xmlns="http://www.w3.org/2000/svg"><rect style="fill:u\72l(https://x.example/)"/></svg>`,
		"style import":       `<svg xmlns="http://www.w3.org/2000/svg"><style><![CDATA[@im` + `port "x.css";]]></style></svg>`,
		"style expression":   `<svg xmlns="http://www.w3.org/2000/svg"><style>rect{width:expression(alert(1))}</style></svg>`,
		"entity":             `<!DOCTYPE svg [<!ENTITY ns "http://www.w3.org/2000/svg">]><svg xmlns="&ns;"/>`,
		"internal subset":    `<!DOCTYPE svg [<!ATTLIST svg onload CDATA "x">]><svg xmlns="http://www.w3.org/2000/svg"/>`,
		"stylesheet pi":      `<?xml-stylesheet href="https://x.example/a.css"?><svg xmlns="http://www.w3.org/2000/svg"/>`,
		"two roots":          `<svg xmlns="http://www.w3.org/2000/svg"/><svg xmlns="http://www.w3.org/2000/svg"><s:script xmlns:s="http://www.w3.org/2000/svg"/></svg>`,
		"not well formed":    `<svg xmlns="http://www.w3.org/2000/svg"><g></svg>`,
	}
	for name, svg := range bad {
		if typ, _, err := ImageType([]byte(svg)); err == nil {
			t.Errorf("%s: unsafe svg accepted as %s: %s", name, typ, svg)
		}
	}
}

// Real exports keep passing: Inkscape with sodipodi, inkscape, rdf, cc and
// dc metadata, Illustrator with a style block and an inline PNG, and a
// hand-written site logo using gradients, filters, symbol, use and switch.
func TestCheckSVGRealExports(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "svg", "*.svg"))
	if err != nil || len(files) < 3 {
		t.Fatalf("fixtures: %v %v", files, err)
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if typ, _, err := ImageType(b); err != nil || typ != "image/svg+xml" {
			t.Errorf("%s refused: %s %v", f, typ, err)
		}
	}
}

// A 16000x16000 PNG compresses to well under MaxAssetBytes; colour
// extraction must read the header and stop, not inflate 256 MB of pixels.
func TestDominantColorsSkipsHugeImage(t *testing.T) {
	const w, h = 16000, 16000
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestCompression}
	if err := enc.Encode(&buf, image.NewGray(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	b := buf.Bytes()
	if _, _, err := ImageType(b); err != nil {
		t.Fatalf("the bomb must still be a valid asset (%d bytes): %v", len(b), err)
	}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	if got := DominantColors(b, 2); got != nil {
		t.Fatalf("colors from a huge image: %v", got)
	}
	runtime.ReadMemStats(&after)
	if n := after.TotalAlloc - before.TotalAlloc; n > 16<<20 {
		t.Fatalf("DominantColors allocated %d MiB", n>>20)
	}
	wide := image.NewRGBA(image.Rect(0, 0, MaxColorSide+1, 1))
	buf.Reset()
	png.Encode(&buf, wide)
	if got := DominantColors(buf.Bytes(), 2); got != nil {
		t.Fatalf("colors from a too wide image: %v", got)
	}
}
