package main

import (
	"log/slog"

	"github.com/kreativethinker/zeta/agent/internal/wg"
)

// applyPeers stores the direct (pre-relay-override) peer configs from the
// latest NetworkMap and applies them, with any active relay overrides, to
// WireGuard.
func (a *Agent) applyPeers(basePeers []wg.PeerConfig) {
	a.peersMu.Lock()
	a.lastPeers = basePeers
	a.peersMu.Unlock()
	a.reapplyPeers()
}

// reapplyPeers reapplies the last-known peer set with pathsel's current
// direct/relay overrides. Called after a NetworkMap update and whenever
// pathsel flips a peer's mode.
func (a *Agent) reapplyPeers() {
	a.peersMu.Lock()
	basePeers := a.lastPeers
	a.peersMu.Unlock()

	peers := a.pathMgr.ApplyOverrides(basePeers, a.relayClient)
	if err := a.wgMgr.ApplyPeers(peers); err != nil {
		slog.Error("applying WireGuard peers", "err", err)
	}
}
