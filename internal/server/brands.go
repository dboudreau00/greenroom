package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/dboudreau00/greenroom/internal/brand"
)

type kitView struct {
	*brand.Kit
	Hash string `json:"hash"`
}

func viewKit(k *brand.Kit) kitView { return kitView{Kit: k, Hash: k.Hash()} }

func brandStatus(err error) int {
	switch {
	case errors.Is(err, brand.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, brand.ErrExists):
		return http.StatusConflict
	}
	return http.StatusBadRequest
}

func (s *Server) brandRoutes() {
	m := s.mux
	m.HandleFunc("GET /api/v1/brands", s.auth(s.brandList))
	m.HandleFunc("POST /api/v1/brands", s.auth(s.brandFetch))
	m.HandleFunc("GET /api/v1/brands/{slug}", s.auth(s.brandGet))
	m.HandleFunc("PATCH /api/v1/brands/{slug}", s.auth(s.brandEdit))
	m.HandleFunc("DELETE /api/v1/brands/{slug}", s.auth(s.brandDelete))
	m.HandleFunc("POST /api/v1/brands/{slug}/assets", s.auth(s.brandAddAsset))
	m.HandleFunc("GET /api/v1/brands/{slug}/assets/{name}", s.auth(s.brandAsset))
	m.HandleFunc("DELETE /api/v1/brands/{slug}/assets/{name}", s.auth(s.brandRemoveAsset))
}

func (s *Server) brandList(w http.ResponseWriter, r *http.Request) {
	list, err := s.eng.Brands().List()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]kitView, 0, len(list))
	for _, k := range list {
		out = append(out, viewKit(k))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) brandFetch(w http.ResponseWriter, r *http.Request) {
	var req brand.FetchRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "bad json: "+err.Error())
		return
	}
	if req.URL == "" {
		jsonError(w, http.StatusBadRequest, "url is required")
		return
	}
	k, err := s.eng.FetchBrand(r.Context(), req, actorOf(r))
	if err != nil {
		jsonError(w, brandStatus(err), err.Error())
		return
	}
	s.log.Info("brand kit fetched", "brand", k.Slug, "domain", k.Domain, "actor", actorOf(r))
	writeJSON(w, http.StatusCreated, viewKit(k))
}

func (s *Server) brandGet(w http.ResponseWriter, r *http.Request) {
	k, err := s.eng.Brands().Get(r.PathValue("slug"))
	if err != nil {
		jsonError(w, brandStatus(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, viewKit(k))
}

func (s *Server) brandEdit(w http.ResponseWriter, r *http.Request) {
	var e brand.Edit
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&e); err != nil {
		jsonError(w, http.StatusBadRequest, "bad json: "+err.Error())
		return
	}
	k, err := s.eng.Brands().Update(r.PathValue("slug"), e, actorOf(r))
	if err != nil {
		jsonError(w, brandStatus(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, viewKit(k))
}

func (s *Server) brandDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.eng.Brands().Delete(r.PathValue("slug")); err != nil {
		jsonError(w, brandStatus(err), err.Error())
		return
	}
	s.log.Info("brand kit deleted", "brand", r.PathValue("slug"), "actor", actorOf(r))
	writeJSON(w, http.StatusOK, map[string]any{"deleted": r.PathValue("slug")})
}

func (s *Server) brandAddAsset(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "bad json: "+err.Error())
		return
	}
	k, err := s.eng.AddBrandAsset(r.Context(), r.PathValue("slug"), body.Name, body.URL, actorOf(r))
	if err != nil {
		jsonError(w, brandStatus(err), err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, viewKit(k))
}

func (s *Server) brandRemoveAsset(w http.ResponseWriter, r *http.Request) {
	k, err := s.eng.Brands().RemoveAsset(r.PathValue("slug"), r.PathValue("name"))
	if err != nil {
		jsonError(w, brandStatus(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, viewKit(k))
}

// brandAsset serves a stored image. Its type was decided from the content
// when it was stored; the response is sandboxed so even an SVG opened on
// its own can not run anything in the portal's origin.
func (s *Server) brandAsset(w http.ResponseWriter, r *http.Request) {
	a, b, err := s.eng.Brands().Asset(r.PathValue("slug"), r.PathValue("name"))
	if err != nil {
		jsonError(w, brandStatus(err), err.Error())
		return
	}
	h := w.Header()
	h.Set("Content-Type", a.Type)
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src data:; sandbox")
	h.Set("Content-Disposition", `inline; filename="`+a.File+`"`)
	h.Set("Cache-Control", "private, no-cache")
	h.Set("ETag", `"`+a.SHA256[:16]+`"`)
	w.Write(b)
}
