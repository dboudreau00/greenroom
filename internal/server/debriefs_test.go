package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestDebriefAndGapAPI(t *testing.T) {
	srv, _ := newTestServer(t, "secret")
	base := srv.URL + "/api/v1"
	resp, body := bearer(t, http.MethodPost, base+"/demos", `{"booked":false,"env":"demo","title":"Larkspur discovery","prospect":"Larkspur"}`)
	if resp.StatusCode != 201 {
		t.Fatalf("log: %d %s", resp.StatusCode, body)
	}
	done := decodeDemo(t, body)
	start := time.Now().UTC().Add(3 * time.Hour).Truncate(time.Minute)
	_, body = bearer(t, http.MethodPost, base+"/demos", `{"env":"demo","title":"Later","start":"`+start.Format(time.RFC3339)+`","duration":"30m"}`)
	later := decodeDemo(t, body)

	if resp, body := bearer(t, http.MethodGet, base+"/overview", ""); !strings.Contains(body, `"debriefsPending": 1`) {
		t.Fatalf("overview pending: %d %s", resp.StatusCode, body)
	}
	if resp, body := bearer(t, http.MethodGet, base+"/demos/"+done.ID+"/debrief", ""); resp.StatusCode != 200 || !strings.Contains(body, `"debrief": null`) {
		t.Fatalf("empty debrief: %d %s", resp.StatusCode, body)
	}
	put := `{"rating":4,"highlights":"Billing landed","outcome":"positive","competitors":["Quillmark"],
		"gaps":[{"new":{"title":"Scheduled exports","kind":"feature"},"severity":"high","detail":"CSV every Monday","quote":"We do it by hand"}]}`
	resp, body = bearer(t, http.MethodPut, base+"/demos/"+done.ID+"/debrief", put)
	if resp.StatusCode != 200 || !strings.Contains(body, `"title": "Scheduled exports"`) || !strings.Contains(body, `"submittedBy": "api"`) {
		t.Fatalf("put: %d %s", resp.StatusCode, body)
	}
	var view struct {
		Debrief struct {
			Gaps []struct {
				GapID string `json:"gapId"`
			} `json:"gaps"`
		} `json:"debrief"`
	}
	json.Unmarshal([]byte(body), &view)
	gid := view.Debrief.Gaps[0].GapID
	if resp, body := bearer(t, http.MethodGet, base+"/demos/"+done.ID, ""); !strings.Contains(body, "debrief submitted by api") || !strings.Contains(body, `"outcome": "positive"`) {
		t.Fatalf("demo after debrief: %d %s", resp.StatusCode, body)
	}
	for _, c := range []struct {
		url, body string
		want      int
	}{
		{base + "/demos/" + done.ID + "/debrief", `{"rating":4,"gaps":[{"gapId":"gap-deadbeef","severity":"low"}]}`, 400},
		{base + "/demos/" + done.ID + "/debrief", `{"rating":9}`, 400},
		{base + "/demos/" + done.ID + "/debrief", `{"rating":`, 400},
		{base + "/demos/" + done.ID + "/debrief", `{"rating":3,"gaps":[{"new":{"title":"x","kind":"environment","env":"nope"},"severity":"low"}]}`, 400},
		{base + "/demos/" + later.ID + "/debrief", `{"rating":4}`, 409},
		{base + "/demos/20261120-1500-0000/debrief", `{"rating":4}`, 404},
		{base + "/demos/..%2f..%2fx/debrief", `{"rating":4}`, 404},
	} {
		if resp, body := bearer(t, http.MethodPut, c.url, c.body); resp.StatusCode != c.want {
			t.Fatalf("put %s %s: %d %s", c.url, c.body, resp.StatusCode, body)
		}
	}

	resp, body = bearer(t, http.MethodGet, base+"/debriefs", "")
	if resp.StatusCode != 200 || !strings.Contains(body, `"pending": []`) || !strings.Contains(body, `"averageRating": 4`) || !strings.Contains(body, `"name": "Quillmark"`) {
		t.Fatalf("debriefs: %d %s", resp.StatusCode, body)
	}
	if resp, _ := bearer(t, http.MethodGet, base+"/debriefs?since=yesterday", ""); resp.StatusCode != 400 {
		t.Fatalf("bad since: %d", resp.StatusCode)
	}

	resp, body = bearer(t, http.MethodPost, base+"/gaps", `{"title":"Export orders on a schedule","kind":"feature"}`)
	if resp.StatusCode != 201 {
		t.Fatalf("create gap: %d %s", resp.StatusCode, body)
	}
	var other struct {
		ID string `json:"id"`
	}
	json.Unmarshal([]byte(body), &other)
	if resp, _ := bearer(t, http.MethodPost, base+"/gaps", `{"title":"x","kind":"wish"}`); resp.StatusCode != 400 {
		t.Fatalf("bad gap: %d", resp.StatusCode)
	}
	resp, body = bearer(t, http.MethodGet, base+"/gaps", "")
	if resp.StatusCode != 200 || strings.Index(body, gid) > strings.Index(body, other.ID) || !strings.Contains(body, `"score": 3`) {
		t.Fatalf("board: %d %s", resp.StatusCode, body)
	}
	for _, q := range []string{"?status=closed", "?kind=wish", "?env=..%2fx"} {
		if resp, _ := bearer(t, http.MethodGet, base+"/gaps"+q, ""); resp.StatusCode != 400 {
			t.Fatalf("board %s: %d", q, resp.StatusCode)
		}
	}
	if resp, body := bearer(t, http.MethodGet, base+"/gaps?status=active&kind=feature&env=demo", ""); resp.StatusCode != 200 || !strings.Contains(body, gid) || strings.Contains(body, other.ID) {
		t.Fatalf("board filters: %d %s", resp.StatusCode, body)
	}
	if resp, body := bearer(t, http.MethodGet, base+"/gaps/suggest?q=scheduled+exports+please", ""); resp.StatusCode != 200 || !strings.Contains(body, gid) {
		t.Fatalf("suggest: %d %s", resp.StatusCode, body)
	}
	if resp, body := bearer(t, http.MethodGet, base+"/gaps/suggest?q=", ""); resp.StatusCode != 200 || strings.TrimSpace(body) != "[]" {
		t.Fatalf("empty suggest: %d %s", resp.StatusCode, body)
	}
	if resp, body := bearer(t, http.MethodGet, base+"/gaps/"+gid, ""); resp.StatusCode != 200 || !strings.Contains(body, `"mentions": [`) || !strings.Contains(body, "We do it by hand") {
		t.Fatalf("gap detail: %d %s", resp.StatusCode, body)
	}
	for _, id := range []string{"gap-deadbeef", "..%2f..%2fx", "nope"} {
		if resp, _ := bearer(t, http.MethodGet, base+"/gaps/"+id, ""); resp.StatusCode != 404 {
			t.Fatalf("gap %s: %d", id, resp.StatusCode)
		}
	}
	if resp, body := bearer(t, http.MethodPatch, base+"/gaps/"+gid, `{"status":"planned","owner":"Data team"}`); resp.StatusCode != 200 || !strings.Contains(body, `"status": "planned"`) {
		t.Fatalf("patch: %d %s", resp.StatusCode, body)
	}
	if resp, _ := bearer(t, http.MethodPatch, base+"/gaps/"+gid, `{"status":"closed"}`); resp.StatusCode != 400 {
		t.Fatalf("bad patch: %d", resp.StatusCode)
	}

	resp, body = bearer(t, http.MethodGet, base+"/gaps/export?format=md", "")
	day := time.Now().UTC().Format("2006-01-02")
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "text/markdown; charset=utf-8" ||
		resp.Header.Get("Content-Disposition") != `attachment; filename="greenroom-gaps-`+day+`.md"` || !strings.HasPrefix(body, "# Project gaps\n") {
		t.Fatalf("md export: %d %v %s", resp.StatusCode, resp.Header, body)
	}
	resp, body = bearer(t, http.MethodGet, base+"/gaps/export?format=csv&status=planned", "")
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "text/csv; charset=utf-8" ||
		resp.Header.Get("Content-Disposition") != `attachment; filename="greenroom-gaps-`+day+`.csv"` || strings.Count(body, "\r\n") != 2 {
		t.Fatalf("csv export: %d %v %q", resp.StatusCode, resp.Header, body)
	}
	if resp, _ := bearer(t, http.MethodGet, base+"/gaps/export?format=xls", ""); resp.StatusCode != 400 {
		t.Fatalf("bad format: %d", resp.StatusCode)
	}

	resp, body = bearer(t, http.MethodPost, base+"/gaps/"+other.ID+"/merge", `{"into":"`+gid+`"}`)
	if resp.StatusCode != 200 || !strings.Contains(body, `"mergedInto": "`+gid+`"`) || !strings.Contains(body, `"demos": 0`) {
		t.Fatalf("merge: %d %s", resp.StatusCode, body)
	}
	for _, c := range []struct {
		id, into string
		want     int
	}{{other.ID, gid, 409}, {gid, other.ID, 409}, {gid, "gap-deadbeef", 400}, {"gap-deadbeef", gid, 404}, {gid, gid, 400}} {
		if resp, body := bearer(t, http.MethodPost, base+"/gaps/"+c.id+"/merge", `{"into":"`+c.into+`"}`); resp.StatusCode != c.want {
			t.Fatalf("merge %s into %s: %d %s", c.id, c.into, resp.StatusCode, body)
		}
	}
	if resp, _ := bearer(t, http.MethodPatch, base+"/gaps/"+other.ID, `{"owner":"x"}`); resp.StatusCode != 409 {
		t.Fatalf("patch merged: %d", resp.StatusCode)
	}

	for _, p := range []string{"/debriefs", "/gaps", "/gaps/" + gid, "/gaps/export", "/gaps/suggest?q=x", "/demos/" + done.ID + "/debrief"} {
		req, _ := http.NewRequest(http.MethodGet, base+p, nil)
		if resp, _ := do(t, req); resp.StatusCode != 401 {
			t.Fatalf("%s without auth: %d", p, resp.StatusCode)
		}
	}
	req, _ := http.NewRequest(http.MethodPut, base+"/demos/"+done.ID+"/debrief", strings.NewReader(`{"rating":1}`))
	if resp, _ := do(t, req); resp.StatusCode != 401 {
		t.Fatalf("put without auth: %d", resp.StatusCode)
	}
}
