package proxy

import (
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"strings"
	"sync"
)

// Manager runs a single HTTP reverse proxy that routes by Host header.
type Manager struct {
	mu          sync.RWMutex
	routes      map[string]*route // service name → route
	ipToPK      map[string]string
	ipToHost    map[string]string
	trustedNets []*net.IPNet

	server *http.Server

	eventsMu sync.RWMutex
	events   []AccessEvent
}

type route struct {
	targetAddr string
	allowAll   bool
	allowedPKs map[string]struct{}
	rp         *httputil.ReverseProxy
}

// ServiceConfig describes a service this agent should proxy.
type ServiceConfig struct {
	Name       string
	TargetAddr string
	AllowedPKs []string
}

func New() *Manager {
	return &Manager{
		routes:   make(map[string]*route),
		ipToPK:   make(map[string]string),
		ipToHost: make(map[string]string),
	}
}

// SetTrustedProxies configures CIDRs (or single IPs with /32) whose
// X-Forwarded-For header is trusted to carry the real client IP.
// Loopback addresses are always trusted regardless of this list.
// The local mesh IP should be included so that a reverse proxy like Caddy
// running on the same host is handled correctly.
func (m *Manager) SetTrustedProxies(cidrs []string) {
	nets := make([]*net.IPNet, 0, len(cidrs))
	for _, cidr := range cidrs {
		if !strings.Contains(cidr, "/") {
			cidr += "/32"
		}
		_, n, err := net.ParseCIDR(cidr)
		if err != nil {
			slog.Warn("proxy: ignoring invalid trusted proxy CIDR", "cidr", cidr, "err", err)
			continue
		}
		nets = append(nets, n)
	}
	m.mu.Lock()
	m.trustedNets = nets
	m.mu.Unlock()
}

// Start begins listening on addr. Must be called once before Sync has any effect.
func (m *Manager) Start(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	m.server = &http.Server{Handler: m}
	slog.Info("proxy listening", "addr", addr)
	go m.server.Serve(ln) //nolint:errcheck
	return nil
}

// Stop shuts down the proxy listener.
func (m *Manager) Stop() {
	if m.server != nil {
		m.server.Close()
	}
}
