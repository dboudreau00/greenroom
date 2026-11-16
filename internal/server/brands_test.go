package server

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func brandTestSite(t *testing.T) *httptest.Server {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for i := 0; i < 64; i++ {
		img.Set(i%8, i/8, color.RGBA{0x0e, 0x74, 0x90, 255})
	}
	var logo bytes.Buffer
	png.Encode(&logo, img)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html")
			io.WriteString(w, `<title>Tidewell</title><meta name="theme-color" content="#0e7490"><link rel="apple-touch-icon" href="/t.png">`)
		case "/t.png", "/hero.png":
			w.Header().Set("Content-Type", "image/png")
			w.Write(logo.Bytes())
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestBrandFetchBlockedByDefault(t *testing.T) {
	srv, _ := newTestServer(t, "secret")
	site := brandTestSite(t)
	resp, body := bearer(t, http.MethodPost, srv.URL+"/api/v1/brands", `{"url":"`+site.URL+`"}`)
	if resp.StatusCode != 400 || !strings.Contains(body, "not allowed") {
		t.Fatalf("loopback brand fetch must be refused: %d %s", resp.StatusCode, body)
	}
	for _, u := range []string{"file:///etc/passwd", "http://169.254.169.254/latest/meta-data/", "http://[::1]/"} {
		if resp, body := bearer(t, http.MethodPost, srv.URL+"/api/v1/brands", `{"url":"`+u+`"}`); resp.StatusCode != 400 {
			t.Fatalf("%s: %d %s", u, resp.StatusCode, body)
		}
	}
}

func TestBrandAPI(t *testing.T) {
	srv, _ := newTestServerWS(t, "secret", `{"linkFetch":{"allowPrivate":true}}`)
	site := brandTestSite(t)
	resp, body := bearer(t, http.MethodPost, srv.URL+"/api/v1/brands", `{"url":"`+site.URL+`","slug":"tidewell"}`)
	if resp.StatusCode != 201 || !strings.Contains(body, `"primary": "#0e7490"`) || !strings.Contains(body, `"hash"`) {
		t.Fatalf("fetch: %d %s", resp.StatusCode, body)
	}
	if resp, _ := bearer(t, http.MethodPost, srv.URL+"/api/v1/brands", `{"url":"`+site.URL+`","slug":"tidewell"}`); resp.StatusCode != 409 {
		t.Fatalf("duplicate: %d", resp.StatusCode)
	}
	if resp, body := bearer(t, http.MethodPatch, srv.URL+"/api/v1/brands/tidewell", `{"name":"Tidewell Freight","accent":"#123456"}`); resp.StatusCode != 200 || !strings.Contains(body, "Tidewell Freight") {
		t.Fatalf("edit: %d %s", resp.StatusCode, body)
	}
	if resp, _ := bearer(t, http.MethodPatch, srv.URL+"/api/v1/brands/tidewell", `{"primary":"red;}"}`); resp.StatusCode != 400 {
		t.Fatalf("bad color: %d", resp.StatusCode)
	}
	if resp, body := bearer(t, http.MethodPost, srv.URL+"/api/v1/brands/tidewell/assets", `{"name":"hero","url":"`+site.URL+`/hero.png"}`); resp.StatusCode != 201 || !strings.Contains(body, `"hero"`) {
		t.Fatalf("add asset: %d %s", resp.StatusCode, body)
	}
	resp, body = bearer(t, http.MethodGet, srv.URL+"/api/v1/brands/tidewell/assets/logo", "")
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "image/png" || !strings.Contains(resp.Header.Get("Content-Security-Policy"), "sandbox") || !strings.HasPrefix(body, "\x89PNG") {
		t.Fatalf("asset: %d %v", resp.StatusCode, resp.Header)
	}
	if resp, _ := bearer(t, http.MethodGet, srv.URL+"/api/v1/brands/tidewell/assets/nope", ""); resp.StatusCode != 404 {
		t.Fatalf("missing asset: %d", resp.StatusCode)
	}
	if resp, _ := bearer(t, http.MethodGet, srv.URL+"/api/v1/brands/..%2f..%2fetc", ""); resp.StatusCode == 200 {
		t.Fatal("path escape")
	}
	resp, body = bearer(t, http.MethodPost, srv.URL+"/api/v1/environments/demo/reset", `{"persona":"cfo","version":1,"brand":"tidewell"}`)
	if resp.StatusCode != 202 {
		t.Fatalf("reset with brand: %d %s", resp.StatusCode, body)
	}
	runID := body[strings.Index(body, `"runId": "`)+10:]
	runID = runID[:strings.Index(runID, `"`)]
	for i := 0; i < 100; i++ {
		_, rb := bearer(t, http.MethodGet, srv.URL+"/api/v1/environments/demo/runs/"+runID, "")
		if !strings.Contains(rb, `"status": "running"`) {
			if !strings.Contains(rb, `"brand": "tidewell"`) || !strings.Contains(rb, `"status": "ok"`) {
				t.Fatalf("branded run: %s", rb)
			}
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if resp, body := bearer(t, http.MethodPost, srv.URL+"/api/v1/environments/demo/reset", `{"persona":"cfo","version":1,"brand":"nope"}`); resp.StatusCode != 400 && resp.StatusCode != 409 {
		t.Fatalf("reset unknown brand: %d %s", resp.StatusCode, body)
	}
	if resp, body := bearer(t, http.MethodGet, srv.URL+"/api/v1/brands", ""); resp.StatusCode != 200 || !strings.Contains(body, "tidewell") {
		t.Fatalf("list: %d %s", resp.StatusCode, body)
	}
	if resp, _ := bearer(t, http.MethodDelete, srv.URL+"/api/v1/brands/tidewell/assets/hero", ""); resp.StatusCode != 200 {
		t.Fatalf("remove asset: %d", resp.StatusCode)
	}
	if resp, _ := bearer(t, http.MethodDelete, srv.URL+"/api/v1/brands/tidewell", ""); resp.StatusCode != 200 {
		t.Fatalf("delete: %d", resp.StatusCode)
	}
	if resp, _ := bearer(t, http.MethodGet, srv.URL+"/api/v1/brands/tidewell", ""); resp.StatusCode != 404 {
		t.Fatalf("after delete: %d", resp.StatusCode)
	}
}
