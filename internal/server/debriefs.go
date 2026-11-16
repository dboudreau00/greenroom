package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/dboudreau00/greenroom/internal/demos"
	"github.com/dboudreau00/greenroom/internal/engine"
	"github.com/dboudreau00/greenroom/internal/gaps"
)

func gapStatus(err error) int {
	switch {
	case errors.Is(err, gaps.ErrNotFound), errors.Is(err, demos.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, gaps.ErrState), errors.Is(err, demos.ErrState):
		return http.StatusConflict
	}
	return http.StatusBadRequest
}

func (s *Server) debriefRoutes() {
	m := s.mux
	m.HandleFunc("GET /api/v1/debriefs", s.auth(s.debriefList))
	m.HandleFunc("GET /api/v1/demos/{id}/debrief", s.auth(s.debriefGet))
	m.HandleFunc("PUT /api/v1/demos/{id}/debrief", s.auth(s.debriefPut))
	m.HandleFunc("GET /api/v1/gaps", s.auth(s.gapList))
	m.HandleFunc("POST /api/v1/gaps", s.auth(s.gapCreate))
	m.HandleFunc("GET /api/v1/gaps/suggest", s.auth(s.gapSuggest))
	m.HandleFunc("GET /api/v1/gaps/export", s.auth(s.gapExport))
	m.HandleFunc("GET /api/v1/gaps/{id}", s.auth(s.gapGet))
	m.HandleFunc("PATCH /api/v1/gaps/{id}", s.auth(s.gapEdit))
	m.HandleFunc("POST /api/v1/gaps/{id}/merge", s.auth(s.gapMerge))
}

func (s *Server) debriefList(w http.ResponseWriter, r *http.Request) {
	since, err := engine.ParseSince(r.URL.Query().Get("since"), s.eng.Now())
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	out, err := s.eng.Debriefs(since)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) debriefGet(w http.ResponseWriter, r *http.Request) {
	v, err := s.eng.GetDebrief(r.PathValue("id"))
	if err != nil {
		jsonError(w, gapStatus(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) debriefPut(w http.ResponseWriter, r *http.Request) {
	var req engine.DebriefRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256*1024)).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "bad json: "+err.Error())
		return
	}
	d, err := s.eng.SaveDebrief(r.PathValue("id"), req, actorOf(r))
	if err != nil {
		jsonError(w, gapStatus(err), err.Error())
		return
	}
	s.log.Info("debrief saved", "demo", d.ID, "gaps", len(d.Debrief.Gaps), "actor", actorOf(r))
	v, err := s.eng.GetDebrief(d.ID)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) gapList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	rows, err := s.eng.GapBoard(engine.GapFilter{Status: q.Get("status"), Kind: q.Get("kind"), Env: q.Get("env"), IncludeMerged: boolParam(r, "includeMerged", false)})
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (s *Server) gapCreate(w http.ResponseWriter, r *http.Request) {
	var req engine.GapRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32*1024)).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "bad json: "+err.Error())
		return
	}
	g, err := s.eng.CreateGap(req, actorOf(r))
	if err != nil {
		jsonError(w, gapStatus(err), err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, g)
}

func (s *Server) gapGet(w http.ResponseWriter, r *http.Request) {
	v, err := s.eng.GapDetail(r.PathValue("id"))
	if err != nil {
		jsonError(w, gapStatus(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) gapEdit(w http.ResponseWriter, r *http.Request) {
	var ed engine.GapEdit
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32*1024)).Decode(&ed); err != nil {
		jsonError(w, http.StatusBadRequest, "bad json: "+err.Error())
		return
	}
	g, err := s.eng.EditGap(r.PathValue("id"), ed, actorOf(r))
	if err != nil {
		jsonError(w, gapStatus(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, g)
}

func (s *Server) gapMerge(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Into string `json:"into"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4*1024)).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "bad json: "+err.Error())
		return
	}
	g, n, err := s.eng.MergeGap(r.PathValue("id"), body.Into, actorOf(r))
	if err != nil {
		jsonError(w, gapStatus(err), err.Error())
		return
	}
	s.log.Info("gap merged", "gap", g.ID, "into", g.MergedInto, "demos", n, "actor", actorOf(r))
	writeJSON(w, http.StatusOK, map[string]any{"gap": g, "demos": n})
}

func (s *Server) gapSuggest(w http.ResponseWriter, r *http.Request) {
	list, err := s.eng.SuggestGaps(r.URL.Query().Get("q"))
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) gapExport(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	body, ctype, name, err := s.eng.ExportGaps(q.Get("format"), q.Get("status"))
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	h := w.Header()
	h.Set("Content-Type", ctype)
	h.Set("Content-Disposition", `attachment; filename="`+name+`"`)
	h.Set("Cache-Control", "no-store")
	w.Write(body)
}
