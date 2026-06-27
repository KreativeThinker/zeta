package firewall

import (
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
)

type nftBackend struct {
	iface string
}

func (n *nftBackend) apply(serviceRules, extraRules []Rule, proxyPort int) error {
	exec.Command("nft", "delete", "table", "inet", "zeta").Run() //nolint:errcheck

	cmd := exec.Command("nft", "-f", "-")
	cmd.Stdin = strings.NewReader(n.buildScript(serviceRules, extraRules, proxyPort))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("nft: %w: %s", err, strings.TrimSpace(string(out)))
	}
	slog.Info("firewall updated", "backend", "nft", "service_rules", len(serviceRules), "extra_rules", len(extraRules))
	return nil
}

func (n *nftBackend) flush() {
	if out, err := exec.Command("nft", "delete", "table", "inet", "zeta").CombinedOutput(); err != nil {
		slog.Debug("firewall flush (nft)", "err", err, "out", strings.TrimSpace(string(out)))
	}
}

func (n *nftBackend) buildScript(serviceRules, extraRules []Rule, proxyPort int) string {
	var b strings.Builder

	fmt.Fprintf(&b, "table inet zeta {\n")
	fmt.Fprintf(&b, "    chain input {\n")
	fmt.Fprintf(&b, "        type filter hook input priority filter; policy accept;\n")
	fmt.Fprintf(&b, "        iif != %q return\n", n.iface)
	fmt.Fprintf(&b, "        ct state established,related accept\n")
	fmt.Fprintf(&b, "        ct state invalid drop\n")
	fmt.Fprintf(&b, "        ip protocol icmp accept\n")
	fmt.Fprintf(&b, "        ip6 nexthdr ipv6-icmp accept\n")

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

	for _, r := range extraRules {
		if expr := nftExpr(r); expr != "" {
			fmt.Fprintf(&b, "        %s accept\n", expr)
		}
	}

	fmt.Fprintf(&b, "        drop\n")
	fmt.Fprintf(&b, "    }\n")
	fmt.Fprintf(&b, "}\n")
	return b.String()
}

func nftExpr(r Rule) string {
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
	}
	return strings.Join(parts, " ")
}
