package dns

import (
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"strings"
)

const resolvConfPath = "/etc/resolv.conf"

// SetupSystemDNS points the OS at our local resolver for mesh DNS.
// Tries resolvectl (systemd-resolved) first; falls back to /etc/resolv.conf.
// Returns a teardown function that restores original state.
func SetupSystemDNS(listenAddr, iface, domain string) (teardown func(), err error) {
	ip, _, err := net.SplitHostPort(listenAddr)
	if err != nil {
		// listenAddr might be bare IP without port
		ip = listenAddr
	}

	if useResolvectl(ip, iface, domain) {
		slog.Info("DNS: configured via resolvectl", "iface", iface, "server", ip)
		return func() { teardownResolvectl(iface) }, nil
	}

	restore, err := prependResolvConf(ip)
	if err != nil {
		return func() {}, fmt.Errorf("configuring /etc/resolv.conf: %w", err)
	}
	slog.Info("DNS: prepended nameserver to /etc/resolv.conf", "server", ip)
	return restore, nil
}

// useResolvectl tries to configure DNS via systemd-resolved. Returns true on success.
// Both the DNS server AND the routing domain must be set — without the routing
// domain, 127.0.0.1 becomes a generic fallback for all queries, which breaks
// non-mesh DNS resolution (e.g. STUN servers resolving to IPv6 via 1.1.1.1).
func useResolvectl(ip, iface, domain string) bool {
	if _, err := exec.LookPath("resolvectl"); err != nil {
		return false
	}
	// Clear any stale config from a previous agent crash (teardown never ran).
	_ = exec.Command("resolvectl", "revert", iface).Run()

	if err := exec.Command("resolvectl", "dns", iface, ip).Run(); err != nil {
		return false
	}
	if err := exec.Command("resolvectl", "domain", iface, "~"+domain).Run(); err != nil {
		// Roll back the DNS setting so 127.0.0.1 is not left as a generic fallback.
		_ = exec.Command("resolvectl", "dns", iface, "").Run()
		slog.Warn("DNS: resolvectl domain failed, reverting", "err", err)
		return false
	}
	return true
}

func teardownResolvectl(iface string) {
	_ = exec.Command("resolvectl", "revert", iface).Run()
	slog.Info("DNS: reverted resolvectl config", "iface", iface)
}

// prependResolvConf prepends "nameserver <ip>" to /etc/resolv.conf.
// Returns a function that restores the original file.
func prependResolvConf(ip string) (func(), error) {
	original, err := os.ReadFile(resolvConfPath)
	if err != nil && !os.IsNotExist(err) {
		return func() {}, err
	}

	// Avoid duplicates.
	line := "nameserver " + ip
	if strings.Contains(string(original), line) {
		return func() {}, nil
	}

	updated := line + "\n" + string(original)
	if err := os.WriteFile(resolvConfPath, []byte(updated), 0644); err != nil {
		return func() {}, err
	}

	return func() {
		if err := os.WriteFile(resolvConfPath, original, 0644); err != nil {
			slog.Warn("DNS: failed to restore /etc/resolv.conf", "err", err)
		} else {
			slog.Info("DNS: restored /etc/resolv.conf")
		}
	}, nil
}
