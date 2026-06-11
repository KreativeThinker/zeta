package agentapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/kreativethinker/zeta/agent/internal/config"
	"github.com/kreativethinker/zeta/agent/internal/proxy"
	"github.com/kreativethinker/zeta/agent/internal/state"
)

type Server struct {
	st           *state.State
	zetafilePath string
	proxyMgr     *proxy.Manager
	announceFn   func(*config.Zetafile) // called after zetafile changes
}

func New(
	st *state.State,
	zetafilePath string,
	pm *proxy.Manager,
	announceFn func(*config.Zetafile),
) http.Handler {
	s := &Server{
		st:           st,
		zetafilePath: zetafilePath,
		proxyMgr:     pm,
		announceFn:   announceFn,
	}

	r := chi.NewRouter()
	r.Use(corsMiddleware)

	r.Get("/api/status", s.handleStatus)
	r.Get("/api/services", s.handleListServices)
	r.Post("/api/services", s.handleAddService)
	r.Delete("/api/services/{name}", s.handleDeleteService)
	r.Get("/api/logs", s.handleLogs)

	// Serve embedded web UI.
	r.Handle("/*", http.FileServer(http.FS(WebFiles)))

	return r
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	// Domain is "hostname.mesh" — extract just the hostname part.
	hostname := s.st.Domain
	if idx := strings.Index(hostname, "."); idx != -1 {
		hostname = hostname[:idx]
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"node_id":  s.st.NodeID,
		"hostname": hostname,
		"mesh_ip":  s.st.MeshIP,
	})
}

func (s *Server) handleListServices(w http.ResponseWriter, r *http.Request) {
	zf, err := config.LoadZetafile(s.zetafilePath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if zf.Services == nil {
		zf.Services = []config.ZetaService{}
	}
	writeJSON(w, http.StatusOK, zf.Services)
}

func (s *Server) handleAddService(w http.ResponseWriter, r *http.Request) {
	var svc config.ZetaService
	if err := json.NewDecoder(r.Body).Decode(&svc); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if svc.Name == "" || svc.Target == "" {
		writeError(w, http.StatusBadRequest, "name and target are required")
		return
	}

	zf, err := config.LoadZetafile(s.zetafilePath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Replace if name already exists, otherwise append.
	found := false
	for i, existing := range zf.Services {
		if existing.Name == svc.Name {
			zf.Services[i] = svc
			found = true
			break
		}
	}
	if !found {
		zf.Services = append(zf.Services, svc)
	}

	if err := config.SaveZetafile(s.zetafilePath, zf); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.announceFn(zf)
	writeJSON(w, http.StatusOK, svc)
}

func (s *Server) handleDeleteService(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")

	zf, err := config.LoadZetafile(s.zetafilePath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	filtered := zf.Services[:0]
	for _, svc := range zf.Services {
		if svc.Name != name {
			filtered = append(filtered, svc)
		}
	}
	zf.Services = filtered

	if err := config.SaveZetafile(s.zetafilePath, zf); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.announceFn(zf)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	events := s.proxyMgr.RecentEvents(200)
	if events == nil {
		events = []proxy.AccessEvent{}
	}
	writeJSON(w, http.StatusOK, events)
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
