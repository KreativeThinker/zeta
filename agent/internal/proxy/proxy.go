package proxy

import (
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
)

// Manager starts and stops TCP proxy listeners for services this agent hosts.
type Manager struct {
	mu        sync.Mutex
	listeners map[string]*serviceListener // service name → listener
}

type serviceListener struct {
	name        string
	targetAddr  string
	allowedPKs  map[string]struct{} // WG pubkeys allowed to connect
	meshIPToPK  map[string]string   // peer mesh IP → WG pubkey (updated from NetworkMap)
	ln          net.Listener
}

func New() *Manager {
	return &Manager{listeners: make(map[string]*serviceListener)}
}

// Sync reconciles running listeners against the desired state.
// meshIP is this agent's own mesh IP (used as the bind address).
// services is a list of (name, port, targetAddr, allowedPKs) for services this agent hosts.
// ipToPK maps every peer's mesh IP to their WG public key (for ACL lookups).
func (m *Manager) Sync(meshIP string, services []ServiceConfig, ipToPK map[string]string) {
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

	// Start listeners for new services.
	for name, cfg := range desired {
		if _, running := m.listeners[name]; running {
			// Update ACL and peer map in place.
			l := m.listeners[name]
			l.allowedPKs = setOf(cfg.AllowedPKs)
			l.meshIPToPK = ipToPK
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

func (sl *serviceListener) serve() {
	for {
		conn, err := sl.ln.Accept()
		if err != nil {
			// Listener closed — normal shutdown.
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

	// Look up the WG pubkey for this source mesh IP.
	pk, ok := sl.meshIPToPK[srcIP]
	if !ok {
		slog.Warn("proxy: unknown source mesh IP", "service", sl.name, "src", srcIP)
		return
	}

	if _, allowed := sl.allowedPKs[pk]; !allowed {
		slog.Warn("proxy: access denied", "service", sl.name, "src", srcIP)
		return
	}

	upstream, err := net.Dial("tcp", sl.targetAddr)
	if err != nil {
		slog.Error("proxy: connecting to target", "service", sl.name, "target", sl.targetAddr, "err", err)
		return
	}
	defer upstream.Close()

	slog.Debug("proxy: forwarding", "service", sl.name, "src", srcIP, "target", sl.targetAddr)

	done := make(chan struct{}, 2)
	go func() { io.Copy(upstream, conn); done <- struct{}{} }()  //nolint:errcheck
	go func() { io.Copy(conn, upstream); done <- struct{}{} }()  //nolint:errcheck
	<-done
}

// ServiceConfig describes a service this agent should proxy.
type ServiceConfig struct {
	Name        string
	Port        int
	TargetAddr  string
	AllowedPKs  []string
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
