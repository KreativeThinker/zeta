package firewall

import (
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
)

// Manager manages the zeta nftables firewall table.
type Manager struct {
	iface string
}

// Rule describes a single allow rule applied to the mesh interface.
type Rule struct {
	Proto  string   // "tcp", "udp", "icmp", "any"
	Port   int      // 0 = any port (only meaningful for tcp/udp)
	SrcIPs []string // empty = any source IP on the mesh interface
}

// Available reports whether nft is present on this system.
func Available() bool {
	_, err := exec.LookPath("nft")
	return err == nil
}

func New(iface string) *Manager {
	return &Manager{iface: iface}
}

// Apply atomically replaces the zeta firewall ruleset.
// serviceRules are derived from zetafile access lists + the network map.
// extraRules are user-configured additions from zetafile firewall.rules.
// proxyPort is the port Zeta's proxy listens on.
func (m *Manager) Apply(serviceRules, extraRules []Rule, proxyPort int) error {
	// Delete the existing table so we can recreate it cleanly.
	exec.Command("nft", "delete", "table", "inet", "zeta").Run() //nolint:errcheck

	script := m.buildScript(serviceRules, extraRules, proxyPort)
	cmd := exec.Command("nft", "-f", "-")
	cmd.Stdin = strings.NewReader(script)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("nft: %w: %s", err, strings.TrimSpace(string(out)))
	}
	slog.Info("firewall updated", "service_rules", len(serviceRules), "extra_rules", len(extraRules))
	return nil
}

// Flush removes the zeta firewall table. Call on agent shutdown.
func (m *Manager) Flush() {
	if out, err := exec.Command("nft", "delete", "table", "inet", "zeta").CombinedOutput(); err != nil {
		slog.Debug("firewall flush", "err", err, "out", strings.TrimSpace(string(out)))
	}
}

func (m *Manager) buildScript(serviceRules, extraRules []Rule, proxyPort int) string {
	var b strings.Builder

	fmt.Fprintf(&b, "table inet zeta {\n")
	fmt.Fprintf(&b, "    chain input {\n")
	fmt.Fprintf(&b, "        type filter hook input priority filter; policy accept;\n")

	// Only act on traffic arriving on the WireGuard interface.
	fmt.Fprintf(&b, "        iif != %q return\n", m.iface)

	// Baseline: allow established/related, drop invalid, allow ICMP.
	fmt.Fprintf(&b, "        ct state established,related accept\n")
	fmt.Fprintf(&b, "        ct state invalid drop\n")
	fmt.Fprintf(&b, "        ip protocol icmp accept\n")
	fmt.Fprintf(&b, "        ip6 nexthdr ipv6-icmp accept\n")

	// Service-derived allows: specific source IPs → proxy port.
	seen := make(map[string]bool)
	for _, r := range serviceRules {
		for _, ip := range r.SrcIPs {
			if seen[ip] {
				continue
			}
			seen[ip] = true
			fmt.Fprintf(&b, "        ip saddr %s tcp dport %d accept\n", ip, proxyPort)
		}
	}

	// User-configured extra rules.
	for _, r := range extraRules {
		if line := ruleToNft(r); line != "" {
			fmt.Fprintf(&b, "        %s accept\n", line)
		}
	}

	// Default deny for everything else on this interface.
	fmt.Fprintf(&b, "        drop\n")
	fmt.Fprintf(&b, "    }\n")
	fmt.Fprintf(&b, "}\n")

	return b.String()
}

func ruleToNft(r Rule) string {
	var parts []string

	if len(r.SrcIPs) > 0 {
		parts = append(parts, fmt.Sprintf("ip saddr %s", r.SrcIPs[0]))
	}

	switch strings.ToLower(r.Proto) {
	case "tcp":
		if r.Port > 0 {
			parts = append(parts, fmt.Sprintf("tcp dport %d", r.Port))
		} else {
			parts = append(parts, "meta l4proto tcp")
		}
	case "udp":
		if r.Port > 0 {
			parts = append(parts, fmt.Sprintf("udp dport %d", r.Port))
		} else {
			parts = append(parts, "meta l4proto udp")
		}
	case "icmp":
		parts = append(parts, "ip protocol icmp")
	case "any", "":
		// no protocol filter
	}

	return strings.Join(parts, " ")
}
