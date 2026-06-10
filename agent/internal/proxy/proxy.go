package proxy

import (
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"
)

const maxEvents = 500

// AccessEvent records a single inbound connection attempt.
type AccessEvent struct {
	Time     time.Time `json:"time"`
	Service  string    `json:"service"`
	SourceIP string    `json:"source_ip"`
	Hostname string    `json:"hostname"` // resolved from mesh IP, empty if unknown
	Allowed  bool      `json:"allowed"`
}

// Manager starts and stops TCP proxy listeners for services this agent hosts.
type Manager struct {
	mu        sync.Mutex
	listeners map[string]*serviceListener // service name → listener

	eventsMu sync.RWMutex
	events   []AccessEvent // ring buffer capped at maxEvents
}

type serviceListener struct {
	name       string
	targetAddr string
	allowedPKs map[string]struct{} // WG pubkeys allowed to connect
	meshIPToPK map[string]string   // peer mesh IP → WG pubkey
	ipToHost   map[string]string   // peer mesh IP → hostname
	mgr        *Manager
	ln         net.Listener
}

func New() *Manager {
	return &Manager{listeners: make(map[string]*serviceListener)}
}

// Sync reconciles running listeners against the desired state.
// meshIP is this agent's own mesh IP (used as the bind address).
// ipToPK maps every peer's mesh IP to their WG public key.
// ipToHost maps every peer's mesh IP to their hostname.
func (m *Manager) Sync(meshIP string, services []ServiceConfig, ipToPK, ipToHost map[string]string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	desired := make(map[string]ServiceConfig, len(services))
	for _, s := range services {
		desired[s.Name] = s
	}

	// Stop listeners for removed or changed services.
	for name, l := range m.listeners {
		cfg, ok := desired[name]
		if !ok || cfg.TargetAddr != l.targetAddr || cfg.Port != portFromAddr(l.ln.Addr().String()) {
			slog.Info("stopping proxy", "service", name)
			l.ln.Close()
			delete(m.listeners, name)
		}
	}

	// Start or update listeners.
	for name, cfg := range desired {
		if l, running := m.listeners[name]; running {
			l.allowedPKs = setOf(cfg.AllowedPKs)
			l.meshIPToPK = ipToPK
			l.ipToHost = ipToHost
			continue
		}
		addr := net.JoinHostPort(meshIP, fmt.Sprint(cfg.Port))
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			slog.Error("starting proxy listener", "service", name, "addr", addr, "err", err)
			continue
		}
		sl := &serviceListener{
			name:       name,
			targetAddr: cfg.TargetAddr,
			allowedPKs: setOf(cfg.AllowedPKs),
			meshIPToPK: ipToPK,
			ipToHost:   ipToHost,
			mgr:        m,
			ln:         ln,
		}
		m.listeners[name] = sl
		slog.Info("proxy listening", "service", name, "addr", addr, "target", cfg.TargetAddr)
		go sl.serve()
	}
}

// StopAll shuts down every running listener.
func (m *Manager) StopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for name, l := range m.listeners {
		l.ln.Close()
		delete(m.listeners, name)
		slog.Info("stopped proxy", "service", name)
	}
}

// RecentEvents returns up to n most recent access events (newest first).
func (m *Manager) RecentEvents(n int) []AccessEvent {
	m.eventsMu.RLock()
	defer m.eventsMu.RUnlock()
	if n <= 0 || n > len(m.events) {
		n = len(m.events)
	}
	out := make([]AccessEvent, n)
	// events are stored oldest-first; return newest-first
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

func (sl *serviceListener) serve() {
	for {
		conn, err := sl.ln.Accept()
		if err != nil {
			return
		}
		go sl.handle(conn)
	}
}

func (sl *serviceListener) handle(conn net.Conn) {
	defer conn.Close()

	srcIP, _, err := net.SplitHostPort(conn.RemoteAddr().String())
	if err != nil {
		slog.Warn("proxy: bad remote addr", "err", err)
		return
	}

	pk, ok := sl.meshIPToPK[srcIP]
	hostname := sl.ipToHost[srcIP]

	if !ok {
		slog.Warn("proxy: unknown source mesh IP", "service", sl.name, "src", srcIP)
		sl.mgr.record(AccessEvent{Time: time.Now(), Service: sl.name, SourceIP: srcIP, Allowed: false})
		return
	}

	if _, allowed := sl.allowedPKs[pk]; !allowed {
		slog.Warn("proxy: access denied", "service", sl.name, "src", srcIP, "hostname", hostname)
		sl.mgr.record(AccessEvent{Time: time.Now(), Service: sl.name, SourceIP: srcIP, Hostname: hostname, Allowed: false})
		return
	}

	sl.mgr.record(AccessEvent{Time: time.Now(), Service: sl.name, SourceIP: srcIP, Hostname: hostname, Allowed: true})

	upstream, err := net.Dial("tcp", sl.targetAddr)
	if err != nil {
		slog.Error("proxy: connecting to target", "service", sl.name, "target", sl.targetAddr, "err", err)
		return
	}
	defer upstream.Close()

	slog.Debug("proxy: forwarding", "service", sl.name, "src", srcIP, "target", sl.targetAddr)

	done := make(chan struct{}, 2)
	go func() { io.Copy(upstream, conn); done <- struct{}{} }() //nolint:errcheck
	go func() { io.Copy(conn, upstream); done <- struct{}{} }() //nolint:errcheck
	<-done
}

// ServiceConfig describes a service this agent should proxy.
type ServiceConfig struct {
	Name       string
	Port       int
	TargetAddr string
	AllowedPKs []string
}

func setOf(keys []string) map[string]struct{} {
	m := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		m[k] = struct{}{}
	}
	return m
}

func portFromAddr(addr string) int {
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return 0
	}
	var port int
	fmt.Sscan(portStr, &port)
	return port
}
