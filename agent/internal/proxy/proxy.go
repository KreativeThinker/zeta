package proxy

import (
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"
)

const maxEvents = 500

// AccessEvent records a single inbound connection attempt.
type AccessEvent struct {
	Time     time.Time `json:"time"`
	Service  string    `json:"service"`
	SourceIP string    `json:"source_ip"`
	Hostname string    `json:"hostname"`
	Allowed  bool      `json:"allowed"`
}

// Manager runs a single HTTP reverse proxy that routes by Host header.
type Manager struct {
	mu           sync.RWMutex
	routes       map[string]*route // service name → route
	ipToPK       map[string]string
	ipToHost     map[string]string
	trustedNets  []*net.IPNet

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

var meshCIDR = func() *net.IPNet {
	_, cidr, _ := net.ParseCIDR("100.64.0.0/10")
	return cidr
}()

func (m *Manager) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	directIP, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	srcIP := directIP
	parsed := net.ParseIP(directIP)
	if m.isTrustedProxy(parsed) || !meshCIDR.Contains(parsed) {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			srcIP = strings.TrimSpace(strings.SplitN(xff, ",", 2)[0])
		}
	}

	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	serviceName := strings.SplitN(host, ".", 2)[0]

	m.mu.RLock()
	rt, routeOk := m.routes[serviceName]
	pk, pkKnown := m.ipToPK[srcIP]
	hostname := m.ipToHost[srcIP]
	var allowed bool
	if routeOk && rt.allowAll {
		allowed = true
	} else if routeOk && pkKnown {
		_, allowed = rt.allowedPKs[pk]
	}
	m.mu.RUnlock()

	if !routeOk {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if !pkKnown || !allowed {
		m.record(AccessEvent{Time: time.Now(), Service: serviceName, SourceIP: srcIP, Hostname: hostname, Allowed: false})
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	m.record(AccessEvent{Time: time.Now(), Service: serviceName, SourceIP: srcIP, Hostname: hostname, Allowed: true})
	rt.rp.ServeHTTP(w, r)
}

// RecentEvents returns up to n most recent access events (newest first).
func (m *Manager) RecentEvents(n int) []AccessEvent {
	m.eventsMu.RLock()
	defer m.eventsMu.RUnlock()
	if n <= 0 || n > len(m.events) {
		n = len(m.events)
	}
	out := make([]AccessEvent, n)
	for i := 0; i < n; i++ {
		out[i] = m.events[len(m.events)-1-i]
	}
	return out
}

func (m *Manager) record(ev AccessEvent) {
	m.eventsMu.Lock()
	defer m.eventsMu.Unlock()
	if len(m.events) >= maxEvents {
		m.events = m.events[1:]
	}
	m.events = append(m.events, ev)
}

// isTrustedProxy returns true when ip is a loopback address or matches one of
// the configured trusted proxy CIDRs. These are sources whose X-Forwarded-For
// header should be used to determine the real client IP.
func (m *Manager) isTrustedProxy(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsLoopback() {
		return true
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, n := range m.trustedNets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// isWildcard reports whether an ACL list grants access to any enrolled mesh peer.
func isWildcard(pks []string) bool {
	for _, pk := range pks {
		if pk == "*" {
			return true
		}
	}
	return false
}

func setOf(keys []string) map[string]struct{} {
	m := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		m[k] = struct{}{}
	}
	return m
}
