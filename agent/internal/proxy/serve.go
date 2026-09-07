package proxy

import (
	"net"
	"net/http"
	"strings"
	"time"
)

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
	trusted := m.isTrustedProxy(net.ParseIP(directIP))
	srcIP := resolveSourceIP(directIP, r.Header.Get("X-Forwarded-For"), trusted)

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

// resolveSourceIP determines the real client IP for an inbound request: the
// direct TCP peer, unless it's a trusted proxy or outside the mesh CIDR, in
// which case the first X-Forwarded-For entry is used instead (when present).
// Pure — trusted is precomputed by the caller, which does the actual
// trusted-proxy lookup against Manager state.
func resolveSourceIP(directIP, xff string, trusted bool) string {
	useXFF := trusted || !meshCIDR.Contains(net.ParseIP(directIP))
	if useXFF && xff != "" {
		return strings.TrimSpace(strings.SplitN(xff, ",", 2)[0])
	}
	return directIP
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
