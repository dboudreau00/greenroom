package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/dboudreau00/greenroom/internal/engine"
	"github.com/dboudreau00/greenroom/internal/seeds"
)

func (s *Server) seedRoutes() {
	m := s.mux
	m.HandleFunc("GET /api/v1/environments/{env}/seeds/diff", s.auth(s.seedDiff))
	m.HandleFunc("POST /api/v1/environments/{env}/seeds/{persona}/bump", s.auth(s.seedBump))
}

func seedStatus(err error) int {
	switch {
	case errors.Is(err, seeds.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, seeds.ErrExists):
		return http.StatusConflict
	case errors.Is(err, engine.ErrBadRef), errors.Is(err, seeds.ErrInvalid):
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}

func (s *Server) seedDiff(w http.ResponseWriter, r *http.Request) {
	v, err := s.eng.Env(r.PathValue("env"))
	if err != nil {
		jsonError(w, http.StatusNotFound, err.Error())
		return
	}
	q := r.URL.Query()
	d, err := s.eng.DiffSets(v, q.Get("from"), q.Get("to"))
	if err != nil {
		jsonError(w, seedStatus(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) seedBump(w http.ResponseWriter, r *http.Request) {
	if _, err := s.eng.Env(r.PathValue("env")); err != nil {
		jsonError(w, http.StatusNotFound, err.Error())
		return
	}
	var body struct {
		From  int    `json:"from"`
		Title string `json:"title"`
		Pin   bool   `json:"pin"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&body); err != nil {
			jsonError(w, http.StatusBadRequest, "bad json: "+err.Error())
			return
		}
	}
	res, err := s.eng.BumpSet(r.PathValue("env"), engine.BumpRequest{Persona: r.PathValue("persona"), From: body.From, Title: body.Title, Pin: body.Pin, Actor: actorOf(r)})
	if err != nil && res == nil {
		jsonError(w, seedStatus(err), err.Error())
		return
	}
	out := map[string]any{"id": res.ID, "from": res.From, "dir": res.Dir, "pinned": res.Pinned}
	if err != nil {
		out["warning"] = err.Error()
	}
	writeJSON(w, http.StatusCreated, out)
}
