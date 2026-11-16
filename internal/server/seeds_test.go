package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSeedDiffAndBumpRoutes(t *testing.T) {
	srv, eng := newTestServer(t, "secret")
	call := func(method, path, body string) (*http.Response, string) {
		t.Helper()
		var req *http.Request
		if body != "" {
			req, _ = http.NewRequest(method, srv.URL+path, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
		} else {
			req, _ = http.NewRequest(method, srv.URL+path, nil)
		}
		req.Header.Set("Authorization", "Bearer secret")
		return do(t, req)
	}

	resp, body := call("GET", "/api/v1/environments/demo/seeds/diff?from=cfo@v1&to=cfo@v1", "")
	if resp.StatusCode != 200 || !strings.Contains(body, `"identical": true`) {
		t.Fatalf("self diff %d %s", resp.StatusCode, body)
	}
	for q, want := range map[string]int{
		"from=cfo@vx&to=cfo@v1": 400,
		"from=cfo@v1":           400,
		"from=cfo@v1&to=cfo@v9": 404,
		"from=nobody&to=cfo@v1": 404,
	} {
		if resp, body := call("GET", "/api/v1/environments/demo/seeds/diff?"+q, ""); resp.StatusCode != want {
			t.Errorf("diff %s: %d %s", q, resp.StatusCode, body)
		}
	}
	if resp, _ := call("GET", "/api/v1/environments/nope/seeds/diff?from=cfo&to=cfo", ""); resp.StatusCode != 404 {
		t.Fatalf("unknown env diff: %d", resp.StatusCode)
	}

	resp, body = call("POST", "/api/v1/environments/demo/seeds/cfo/bump", `{"title":"Larkspur close","pin":true}`)
	if resp.StatusCode != 201 {
		t.Fatalf("bump %d %s", resp.StatusCode, body)
	}
	var res struct {
		ID, From, Dir string
		Pinned        bool
	}
	json.Unmarshal([]byte(body), &res)
	if res.ID != "cfo@v2" || res.From != "cfo@v1" || !res.Pinned || filepath.Base(res.Dir) != "v2" {
		t.Fatalf("bump result %+v", res)
	}
	// The reload shows the new set, pinned.
	resp, body = call("GET", "/api/v1/environments/demo", "")
	var detail struct {
		Pinned string `json:"pinned"`
		Seeds  []struct {
			ID     string `json:"id"`
			Title  string `json:"title"`
			Pinned bool   `json:"pinned"`
		} `json:"seeds"`
	}
	json.Unmarshal([]byte(body), &detail)
	if len(detail.Seeds) != 2 || detail.Seeds[1].ID != "cfo@v2" || !detail.Seeds[1].Pinned || detail.Seeds[1].Title != "Larkspur close" {
		t.Fatalf("detail after bump: %d %s", resp.StatusCode, body)
	}
	v, _ := eng.Env("demo")
	if v.Config.Pin.Version != 2 {
		t.Fatalf("pin %+v", v.Config.Pin)
	}

	resp, body = call("GET", "/api/v1/environments/demo/seeds/diff?from=cfo@v1&to=cfo@v2", "")
	if resp.StatusCode != 200 || !strings.Contains(body, `"identical": false`) || !strings.Contains(body, `"to": "Larkspur close"`) || !strings.Contains(body, `"header": "@@ -1 +1 @@"`) {
		t.Fatalf("diff v1 v2 %d %s", resp.StatusCode, body)
	}

	// A file squatting on the next version name makes the bump a conflict.
	os.WriteFile(filepath.Join(v.Config.Dir, "seeds", "cfo", "v3"), []byte("x"), 0o644)
	if resp, body := call("POST", "/api/v1/environments/demo/seeds/cfo/bump", ""); resp.StatusCode != 409 {
		t.Fatalf("bump conflict %d %s", resp.StatusCode, body)
	}
	if resp, _ := call("POST", "/api/v1/environments/demo/seeds/nobody/bump", "{}"); resp.StatusCode != 404 {
		t.Fatalf("unknown persona: %d", resp.StatusCode)
	}
	if resp, _ := call("POST", "/api/v1/environments/nope/seeds/cfo/bump", "{}"); resp.StatusCode != 404 {
		t.Fatalf("unknown env: %d", resp.StatusCode)
	}
	if resp, _ := call("POST", "/api/v1/environments/demo/seeds/Bad_Name/bump", "{}"); resp.StatusCode != 400 {
		t.Fatalf("bad persona: %d", resp.StatusCode)
	}
	if resp, _ := call("POST", "/api/v1/environments/demo/seeds/cfo/bump", `{"from":9}`); resp.StatusCode != 404 {
		t.Fatalf("unknown from: %d", resp.StatusCode)
	}
}
