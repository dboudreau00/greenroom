package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/dboudreau00/greenroom/internal/config"
	"github.com/dboudreau00/greenroom/internal/demos"
	"github.com/dboudreau00/greenroom/internal/engine"
)

func decodeDemo(t *testing.T, body string) demos.Demo {
	t.Helper()
	var d demos.Demo
	if err := json.Unmarshal([]byte(body), &d); err != nil {
		t.Fatalf("decode %v: %s", err, body)
	}
	return d
}

func waitRunDone(t *testing.T, base, id string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		_, body := bearer(t, http.MethodGet, base+"/api/v1/environments/demo/runs/"+id, "")
		if !strings.Contains(body, `"status": "running"`) && strings.Contains(body, `"status"`) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("run %s did not finish: %s", id, body)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestDemoAPI(t *testing.T) {
	srv, _ := newTestServer(t, "secret")
	u := srv.URL + "/api/v1/demos"
	start := time.Now().UTC().Add(3 * time.Hour).Truncate(time.Minute)
	book := `{"env":"demo","title":"Tidewell discovery","prospect":"Tidewell","start":"` + start.Format(time.RFC3339) + `","duration":"45m","attendees":["Ana Ruiz"," "]}`
	resp, body := bearer(t, http.MethodPost, u, book)
	if resp.StatusCode != 201 {
		t.Fatalf("book: %d %s", resp.StatusCode, body)
	}
	d := decodeDemo(t, body)
	if d.Status != "scheduled" || d.SetID() != "cfo@v1" || d.Owner != "api" || len(d.Attendees) != 1 || !d.End.Equal(start.Add(45*time.Minute)) {
		t.Fatalf("booked %+v", d)
	}
	if resp, body := bearer(t, http.MethodPost, u, book); resp.StatusCode != 409 || !strings.Contains(body, d.ID) {
		t.Fatalf("overlap: %d %s", resp.StatusCode, body)
	}
	for _, bad := range []string{
		`{"env":"nope","title":"x","start":"` + start.Add(5*time.Hour).Format(time.RFC3339) + `","duration":"30m"}`,
		`{"env":"demo","title":"x","brand":"nobody","start":"` + start.Add(5*time.Hour).Format(time.RFC3339) + `","duration":"30m"}`,
		`{"env":"demo","title":"x","persona":"ghost","start":"` + start.Add(5*time.Hour).Format(time.RFC3339) + `","duration":"30m"}`,
		`{"env":"demo","title":"x","start":"2020-01-01T10:00:00Z","duration":"30m"}`,
		`{"env":"demo","title":"x","start":"not a time"}`,
	} {
		if resp, body := bearer(t, http.MethodPost, u, bad); resp.StatusCode != 400 {
			t.Fatalf("%s: %d %s", bad, resp.StatusCode, body)
		}
	}

	resp, body = bearer(t, http.MethodGet, u+"?scope=upcoming", "")
	if resp.StatusCode != 200 || !strings.Contains(body, d.ID) || !strings.Contains(body, `"journalCount": 1`) {
		t.Fatalf("list: %d %s", resp.StatusCode, body)
	}
	for _, q := range []string{"?scope=soon", "?env=..%2fx"} {
		if resp, _ := bearer(t, http.MethodGet, u+q, ""); resp.StatusCode != 400 {
			t.Fatalf("list %s: %d", q, resp.StatusCode)
		}
	}
	if resp, body := bearer(t, http.MethodGet, u+"/"+d.ID, ""); resp.StatusCode != 200 || !strings.Contains(body, `"journal"`) {
		t.Fatalf("get: %d %s", resp.StatusCode, body)
	}
	for _, id := range []string{"nope", "..%2f..%2fx", "20261120-1500-zzzz"} {
		if resp, _ := bearer(t, http.MethodGet, u+"/"+id, ""); resp.StatusCode != 404 {
			t.Fatalf("get %s: %d", id, resp.StatusCode)
		}
	}

	resp, body = bearer(t, http.MethodPatch, u+"/"+d.ID, `{"outcome":"positive","stage":"poc","nextStep":"Send pricing"}`)
	if resp.StatusCode != 200 || !strings.Contains(body, `"stage": "poc"`) || !strings.Contains(body, "Send pricing") {
		t.Fatalf("patch: %d %s", resp.StatusCode, body)
	}
	if resp, _ := bearer(t, http.MethodPatch, u+"/"+d.ID, `{"outcome":"won"}`); resp.StatusCode != 400 {
		t.Fatalf("bad outcome: %d", resp.StatusCode)
	}

	resp, body = bearer(t, http.MethodPost, u+"/"+d.ID+"/journal", `{"kind":"question","text":"Can it do SSO?\nAnd SCIM?"}`)
	if resp.StatusCode != 201 {
		t.Fatalf("journal: %d %s", resp.StatusCode, body)
	}
	d = decodeDemo(t, body)
	entry := d.Journal[len(d.Journal)-1]
	if entry.Kind != "question" || entry.Text != "Can it do SSO?\nAnd SCIM?" || entry.Author != "api" {
		t.Fatalf("entry %+v", entry)
	}
	if resp, _ := bearer(t, http.MethodPost, u+"/"+d.ID+"/journal", `{"kind":"system","text":"fake"}`); resp.StatusCode != 400 {
		t.Fatalf("system entry from the API: %d", resp.StatusCode)
	}
	if resp, _ := bearer(t, http.MethodDelete, u+"/"+d.ID+"/journal/"+d.Journal[0].ID, ""); resp.StatusCode != 409 {
		t.Fatalf("delete system entry: %d", resp.StatusCode)
	}
	if resp, _ := bearer(t, http.MethodDelete, u+"/"+d.ID+"/journal/0badc0de", ""); resp.StatusCode != 404 {
		t.Fatalf("delete missing entry: %d", resp.StatusCode)
	}
	if resp, body := bearer(t, http.MethodDelete, u+"/"+d.ID+"/journal/"+entry.ID, ""); resp.StatusCode != 200 || strings.Contains(body, "SSO") {
		t.Fatalf("delete entry: %d %s", resp.StatusCode, body)
	}

	resp, body = bearer(t, http.MethodPost, u+"/"+d.ID+"/cancel", "")
	if resp.StatusCode != 200 || !strings.Contains(body, `"status": "cancelled"`) || !strings.Contains(body, "cancelled by api") {
		t.Fatalf("cancel: %d %s", resp.StatusCode, body)
	}
	if resp, _ := bearer(t, http.MethodPost, u+"/"+d.ID+"/cancel", ""); resp.StatusCode != 409 {
		t.Fatalf("cancel twice: %d", resp.StatusCode)
	}
	if resp, _ := bearer(t, http.MethodPost, u+"/"+d.ID+"/prepare", ""); resp.StatusCode != 409 {
		t.Fatalf("prepare cancelled: %d", resp.StatusCode)
	}
	if resp, _ := bearer(t, http.MethodPatch, u+"/"+d.ID, `{"start":"`+start.Add(6*time.Hour).Format(time.RFC3339)+`"}`); resp.StatusCode != 409 {
		t.Fatalf("reschedule cancelled: %d", resp.StatusCode)
	}

	resp, body = bearer(t, http.MethodPost, u, `{"booked":false,"env":"demo","title":"Hallway demo","outcome":"neutral"}`)
	if resp.StatusCode != 201 || !strings.Contains(body, `"status": "done"`) || !strings.Contains(body, `"booked": false`) {
		t.Fatalf("log: %d %s", resp.StatusCode, body)
	}
	resp, body = bearer(t, http.MethodGet, u+"?scope=past&env=demo", "")
	if resp.StatusCode != 200 || !strings.Contains(body, "Hallway demo") || !strings.Contains(body, d.ID) {
		t.Fatalf("past: %d %s", resp.StatusCode, body)
	}

	resp, body = bearer(t, http.MethodPost, u, `{"env":"demo","title":"Early","start":"`+start.Add(8*time.Hour).Format(time.RFC3339)+`","duration":"30m"}`)
	early := decodeDemo(t, body)
	resp, body = bearer(t, http.MethodPost, u+"/"+early.ID+"/prepare", "")
	if resp.StatusCode != 202 || !strings.Contains(body, `"runId"`) || !strings.Contains(body, `"status": "preparing"`) {
		t.Fatalf("prepare: %d %s", resp.StatusCode, body)
	}
	var out struct {
		RunID string `json:"runId"`
	}
	json.Unmarshal([]byte(body), &out)
	waitRunDone(t, srv.URL, out.RunID)

	resp, body = bearer(t, http.MethodGet, srv.URL+"/api/v1/environments/demo", "")
	if resp.StatusCode != 200 || !strings.Contains(body, `"demos": [`) || !strings.Contains(body, `"nextDemo"`) {
		t.Fatalf("detail with demos: %d", resp.StatusCode)
	}
}

func TestResetDuringLiveDemo(t *testing.T) {
	srv, eng := newTestServer(t, "secret")
	now := time.Now().UTC()
	live := &demos.Demo{Title: "Quillmark exec", Env: "demo", Persona: "cfo", Version: 1, Booked: true, Status: demos.Live,
		Start: now.Add(-10 * time.Minute), End: now.Add(40 * time.Minute), Stage: "executive"}
	if err := eng.Demos().Create(live, nil); err != nil {
		t.Fatal(err)
	}
	reset := srv.URL + "/api/v1/environments/demo/reset"
	resp, body := bearer(t, http.MethodPost, reset, `{"persona":"cfo"}`)
	if resp.StatusCode != 409 || !strings.Contains(body, "booked") || !strings.Contains(body, live.ID) {
		t.Fatalf("reset during live demo: %d %s", resp.StatusCode, body)
	}
	if resp, body := bearer(t, http.MethodPost, srv.URL+"/api/v1/environments/demo/snapshot", `{"dependency":"api"}`); resp.StatusCode != 409 {
		t.Fatalf("snapshot during live demo: %d %s", resp.StatusCode, body)
	}
	resp, body = bearer(t, http.MethodPost, reset, `{"persona":"cfo","override":true}`)
	if resp.StatusCode != 202 {
		t.Fatalf("override: %d %s", resp.StatusCode, body)
	}
	var out struct {
		RunID string `json:"runId"`
	}
	json.Unmarshal([]byte(body), &out)
	waitRunDone(t, srv.URL, out.RunID)
	resp, body = bearer(t, http.MethodGet, srv.URL+"/api/v1/demos/"+live.ID, "")
	if !strings.Contains(body, "reset by api during the live demo (override)") || !strings.Contains(body, out.RunID) {
		t.Fatalf("override journal: %s", body)
	}
	resp, body = bearer(t, http.MethodGet, srv.URL+"/api/v1/overview", "")
	if !strings.Contains(body, `"liveDemo"`) || !strings.Contains(body, "Quillmark exec") {
		t.Fatalf("overview live demo: %s", body)
	}
}

func TestDemoCalendar(t *testing.T) {
	srv, eng := newTestServer(t, "secret")
	start := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Minute)
	title := `Pricing; terms, and \ more for the Larkspur regional operations leadership group`
	d, err := eng.BookDemo(engine.DemoRequest{Env: "demo", Title: title, Prospect: "Larkspur, Inc.", Start: start, Duration: config.Duration(45 * time.Minute)}, "api")
	if err != nil {
		t.Fatal(err)
	}
	gone, err := eng.BookDemo(engine.DemoRequest{Env: "demo", Title: "Gone", Start: start.Add(3 * time.Hour), Duration: config.Duration(30 * time.Minute)}, "api")
	if err != nil {
		t.Fatal(err)
	}
	eng.CancelDemo(gone.ID, "api")
	eng.LogDemo(engine.DemoRequest{Env: "demo", Title: "Logged"}, "api")

	resp, body := bearer(t, http.MethodGet, srv.URL+"/api/v1/demos.ics", "")
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/calendar") || !strings.HasPrefix(resp.Header.Get("Content-Disposition"), "attachment") {
		t.Fatalf("ics: %d %v", resp.StatusCode, resp.Header)
	}
	if !strings.HasPrefix(body, "BEGIN:VCALENDAR\r\nVERSION:2.0\r\n") || !strings.HasSuffix(body, "END:VCALENDAR\r\n") {
		t.Fatalf("calendar frame: %q", body)
	}
	if strings.Count(body, "\n") != strings.Count(body, "\r\n") {
		t.Fatal("bare LF in the calendar")
	}
	folded := false
	for _, l := range strings.Split(strings.TrimSuffix(body, "\r\n"), "\r\n") {
		if len(l) > 75 {
			t.Fatalf("line over 75 octets: %q", l)
		}
		if strings.HasPrefix(l, " ") {
			folded = true
		}
	}
	if !folded {
		t.Fatal("long lines were not folded")
	}
	unfolded := strings.ReplaceAll(body, "\r\n ", "")
	for _, want := range []string{
		"UID:" + d.ID + "@greenroom\r\n",
		"DTSTART:" + start.Format("20060102T150405Z") + "\r\n",
		"DTEND:" + start.Add(45*time.Minute).Format("20060102T150405Z") + "\r\n",
		`SUMMARY:Pricing\; terms\, and \\ more for the Larkspur regional operations leadership group` + "\r\n",
		`DESCRIPTION:Environment: demo\nPersona: cfo@v1\nProspect: Larkspur\, Inc.\n`,
		"DTSTAMP:",
	} {
		if !strings.Contains(unfolded, want) {
			t.Fatalf("calendar lacks %q:\n%s", want, unfolded)
		}
	}
	if strings.Contains(unfolded, "Gone") || strings.Contains(unfolded, "Logged") {
		t.Fatalf("cancelled or logged demo in the calendar:\n%s", unfolded)
	}
	if resp, _ := bearer(t, http.MethodGet, srv.URL+"/api/v1/demos.ics?env=..%2fx", ""); resp.StatusCode != 400 {
		t.Fatalf("bad env filter: %d", resp.StatusCode)
	}
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/demos.ics", nil)
	if resp, _ := do(t, req); resp.StatusCode != 401 {
		t.Fatalf("calendar without auth: %d", resp.StatusCode)
	}
}
