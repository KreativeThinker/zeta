package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/kreativethinker/zeta/controller/internal/coordinator"
	"github.com/kreativethinker/zeta/controller/internal/db"
	zetaweb "github.com/kreativethinker/zeta/controller/web"
)

var (
	// Set by main via ldflags.
	Version = "dev"
	Commit  = "unknown"
)

// NewHTTPServer builds and returns the chi router. Call http.ListenAndServe
// with the returned handler.
func NewHTTPServer(coord *coordinator.Coordinator, database *db.DB, adminPassword string) http.Handler {
	initAuth(adminPassword)

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.RealIP)

	r.Route("/api/v1", func(r chi.Router) {
		// Public: status (used by Docker healthcheck) + auth endpoints.
		r.Get("/status", handleStatus(coord, database))
		r.Post("/auth/login", handleLogin)
		r.Post("/auth/logout", handleLogout)

		// Protected: everything else requires a valid session cookie.
		r.Group(func(r chi.Router) {
			r.Use(requireAuth)

			r.Get("/devices", handleListDevices(coord, database))
			r.Get("/devices/{id}", handleGetDevice(coord, database))
			r.Delete("/devices/{id}", handleDeleteDevice(coord, database))

			r.Get("/preauth-keys", handleListPreauthKeys(database))
			r.Post("/preauth-keys", handleCreatePreauthKey(coord))
			r.Delete("/preauth-keys/{key}", handleDeletePreauthKey(database))

			r.Get("/audit-log", handleAuditLog(database))
		})
	})

	// Serve embedded SvelteKit SPA for everything else.
	r.Handle("/*", zetaweb.Handler())

	return r
}

// ── Status ────────────────────────────────────────────────────────────────────

func handleStatus(coord *coordinator.Coordinator, database *db.DB) http.HandlerFunc {
	start := time.Now()
	return func(w http.ResponseWriter, r *http.Request) {
		devices, _ := database.ListDevices()
		writeJSON(w, http.StatusOK, map[string]any{
			"version":      Version,
			"commit":       Commit,
			"uptime":       time.Since(start).String(),
			"device_count": len(devices),
		})
	}
}

// ── Devices ───────────────────────────────────────────────────────────────────

func handleListDevices(coord *coordinator.Coordinator, database *db.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		devices, err := database.ListDevices()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if devices == nil {
			devices = []db.Device{}
		}
		type withOnline struct {
			db.Device
			Online bool `json:"online"`
		}
		resp := make([]withOnline, 0, len(devices))
		for _, d := range devices {
			resp = append(resp, withOnline{Device: d, Online: coord.IsOnline(d.ID)})
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

func handleGetDevice(coord *coordinator.Coordinator, database *db.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		dev, err := database.GetDevice(id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if dev == nil {
			writeError(w, http.StatusNotFound, "device not found")
			return
		}
		svcs, _ := database.ListServicesByDevice(id)
		writeJSON(w, http.StatusOK, map[string]any{
			"id":            dev.ID,
			"hostname":      dev.Hostname,
			"os":            dev.OS,
			"mesh_ip":       dev.MeshIP,
			"wg_public_key": dev.WGPublicKey,
			"last_endpoint": dev.LastEndpoint,
			"last_seen":     dev.LastSeen,
			"agent_version": dev.AgentVersion,
			"online":        coord.IsOnline(dev.ID),
			"created_at":    dev.CreatedAt,
			"services":      svcs,
		})
	}
}

func handleDeleteDevice(coord *coordinator.Coordinator, database *db.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		dev, err := database.GetDevice(id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if dev == nil {
			writeError(w, http.StatusNotFound, "device not found")
			return
		}
		if err := database.DeleteDevice(id); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		_ = database.AppendAudit("device.deleted", id, map[string]string{"hostname": dev.Hostname})
		go coord.NotifyAll()
		w.WriteHeader(http.StatusNoContent)
	}
}

// ── Preauth Keys ──────────────────────────────────────────────────────────────

func handleListPreauthKeys(database *db.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		keys, err := database.ListPreauthKeys()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if keys == nil {
			keys = []db.PreauthKey{}
		}
		writeJSON(w, http.StatusOK, keys)
	}
}

func handleCreatePreauthKey(coord *coordinator.Coordinator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Label    string `json:"label"`
			TTLHours int    `json:"ttl_hours"`
			Reusable bool   `json:"reusable"`
		}
		req.TTLHours = 24 // default
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err.Error() != "EOF" {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if req.TTLHours <= 0 {
			req.TTLHours = 24
		}

		k, err := coord.GeneratePreauthKey(req.Label, time.Duration(req.TTLHours)*time.Hour, req.Reusable)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, k)
	}
}

func handleDeletePreauthKey(database *db.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := chi.URLParam(r, "key")
		if err := database.DeletePreauthKey(key); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// ── Audit Log ─────────────────────────────────────────────────────────────────

func handleAuditLog(database *db.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		entries, err := database.ListAudit(limit, offset)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, entries)
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
