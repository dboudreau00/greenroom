package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/dboudreau00/greenroom/internal/config"
	"github.com/dboudreau00/greenroom/internal/demos"
	"github.com/dboudreau00/greenroom/internal/engine"
)

func demoStatus(err error) int {
	switch {
	case errors.Is(err, demos.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, demos.ErrConflict), errors.Is(err, demos.ErrState), errors.Is(err, engine.ErrBusy), errors.Is(err, engine.ErrBooked):
		return http.StatusConflict
	}
	return http.StatusBadRequest
}

func (s *Server) demoRoutes() {
	m := s.mux
	m.HandleFunc("GET /api/v1/demos", s.auth(s.demoList))
	m.HandleFunc("GET /api/v1/demos.ics", s.auth(s.demoCalendar))
	m.HandleFunc("POST /api/v1/demos", s.auth(s.demoCreate))
	m.HandleFunc("GET /api/v1/demos/{id}", s.auth(s.demoGet))
	m.HandleFunc("PATCH /api/v1/demos/{id}", s.auth(s.demoEdit))
	m.HandleFunc("POST /api/v1/demos/{id}/cancel", s.auth(s.demoCancel))
	m.HandleFunc("POST /api/v1/demos/{id}/prepare", s.auth(s.demoPrepare))
	m.HandleFunc("POST /api/v1/demos/{id}/journal", s.auth(s.demoAddEntry))
	m.HandleFunc("DELETE /api/v1/demos/{id}/journal/{entry}", s.auth(s.demoDeleteEntry))
}

func (s *Server) demoList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	list, err := s.eng.ListDemos(engine.DemoFilter{Env: q.Get("env"), Scope: q.Get("scope"), Limit: limit})
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) demoCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		engine.DemoRequest
		// Booked is true unless the demo is logged after the fact.
		Booked *bool `json:"booked"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32*1024)).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "bad json: "+err.Error())
		return
	}
	book := body.Booked == nil || *body.Booked
	var d *demos.Demo
	var err error
	if book {
		d, err = s.eng.BookDemo(body.DemoRequest, actorOf(r))
	} else {
		d, err = s.eng.LogDemo(body.DemoRequest, actorOf(r))
	}
	if err != nil {
		jsonError(w, demoStatus(err), err.Error())
		return
	}
	s.log.Info("demo recorded", "demo", d.ID, "env", d.Env, "booked", d.Booked, "actor", actorOf(r))
	writeJSON(w, http.StatusCreated, d)
}

func (s *Server) demoGet(w http.ResponseWriter, r *http.Request) {
	d, err := s.eng.Demos().Get(r.PathValue("id"))
	if err != nil {
		jsonError(w, demoStatus(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) demoEdit(w http.ResponseWriter, r *http.Request) {
	var ed engine.DemoEdit
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32*1024)).Decode(&ed); err != nil {
		jsonError(w, http.StatusBadRequest, "bad json: "+err.Error())
		return
	}
	d, err := s.eng.EditDemo(r.PathValue("id"), ed, actorOf(r))
	if err != nil {
		jsonError(w, demoStatus(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) demoCancel(w http.ResponseWriter, r *http.Request) {
	d, err := s.eng.CancelDemo(r.PathValue("id"), actorOf(r))
	if err != nil {
		jsonError(w, demoStatus(err), err.Error())
		return
	}
	s.log.Info("demo cancelled", "demo", d.ID, "actor", actorOf(r))
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) demoPrepare(w http.ResponseWriter, r *http.Request) {
	d, err := s.eng.PrepareDemo(r.PathValue("id"), actorOf(r))
	if err != nil {
		jsonError(w, demoStatus(err), err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"runId": d.PrepRunID, "demo": d})
}

func (s *Server) demoAddEntry(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Kind string `json:"kind"`
		Text string `json:"text"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32*1024)).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "bad json: "+err.Error())
		return
	}
	d, err := s.eng.AddDemoEntry(r.PathValue("id"), body.Kind, body.Text, actorOf(r))
	if err != nil {
		jsonError(w, demoStatus(err), err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, d)
}

func (s *Server) demoDeleteEntry(w http.ResponseWriter, r *http.Request) {
	d, err := s.eng.DeleteDemoEntry(r.PathValue("id"), r.PathValue("entry"), actorOf(r))
	if err != nil {
		jsonError(w, demoStatus(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// demoCalendar serves booked, non-cancelled demos as an iCalendar file,
// optionally for one environment.
func (s *Server) demoCalendar(w http.ResponseWriter, r *http.Request) {
	env := r.URL.Query().Get("env")
	if env != "" && !config.ValidName(env) {
		jsonError(w, http.StatusBadRequest, "bad environment name")
		return
	}
	all, err := s.eng.Demos().List()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	list := all[:0]
	for _, d := range all {
		if env == "" || d.Env == env {
			list = append(list, d)
		}
	}
	h := w.Header()
	h.Set("Content-Type", "text/calendar; charset=utf-8")
	h.Set("Content-Disposition", `attachment; filename="greenroom-demos.ics"`)
	h.Set("Cache-Control", "no-store")
	w.Write(demos.ICS(list, s.eng.Now()))
}
