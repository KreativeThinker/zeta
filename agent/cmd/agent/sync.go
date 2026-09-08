package main

import (
	"log/slog"
	"strings"

	"github.com/kreativethinker/zeta/agent/internal/pathsel"
	"github.com/kreativethinker/zeta/agent/internal/wg"
	"github.com/kreativethinker/zeta/proto/zetapb"
)

func (a *Agent) handleSyncResponse(msg *zetapb.SyncResponse) {
	switch p := msg.Payload.(type) {
	case *zetapb.SyncResponse_NetworkMap:
		nm := p.NetworkMap

		selfHostname := a.st.Domain
		if idx := strings.Index(selfHostname, "."); idx != -1 {
			selfHostname = selfHostname[:idx]
		}

		wgPeers, pathPeers, hostToIP := buildPeerState(
			nm.Peers, a.st.NodeID, a.st.MeshIP, selfHostname,
		)

		a.pathMgr.UpdatePeers(pathPeers)
		a.applyPeers(wgPeers)
		if nm.Dns != nil {
			a.resolver.UpdateFromNetworkMap(nm.Peers, nm.Dns.MeshDomain)
		}

		// Update peer map and recompute firewall rules.
		a.hostToIP.Store(&hostToIP)
		a.applyFirewall()

		slog.Info("network map updated", "peers", len(wgPeers))

	case *zetapb.SyncResponse_Relay:
		slog.Debug("relay offer received (Phase 3 stub)")
	case *zetapb.SyncResponse_IceSignal:
		slog.Debug("ICE signal received (Phase 3 stub)")
	}
}

// buildPeerState turns a NetworkMap's peer list into WireGuard peer configs,
// pathsel peer descriptors, and the hostname→IP lookup used by the firewall.
// Pure — reads only its arguments, touches no Agent field.
func buildPeerState(peers []*zetapb.Peer, selfNodeID, selfMeshIP, selfHostname string) (
	wgPeers []wg.PeerConfig,
	pathPeers []pathsel.Peer,
	hostToIP map[string]string,
) {
	hostToIP = map[string]string{selfHostname: selfMeshIP}

	for _, peer := range peers {
		if peer.NodeId == selfNodeID {
			continue
		}
		hostToIP[peer.Hostname] = peer.MeshIp
		wgPeers = append(wgPeers, wg.PeerConfig{
			PublicKey:  peer.WgPublicKey,
			AllowedIPs: []string{peer.MeshIp + "/32"},
			Endpoint:   peer.Endpoint,
		})
		pathPeers = append(pathPeers, pathsel.Peer{
			NodeID:   peer.NodeId,
			PubKey:   peer.WgPublicKey,
			Endpoint: peer.Endpoint,
		})
	}
	return
}
