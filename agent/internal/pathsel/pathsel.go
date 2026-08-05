// Package pathsel tracks, per mesh peer, whether WireGuard traffic should go
// direct (the controller-reported STUN endpoint) or via the controller relay
// fallback, and flips between the two based on observed handshake activity.
package pathsel

import (
	"log/slog"
	"sync"
	"time"

	"github.com/kreativethinker/zeta/agent/internal/relay"
	"github.com/kreativethinker/zeta/agent/internal/wg"
)

type Mode int

const (
	Direct Mode = iota
	Relay
)

// Peer is the subset of NetworkMap peer info pathsel needs to track.
type Peer struct {
	NodeID   string
	PubKey   string // base64, matches wg.PeerConfig.PublicKey and wgctrl handshake keys
	Endpoint string // controller-reported direct (STUN) endpoint
}

type peerState struct {
	pubKey    string
	endpoint  string
	mode      Mode
	appliedAt time.Time
}

// Manager decides, per peer, whether to use the direct endpoint or the relay
// fallback. It holds no network resources itself — RelayAddrFor calls into a
// relay.Client are what actually provision the loopback relay sockets.
type Manager struct {
	handshakeTimeout time.Duration
	retryInterval    time.Duration

	mu          sync.Mutex
	peers       map[string]*peerState // nodeID -> state
	pubKeyIndex map[string]string     // pubKey -> nodeID
}

// New creates a Manager. handshakeTimeout is how long a freshly-applied
// direct endpoint gets before it's considered failed; retryInterval is how
// long a peer stays on relay before direct is tried again.
func New(handshakeTimeout, retryInterval time.Duration) *Manager {
	return &Manager{
		handshakeTimeout: handshakeTimeout,
		retryInterval:    retryInterval,
		peers:            make(map[string]*peerState),
		pubKeyIndex:      make(map[string]string),
	}
}

// UpdatePeers replaces the tracked peer set from a fresh NetworkMap. New
// peers start in Direct mode. Peers whose reported endpoint changed are reset
// to Direct to give the new endpoint a chance; peers with an unchanged
// endpoint keep their current mode (so an active relay fallback survives
// routine NetworkMap churn).
func (m *Manager) UpdatePeers(peers []Peer) {
	m.mu.Lock()
	defer m.mu.Unlock()

	fresh := make(map[string]*peerState, len(peers))
	pubKeyIndex := make(map[string]string, len(peers))
	now := time.Now()
	for _, p := range peers {
		pubKeyIndex[p.PubKey] = p.NodeID
		if existing, ok := m.peers[p.NodeID]; ok && existing.endpoint == p.Endpoint {
			existing.pubKey = p.PubKey
			fresh[p.NodeID] = existing
			continue
		}
		fresh[p.NodeID] = &peerState{pubKey: p.PubKey, endpoint: p.Endpoint, mode: Direct, appliedAt: now}
	}
	m.peers = fresh
	m.pubKeyIndex = pubKeyIndex
}

// Tick re-evaluates each peer's mode against current handshake data (base64
// pubkey -> last handshake time, from wg.Manager.PeerHandshakes) and flips
// direct<->relay as needed, provisioning relay sockets via relayClient as
// required. Returns true if any peer's mode changed, meaning the caller
// should reapply peer configs via ApplyOverrides.
func (m *Manager) Tick(handshakes map[string]time.Time, relayClient *relay.Client) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	changed := false
	for nodeID, st := range m.peers {
		switch st.mode {
		case Direct:
			if now.Sub(st.appliedAt) < m.handshakeTimeout {
				continue
			}
			if hs, ok := handshakes[st.pubKey]; ok && hs.After(st.appliedAt) {
				continue // handshake succeeded since we applied the direct endpoint
			}
			if _, err := relayClient.RelayAddrFor(nodeID); err != nil {
				slog.Warn("pathsel: failed to provision relay socket", "node_id", nodeID, "err", err)
				continue
			}
			slog.Info("pathsel: direct handshake timed out, falling back to relay", "node_id", nodeID)
			st.mode = Relay
			st.appliedAt = now
			changed = true

		case Relay:
			if now.Sub(st.appliedAt) < m.retryInterval {
				continue
			}
			slog.Info("pathsel: retrying direct endpoint", "node_id", nodeID)
			st.mode = Direct
			st.appliedAt = now
			changed = true
		}
	}
	return changed
}

// ApplyOverrides returns a copy of peers with the Endpoint of any
// currently-relayed peer replaced by its local relay loopback address.
func (m *Manager) ApplyOverrides(peers []wg.PeerConfig, relayClient *relay.Client) []wg.PeerConfig {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]wg.PeerConfig, len(peers))
	copy(out, peers)
	for i, p := range out {
		nodeID, ok := m.pubKeyIndex[p.PublicKey]
		if !ok {
			continue
		}
		st, ok := m.peers[nodeID]
		if !ok || st.mode != Relay {
			continue
		}
		addr, err := relayClient.RelayAddrFor(nodeID)
		if err != nil {
			slog.Warn("pathsel: relay socket unavailable, leaving endpoint as-is", "node_id", nodeID, "err", err)
			continue
		}
		out[i].Endpoint = addr.String()
	}
	return out
}
