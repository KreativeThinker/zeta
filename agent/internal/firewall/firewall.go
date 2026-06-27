package firewall

import (
	"log/slog"
	"os/exec"
)

// Rule describes a single allow rule on the mesh interface.
type Rule struct {
	Proto  string   // "tcp", "udp", "icmp", "any"
	Port   int      // 0 = any port (only meaningful for tcp/udp)
	SrcIPs []string // empty = any source IP on the mesh interface
}

type backend interface {
	apply(serviceRules, extraRules []Rule, proxyPort int) error
	flush()
}

type Manager struct {
	b backend
}

// Available returns the name of the first available firewall backend
// ("ufw" or "nft") and true if one is found.
func Available() (string, bool) {
	for _, name := range []string{"ufw", "nft"} {
		if _, err := exec.LookPath(name); err == nil {
			return name, true
		}
	}
	return "", false
}

// New returns a Manager using the first available backend (ufw → nft).
// iface is the WireGuard interface name, used by the nft backend.
func New(iface string) *Manager {
	name, ok := Available()
	if !ok {
		return &Manager{}
	}
	var b backend
	switch name {
	case "ufw":
		b = &ufwBackend{}
	case "nft":
		b = &nftBackend{iface: iface}
	}
	slog.Info("firewall backend selected", "backend", name)
	return &Manager{b: b}
}

// Apply replaces all Zeta-managed firewall rules.
func (m *Manager) Apply(serviceRules, extraRules []Rule, proxyPort int) error {
	if m.b == nil {
		return nil
	}
	return m.b.apply(serviceRules, extraRules, proxyPort)
}

// Flush removes all Zeta-managed firewall rules. Call on agent shutdown.
func (m *Manager) Flush() {
	if m.b != nil {
		m.b.flush()
	}
}
