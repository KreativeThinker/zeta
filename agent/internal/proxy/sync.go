package proxy

import (
	"log/slog"
	"net/http/httputil"
	"net/url"
)

// Sync reconciles the routing table and ACL maps.
func (m *Manager) Sync(services []ServiceConfig, ipToPK, ipToHost map[string]string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.ipToPK = ipToPK
	m.ipToHost = ipToHost

	desired := make(map[string]ServiceConfig, len(services))
	for _, s := range services {
		desired[s.Name] = s
	}

	for name := range m.routes {
		if _, ok := desired[name]; !ok {
			delete(m.routes, name)
			slog.Info("proxy: removed route", "service", name)
		}
	}

	for name, cfg := range desired {
		allowAll := isWildcard(cfg.AllowedPKs)
		if r, ok := m.routes[name]; ok && r.targetAddr == cfg.TargetAddr {
			r.allowAll = allowAll
			r.allowedPKs = setOf(cfg.AllowedPKs)
			continue
		}
		target, err := url.Parse("http://" + cfg.TargetAddr)
		if err != nil {
			slog.Error("proxy: invalid target", "service", name, "target", cfg.TargetAddr, "err", err)
			continue
		}
		m.routes[name] = &route{
			targetAddr: cfg.TargetAddr,
			allowAll:   allowAll,
			allowedPKs: setOf(cfg.AllowedPKs),
			rp:         httputil.NewSingleHostReverseProxy(target),
		}
		slog.Info("proxy: added route", "service", name, "target", cfg.TargetAddr)
	}
}
