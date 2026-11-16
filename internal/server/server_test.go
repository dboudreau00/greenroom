package server

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dboudreau00/greenroom/internal/config"
	"github.com/dboudreau00/greenroom/internal/engine"
	"github.com/dboudreau00/greenroom/internal/state"
)

func newTestServer(t *testing.T, token string) (*httptest.Server, *engine.Engine) {
	t.Helper()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") }))
	t.Cleanup(api.Close)
	dir := t.TempDir()
	envDir := filepath.Join(dir, "environments", "demo")
	os.MkdirAll(filepath.Join(envDir, "seeds", "cfo", "v1"), 0o755)
	env := config.Environment{Name: "demo", Title: "Demo", Pin: config.Pin{Persona: "cfo", Version: 1},
		Dependencies: []config.Dependency{{Name: "api", Kind: config.KindHTTP, URL: api.URL + "/healthz", Critical: true}}}
	b, _ := json.Marshal(env)
	os.WriteFile(filepath.Join(envDir, config.EnvironmentFile), b, 0o644)
	os.WriteFile(filepath.Join(envDir, "seeds", "cfo", "v1", config.SeedFile), []byte(`{"steps":[{"name":"ping","kind":"http","url":"`+api.URL+`/seed"}]}`), 0o644)
	ws, err := config.LoadWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}
	st, _ := state.Open(ws.StateDir)
	eng := engine.New(ws, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := eng.Reload(); err != nil {
		t.Fatal(err)
	}
	s := New(eng, Options{Token: token, Version: "test"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return srv, eng
}

func do(t *testing.T, req *http.Request) (*http.Response, string) {
	t.Helper()
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, string(b)
}

func TestAuthAndAPI(t *testing.T) {
	srv, _ := newTestServer(t, "secret")
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/healthz", nil)
	if resp, body := do(t, req); resp.StatusCode != 200 || !strings.Contains(body, `"ok": true`) {
		t.Fatalf("healthz %d %s", resp.StatusCode, body)
	}
	req, _ = http.NewRequest(http.MethodGet, srv.URL+"/api/v1/overview", nil)
	if resp, _ := do(t, req); resp.StatusCode != 401 {
		t.Fatalf("overview without auth: %d", resp.StatusCode)
	}
	req, _ = http.NewRequest(http.MethodGet, srv.URL+"/", nil)
	if resp, _ := do(t, req); resp.StatusCode != 302 || resp.Header.Get("Location") != "/login" {
		t.Fatalf("index without auth: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	req, _ = http.NewRequest(http.MethodGet, srv.URL+"/api/v1/overview", nil)
	req.Header.Set("Authorization", "Bearer secret")
	resp, body := do(t, req)
	if resp.StatusCode != 200 || !strings.Contains(body, `"name": "demo"`) {
		t.Fatalf("overview bearer: %d %s", resp.StatusCode, body)
	}
	req, _ = http.NewRequest(http.MethodGet, srv.URL+"/api/v1/overview", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	if resp, _ := do(t, req); resp.StatusCode != 401 {
		t.Fatalf("wrong bearer: %d", resp.StatusCode)
	}
	req, _ = http.NewRequest(http.MethodGet, srv.URL+"/assets/style.css", nil)
	if resp, _ := do(t, req); resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Type"), "text/css") {
		t.Fatalf("assets: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	req, _ = http.NewRequest(http.MethodGet, srv.URL+"/api/v1/environments/nope", nil)
	req.Header.Set("Authorization", "Bearer secret")
	if resp, _ := do(t, req); resp.StatusCode != 404 {
		t.Fatalf("unknown env: %d", resp.StatusCode)
	}
}

func TestLoginSessionAndCSRF(t *testing.T) {
	srv, _ := newTestServer(t, "secret")
	form := url.Values{"token": {"wrong"}}
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if resp, _ := do(t, req); resp.StatusCode != 401 {
		t.Fatalf("wrong token: %d", resp.StatusCode)
	}
	form = url.Values{"token": {"secret"}}
	req, _ = http.NewRequest(http.MethodPost, srv.URL+"/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, _ := do(t, req)
	if resp.StatusCode != 302 || len(resp.Cookies()) == 0 || resp.Cookies()[0].Name != sessionCookie || !resp.Cookies()[0].HttpOnly {
		t.Fatalf("login: %d %v", resp.StatusCode, resp.Cookies())
	}
	cookie := resp.Cookies()[0]
	req, _ = http.NewRequest(http.MethodGet, srv.URL+"/api/v1/me", nil)
	req.AddCookie(cookie)
	resp, body := do(t, req)
	if resp.StatusCode != 200 || !strings.Contains(body, `"actor": "portal"`) {
		t.Fatalf("me: %d %s", resp.StatusCode, body)
	}
	req, _ = http.NewRequest(http.MethodPost, srv.URL+"/api/v1/environments/demo/check", nil)
	req.AddCookie(cookie)
	if resp, _ := do(t, req); resp.StatusCode != 403 {
		t.Fatalf("csrf: %d", resp.StatusCode)
	}
	req, _ = http.NewRequest(http.MethodPost, srv.URL+"/api/v1/environments/demo/check?drift=false", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-Requested-With", "greenroom")
	resp, body = do(t, req)
	if resp.StatusCode != 200 || !strings.Contains(body, `"readiness": "amber"`) {
		t.Fatalf("check: %d %s", resp.StatusCode, body)
	}
	req, _ = http.NewRequest(http.MethodPost, srv.URL+"/logout", nil)
	req.AddCookie(cookie)
	do(t, req)
	req, _ = http.NewRequest(http.MethodGet, srv.URL+"/api/v1/me", nil)
	req.AddCookie(cookie)
	if resp, _ := do(t, req); resp.StatusCode != 401 {
		t.Fatalf("after logout: %d", resp.StatusCode)
	}
	req, _ = http.NewRequest(http.MethodGet, srv.URL+"/login", nil)
	if resp, body := do(t, req); resp.StatusCode != 200 || !strings.Contains(body, "Greenroom") {
		t.Fatalf("login page: %d", resp.StatusCode)
	}
}

func TestResetAndEvents(t *testing.T) {
	srv, _ := newTestServer(t, "secret")
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/environments/demo/reset", strings.NewReader(`{"persona":"cfo","version":1}`))
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("X-Actor", "alice")
	resp, body := do(t, req)
	if resp.StatusCode != 202 {
		t.Fatalf("reset: %d %s", resp.StatusCode, body)
	}
	var out struct {
		RunID string `json:"runId"`
	}
	json.Unmarshal([]byte(body), &out)
	if out.RunID == "" {
		t.Fatal("no run id")
	}
	req, _ = http.NewRequest(http.MethodGet, srv.URL+"/api/v1/environments/demo/runs/"+out.RunID+"/events", nil)
	req.Header.Set("Authorization", "Bearer secret")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	req = req.WithContext(ctx)
	er, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer er.Body.Close()
	if er.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("content type %q", er.Header.Get("Content-Type"))
	}
	var events []string
	sc := bufio.NewScanner(er.Body)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "event: ") {
			events = append(events, strings.TrimPrefix(line, "event: "))
		}
		if line == "event: done" {
			break
		}
	}
	joined := strings.Join(events, ",")
	if !strings.HasPrefix(joined, "snapshot") || !strings.HasSuffix(joined, "done") {
		t.Fatalf("events %s", joined)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		req, _ = http.NewRequest(http.MethodGet, srv.URL+"/api/v1/environments/demo/runs/"+out.RunID, nil)
		req.Header.Set("Authorization", "Bearer secret")
		resp, body = do(t, req)
		if strings.Contains(body, `"status": "ok"`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("run did not finish ok: %s", body)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !strings.Contains(body, `"actor": "api:alice"`) {
		t.Fatalf("actor not recorded: %s", body)
	}
	req, _ = http.NewRequest(http.MethodGet, srv.URL+"/api/v1/environments/demo/runs", nil)
	req.Header.Set("Authorization", "Bearer secret")
	if resp, body := do(t, req); resp.StatusCode != 200 || !strings.Contains(body, out.RunID) {
		t.Fatalf("runs list: %d %s", resp.StatusCode, body)
	}
	req, _ = http.NewRequest(http.MethodGet, srv.URL+"/api/v1/environments/demo", nil)
	req.Header.Set("Authorization", "Bearer secret")
	if resp, body := do(t, req); resp.StatusCode != 200 || !strings.Contains(body, `"readiness": "green"`) {
		t.Fatalf("detail after reset: %d %s", resp.StatusCode, body)
	}
	req, _ = http.NewRequest(http.MethodGet, srv.URL+"/api/v1/environments/demo/runs/nope/events", nil)
	req.Header.Set("Authorization", "Bearer secret")
	if resp, _ := do(t, req); resp.StatusCode != 404 {
		t.Fatalf("missing run events: %d", resp.StatusCode)
	}
}

func TestOpenAccessAndListenGuard(t *testing.T) {
	srv, eng := newTestServer(t, "")
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/overview", nil)
	if resp, _ := do(t, req); resp.StatusCode != 200 {
		t.Fatalf("open access: %d", resp.StatusCode)
	}
	s := New(eng, Options{}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.ListenAndServe(ctx, "0.0.0.0:0"); err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("expected refusal without token: %v", err)
	}
	if !isLoopback("127.0.0.1:8470") || !isLoopback("[::1]:1") || isLoopback("0.0.0.0:1") || isLoopback(":8470") {
		t.Fatal("isLoopback")
	}
}

func bearer(t *testing.T, method, url, body string) (*http.Response, string) {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, _ := http.NewRequest(method, url, rd)
	req.Header.Set("Authorization", "Bearer secret")
	return do(t, req)
}

func TestTemplatesAndCreate(t *testing.T) {
	srv, eng := newTestServer(t, "secret")
	resp, body := bearer(t, http.MethodGet, srv.URL+"/api/v1/templates", "")
	if resp.StatusCode != 200 || !strings.Contains(body, `"name": "saas-web"`) || !strings.Contains(body, `"productType": "SaaS web app"`) {
		t.Fatalf("templates: %d %s", resp.StatusCode, body)
	}
	resp, body = bearer(t, http.MethodPost, srv.URL+"/api/v1/environments", `{"template":"saas-web","name":"preview","modules":["support"],"dryRun":true}`)
	if resp.StatusCode != 200 || !strings.Contains(body, `"accounts"`) || !strings.Contains(body, "seeds/support-lead/v1/seed.json") {
		t.Fatalf("dry run: %d %s", resp.StatusCode, body)
	}
	if _, err := eng.Env("preview"); err == nil {
		t.Fatal("dry run created the environment")
	}
	resp, body = bearer(t, http.MethodPost, srv.URL+"/api/v1/environments", `{"template":"saas-web","name":"sales-demo","title":"Sales demo","modules":["billing"],"persona":"cfo"}`)
	if resp.StatusCode != 201 {
		t.Fatalf("create: %d %s", resp.StatusCode, body)
	}
	resp, body = bearer(t, http.MethodGet, srv.URL+"/api/v1/environments/sales-demo", "")
	if resp.StatusCode != 200 || !strings.Contains(body, `"pinned": "cfo@v1"`) || !strings.Contains(body, `"productType": "SaaS web app"`) {
		t.Fatalf("created env: %d %s", resp.StatusCode, body)
	}
	if resp, body := bearer(t, http.MethodPost, srv.URL+"/api/v1/environments", `{"template":"saas-web","name":"sales-demo"}`); resp.StatusCode != 409 {
		t.Fatalf("duplicate: %d %s", resp.StatusCode, body)
	}
	if resp, body := bearer(t, http.MethodPost, srv.URL+"/api/v1/environments", `{"template":"saas-web","name":"demo"}`); resp.StatusCode != 409 {
		t.Fatalf("existing env name: %d %s", resp.StatusCode, body)
	}
	for _, bad := range []string{
		`{"template":"nope","name":"x"}`,
		`{"template":"saas-web","name":"../x"}`,
		`{"template":"saas-web","name":"x","params":{"webPort":"$(id)"}}`,
		`{"template":"saas-web","name":"x","title":"<img src=x>"}`,
	} {
		if resp, body := bearer(t, http.MethodPost, srv.URL+"/api/v1/environments", bad); resp.StatusCode != 400 {
			t.Fatalf("%s: %d %s", bad, resp.StatusCode, body)
		}
	}
}
