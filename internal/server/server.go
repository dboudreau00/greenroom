// Package server is the portal: a JSON API under /api/v1, server-sent
// events for live run logs, token or session authentication, and the
// embedded single-page UI.
package server

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dboudreau00/greenroom/internal/engine"
	"github.com/dboudreau00/greenroom/internal/ui"
)

// Options configure the server.
type Options struct {
	Token    string
	Insecure bool
	Version  string
}

// Server serves the portal and API.
type Server struct {
	eng      *engine.Engine
	opt      Options
	mux      *http.ServeMux
	log      *slog.Logger
	mu       sync.Mutex
	sessions map[string]time.Time
}

type ctxKey int

const actorKey ctxKey = 1

const sessionCookie = "greenroom_session"
const sessionTTL = 12 * time.Hour

// New builds the server.
func New(eng *engine.Engine, opt Options, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{eng: eng, opt: opt, mux: http.NewServeMux(), log: logger, sessions: map[string]time.Time{}}
	s.routes()
	return s
}

// Handler returns the root handler.
func (s *Server) Handler() http.Handler { return s.withHeaders(s.mux) }

func (s *Server) routes() {
	m := s.mux
	m.HandleFunc("GET /healthz", s.healthz)
	m.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServerFS(ui.Files)))
	m.HandleFunc("GET /login", s.loginPage)
	m.HandleFunc("POST /login", s.loginPost)
	m.HandleFunc("POST /logout", s.logout)
	m.HandleFunc("GET /{$}", s.auth(s.index))
	m.HandleFunc("GET /api/v1/me", s.auth(s.me))
	m.HandleFunc("GET /api/v1/overview", s.auth(s.overview))
	m.HandleFunc("POST /api/v1/reload", s.auth(s.reload))
	m.HandleFunc("GET /api/v1/environments/{env}", s.auth(s.environment))
	m.HandleFunc("POST /api/v1/environments/{env}/check", s.auth(s.check))
	m.HandleFunc("GET /api/v1/environments/{env}/stale", s.auth(s.stale))
	m.HandleFunc("POST /api/v1/environments/{env}/reset", s.auth(s.reset))
	m.HandleFunc("POST /api/v1/environments/{env}/snapshot", s.auth(s.snapshot))
	m.HandleFunc("POST /api/v1/environments/{env}/dependencies/{dep}/heal", s.auth(s.heal))
	m.HandleFunc("GET /api/v1/environments/{env}/targets", s.auth(s.targets))
	m.HandleFunc("GET /api/v1/environments/{env}/runs", s.auth(s.runs))
	m.HandleFunc("GET /api/v1/environments/{env}/runs/{id}", s.auth(s.run))
	m.HandleFunc("GET /api/v1/environments/{env}/runs/{id}/events", s.auth(s.runEvents))
}

func (s *Server) withHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'")
		next.ServeHTTP(w, r)
	})
}

// ---- auth ----

func (s *Server) authenticate(r *http.Request) (string, bool) {
	if s.opt.Token == "" {
		return "local", true
	}
	if a := r.Header.Get("Authorization"); strings.HasPrefix(a, "Bearer ") {
		if subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(a, "Bearer ")), []byte(s.opt.Token)) == 1 {
			return "api", true
		}
		return "", false
	}
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	exp, ok := s.sessions[c.Value]
	if !ok || time.Now().After(exp) {
		delete(s.sessions, c.Value)
		return "", false
	}
	return "portal", true
}

func (s *Server) auth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, ok := s.authenticate(r)
		if !ok {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				jsonError(w, http.StatusUnauthorized, "authentication required")
				return
			}
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && actor == "portal" && r.Header.Get("X-Requested-With") != "greenroom" {
			jsonError(w, http.StatusForbidden, "missing X-Requested-With header")
			return
		}
		if a := r.Header.Get("X-Actor"); a != "" && len(a) < 64 {
			actor = actor + ":" + a
		}
		h(w, r.WithContext(context.WithValue(r.Context(), actorKey, actor)))
	}
}

func actorOf(r *http.Request) string {
	if a, ok := r.Context().Value(actorKey).(string); ok {
		return a
	}
	return "unknown"
}

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	if s.opt.Token == "" {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	s.renderLogin(w, "")
}

func (s *Server) renderLogin(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	note := ""
	if msg != "" {
		note = `<p class="err">` + html.EscapeString(msg) + `</p>`
	}
	fmt.Fprintf(w, `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Greenroom sign in</title><link rel="stylesheet" href="/assets/style.css"></head>
<body class="login"><form method="post" action="/login" class="login-card"><h1><span class="dot"></span>Greenroom</h1><p>Enter the portal token to continue.</p>%s<input type="password" name="token" placeholder="token" autofocus autocomplete="current-password"><button type="submit">Sign in</button></form></body></html>`, note)
}

func (s *Server) loginPost(w http.ResponseWriter, r *http.Request) {
	if s.opt.Token == "" {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderLogin(w, "bad form")
		return
	}
	tok := r.Form.Get("token")
	if subtle.ConstantTimeCompare([]byte(tok), []byte(s.opt.Token)) != 1 {
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(http.StatusUnauthorized)
		s.renderLogin(w, "that token is not right")
		return
	}
	var b [24]byte
	rand.Read(b[:])
	id := hex.EncodeToString(b[:])
	s.mu.Lock()
	for k, exp := range s.sessions {
		if time.Now().After(exp) {
			delete(s.sessions, k)
		}
	}
	s.sessions[id] = time.Now().Add(sessionTTL)
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: id, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil, MaxAge: int(sessionTTL.Seconds())})
	http.Redirect(w, r, "/", http.StatusFound)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		s.mu.Lock()
		delete(s.sessions, c.Value)
		s.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1})
	http.Redirect(w, r, "/login", http.StatusFound)
}

// ---- helpers ----

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", " ")
	enc.Encode(v)
}

func jsonError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}

func boolParam(r *http.Request, name string, def bool) bool {
	v := r.URL.Query().Get(name)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

// ---- handlers ----

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "version": s.opt.Version, "environments": len(s.eng.Envs())})
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	b, err := ui.Files.ReadFile("index.html")
	if err != nil {
		http.Error(w, "ui missing", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(b)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"actor": actorOf(r), "auth": s.opt.Token != "", "version": s.opt.Version})
}

func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	var list []engine.EnvSummary
	for _, v := range s.eng.Envs() {
		list = append(list, s.eng.Summary(r.Context(), v))
	}
	if list == nil {
		list = []engine.EnvSummary{}
	}
	out := map[string]any{"version": s.opt.Version, "now": time.Now(), "environments": list,
		"environmentsDir": s.eng.Workspace().EnvironmentsDir, "probeInterval": s.eng.Workspace().ProbeInterval.D().String()}
	if err := s.eng.LoadError(); err != nil {
		out["loadError"] = err.Error()
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) reload(w http.ResponseWriter, r *http.Request) {
	err := s.eng.Reload()
	out := map[string]any{"ok": err == nil, "environments": len(s.eng.Envs())}
	if err != nil {
		out["error"] = err.Error()
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) environment(w http.ResponseWriter, r *http.Request) {
	v, err := s.eng.Env(r.PathValue("env"))
	if err != nil {
		jsonError(w, http.StatusNotFound, err.Error())
		return
	}
	d, err := s.eng.Detail(r.Context(), v, boolParam(r, "drift", false))
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) check(w http.ResponseWriter, r *http.Request) {
	rep, err := s.eng.Check(r.Context(), r.PathValue("env"), engine.CheckOptions{Record: boolParam(r, "record", true), Drift: boolParam(r, "drift", true), Actor: actorOf(r)})
	if err != nil {
		jsonError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

func (s *Server) stale(w http.ResponseWriter, r *http.Request) {
	v, err := s.eng.Env(r.PathValue("env"))
	if err != nil {
		jsonError(w, http.StatusNotFound, err.Error())
		return
	}
	rep, err := s.eng.StaleReport(r.Context(), v, nil, boolParam(r, "drift", true))
	if err != nil && rep == nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

func (s *Server) reset(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Persona   string `json:"persona"`
		Version   int    `json:"version"`
		Force     bool   `json:"force"`
		SkipReset bool   `json:"skipReset"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&body); err != nil {
			jsonError(w, http.StatusBadRequest, "bad json: "+err.Error())
			return
		}
	}
	id, err := s.eng.StartReset(r.PathValue("env"), engine.ResetOptions{Persona: body.Persona, Version: body.Version, Force: body.Force, SkipReset: body.SkipReset, Actor: actorOf(r)})
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "busy") {
			status = http.StatusConflict
		}
		jsonError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"runId": id})
}

func (s *Server) snapshot(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Dependency string `json:"dependency"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "bad json: "+err.Error())
		return
	}
	id, err := s.eng.StartSnapshot(r.PathValue("env"), body.Dependency, actorOf(r))
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "busy") {
			status = http.StatusConflict
		}
		jsonError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"runId": id})
}

func (s *Server) heal(w http.ResponseWriter, r *http.Request) {
	id, err := s.eng.StartHeal(r.PathValue("env"), r.PathValue("dep"), actorOf(r))
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "busy") {
			status = http.StatusConflict
		}
		jsonError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"runId": id})
}

func (s *Server) targets(w http.ResponseWriter, r *http.Request) {
	v, err := s.eng.Env(r.PathValue("env"))
	if err != nil {
		jsonError(w, http.StatusNotFound, err.Error())
		return
	}
	out := s.eng.Targets(r.Context(), v)
	if out == nil {
		out = []engine.TargetStatus{}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) runs(w http.ResponseWriter, r *http.Request) {
	env := r.PathValue("env")
	if _, err := s.eng.Env(env); err != nil {
		jsonError(w, http.StatusNotFound, err.Error())
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 50
	}
	list, err := s.eng.Store().ListRuns(env, limit)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	active := s.eng.ActiveRuns(env)
	writeJSON(w, http.StatusOK, append(active, list...))
}

func (s *Server) run(w http.ResponseWriter, r *http.Request) {
	if _, err := s.eng.Env(r.PathValue("env")); err != nil {
		jsonError(w, http.StatusNotFound, err.Error())
		return
	}
	run, err := s.eng.Run(r.PathValue("env"), r.PathValue("id"))
	if err != nil {
		jsonError(w, http.StatusNotFound, "run not found")
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (s *Server) runEvents(w http.ResponseWriter, r *http.Request) {
	env, id := r.PathValue("env"), r.PathValue("id")
	if _, err := s.eng.Env(env); err != nil {
		jsonError(w, http.StatusNotFound, err.Error())
		return
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		jsonError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	send := func(name string, v any) {
		b, _ := json.Marshal(v)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, b)
		fl.Flush()
	}
	snap, ch, cancel, live := s.eng.Subscribe(id)
	if !live {
		run, err := s.eng.Run(env, id)
		if err != nil {
			jsonError(w, http.StatusNotFound, "run not found")
			return
		}
		w.WriteHeader(http.StatusOK)
		send("snapshot", run)
		brief := run.Brief()
		send("done", engine.Event{Type: "done", RunID: run.ID, At: run.EndedAt, Status: run.Status, Run: &brief})
		return
	}
	defer cancel()
	w.WriteHeader(http.StatusOK)
	send("snapshot", snap)
	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			fl.Flush()
		case ev, ok := <-ch:
			if !ok {
				return
			}
			send(ev.Type, ev)
			if ev.Type == "done" {
				return
			}
		}
	}
}

// ListenAndServe runs the HTTP server until ctx is cancelled.
func (s *Server) ListenAndServe(ctx context.Context, addr string) error {
	if s.opt.Token == "" && !s.opt.Insecure && !isLoopback(addr) {
		return fmt.Errorf("refusing to listen on %s without a token: set GREENROOM_TOKEN (or pass --insecure to allow open access)", addr)
	}
	srv := &http.Server{Addr: addr, Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	errCh := make(chan error, 1)
	go func() {
		s.log.Info("portal listening", "addr", addr, "auth", s.opt.Token != "")
		errCh <- srv.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func isLoopback(addr string) bool {
	host := addr
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		host = addr[:i]
	}
	host = strings.Trim(host, "[]")
	return host == "127.0.0.1" || host == "localhost" || host == "::1"
}
