package main

import (
	"log/slog"
	"slices"
	"strings"

	"github.com/kreativethinker/zeta/agent/internal/firewall"
)

// applyFirewallRules recomputes and applies the firewall rule set from the
// effective service ACLs, current peer IPs, and any user-configured extra
// rules in the zetafile.
func (a *Agent) applyFirewall() {
	if a.fwMgr == nil {
		return
	}
	zf := a.zf.Load()
	hostToIPPtr := a.hostToIP.Load()

	// Derive per-service allow rules from access lists + current peer IPs.
	var serviceRules []firewall.Rule
	if hostToIPPtr != nil {
		hostToIP := *hostToIPPtr
		for _, svc := range a.effectiveServices() {
			if slices.Contains(svc.Access, "*") {
				serviceRules = append(serviceRules, firewall.Rule{}) // any source on the mesh interface
				continue
			}
			var ips []string
			for _, entry := range svc.Access {
				hostname, _ := strings.CutPrefix(entry, "user:")
				if ip, ok := hostToIP[hostname]; ok {
					ips = append(ips, ip)
				}
			}
			if len(ips) > 0 {
				serviceRules = append(serviceRules, firewall.Rule{SrcIPs: ips})
			}
		}
	}

	// User-configured extra rules from zetafile firewall.rules.
	var extraRules []firewall.Rule
	if zf != nil {
		for _, r := range zf.Firewall.Rules {
			fr := firewall.Rule{Proto: r.Proto, Port: r.Port}
			if r.From != "" && r.From != "any" {
				fr.SrcIPs = []string{r.From}
			}
			extraRules = append(extraRules, fr)
		}
	}

	if err := a.fwMgr.Apply(serviceRules, extraRules, a.proxyPort); err != nil {
		slog.Error("applying firewall rules", "err", err)
	}
}
