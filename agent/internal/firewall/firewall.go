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

// New returns a Manager for the given WireGuard interface.
// prefer selects the backend explicitly ("ufw" or "nft"); empty = auto-detect (ufw → nft).
func New(iface, prefer string) *Manager {
	name := prefer
	if name == "" {
		var ok bool
		name, ok = Available()
		if !ok {
			slog.Warn("no firewall backend available — firewall disabled")
			return &Manager{}
		}
	} else {
		if _, err := exec.LookPath(name); err != nil {
			slog.Warn("configured firewall backend not found", "backend", name)
			return &Manager{}
		}
	}
	var b backend
	switch name {
	case "ufw":
		b = &ufwBackend{iface: iface}
	case "nft":
		b = &nftBackend{iface: iface}
	default:
		slog.Warn("unknown firewall backend", "backend", name)
		return &Manager{}
	}
	slog.Info("firewall backend selected", "backend", name, "interface", iface)
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
