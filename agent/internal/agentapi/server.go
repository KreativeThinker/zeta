package agentapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/kreativethinker/zeta/agent/internal/config"
	"github.com/kreativethinker/zeta/agent/internal/state"
)

type Server struct {
	st         *state.State
	servicesFn func() []config.ZetaService
}

// New builds the agent's local HTTP API + embedded web UI. servicesFn
// returns the current set of Docker-label-discovered mesh services
// (read-only — services are declared via container `caddy` labels, not
// through this API).
func New(st *state.State, servicesFn func() []config.ZetaService) http.Handler {
	s := &Server{
		st:         st,
		servicesFn: servicesFn,
	}

	r := chi.NewRouter()
	r.Use(corsMiddleware)

	r.Get("/api/status", s.handleStatus)
	r.Get("/api/services", s.handleListServices)

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
	svcs := s.servicesFn()
	if svcs == nil {
		svcs = []config.ZetaService{}
	}
	writeJSON(w, http.StatusOK, svcs)
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
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
