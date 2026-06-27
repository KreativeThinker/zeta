package firewall

import (
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
)

type ufwBackend struct {
	iface string
	mu    sync.Mutex
	rules []string // rule specs added by us, tracked for cleanup
}

func (u *ufwBackend) apply(serviceRules, extraRules []Rule, proxyPort int) error {
	u.mu.Lock()
	defer u.mu.Unlock()

	for _, spec := range u.rules {
		args := append([]string{"delete", "allow"}, strings.Fields(spec)...)
		if out, err := exec.Command("ufw", args...).CombinedOutput(); err != nil {
			slog.Debug("ufw delete", "spec", spec, "err", err, "out", strings.TrimSpace(string(out)))
		}
	}

	var next []string

	seen := make(map[string]bool)
	for _, r := range serviceRules {
		for _, ip := range r.SrcIPs {
			if seen[ip] {
				continue
			}
			seen[ip] = true
			next = append(next, fmt.Sprintf("in on %s from %s to any port %d proto tcp", u.iface, ip, proxyPort))
		}
	}

	for _, r := range extraRules {
		if spec := ufwSpec(u.iface, r); spec != "" {
			next = append(next, spec)
		}
	}

	for _, spec := range next {
		args := append([]string{"allow"}, strings.Fields(spec)...)
		if out, err := exec.Command("ufw", args...).CombinedOutput(); err != nil {
			return fmt.Errorf("ufw allow %s: %w: %s", spec, err, strings.TrimSpace(string(out)))
		}
	}

	u.rules = next
	slog.Info("firewall updated", "backend", "ufw", "service_rules", len(serviceRules), "extra_rules", len(extraRules))
	return nil
}

func (u *ufwBackend) flush() {
	u.mu.Lock()
	defer u.mu.Unlock()
	for _, spec := range u.rules {
		args := append([]string{"delete", "allow"}, strings.Fields(spec)...)
		exec.Command("ufw", args...).Run() //nolint:errcheck
	}
	u.rules = nil
}

func ufwSpec(iface string, r Rule) string {
	parts := []string{"in", "on", iface}
	if len(r.SrcIPs) > 0 {
		parts = append(parts, "from", r.SrcIPs[0])
	}
	switch strings.ToLower(r.Proto) {
	case "icmp":
		parts = append(parts, "proto", "icmp")
		return strings.Join(parts, " ")
	case "tcp", "udp":
		if r.Port > 0 {
			parts = append(parts, "to", "any", "port", fmt.Sprint(r.Port), "proto", strings.ToLower(r.Proto))
		} else {
			parts = append(parts, "proto", strings.ToLower(r.Proto))
		}
	case "any", "":
		if r.Port > 0 {
			parts = append(parts, "to", "any", "port", fmt.Sprint(r.Port))
		}
	}
	return strings.Join(parts, " ")
}
