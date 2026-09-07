package main

import (
	"log/slog"
	"strings"

	"github.com/kreativethinker/zeta/agent/internal/config"
	"github.com/kreativethinker/zeta/agent/internal/pathsel"
	"github.com/kreativethinker/zeta/agent/internal/proxy"
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

		wgPeers, pathPeers, ipToPK, ipToHost, hostToIP := buildPeerState(
			nm.Peers, a.st.NodeID, a.st.MeshIP, a.st.WGPublicKey, selfHostname,
		)

		a.pathMgr.UpdatePeers(pathPeers)
		a.applyPeers(wgPeers)
		if nm.Dns != nil {
			a.resolver.UpdateFromNetworkMap(nm.Peers, nm.Dns.MeshDomain)
		}

		if svcs := a.effectiveServices(); len(svcs) > 0 {
			a.proxyMgr.Sync(buildProxyServices(svcs, nm, a.st.NodeID), ipToPK, ipToHost)
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
// pathsel peer descriptors, and the IP/hostname lookup maps used by the
// resolver, proxy, and firewall. Pure — reads only its arguments, touches no
// Agent field.
func buildPeerState(peers []*zetapb.Peer, selfNodeID, selfMeshIP, selfPubKey, selfHostname string) (
	wgPeers []wg.PeerConfig,
	pathPeers []pathsel.Peer,
	ipToPK map[string]string,
	ipToHost map[string]string,
	hostToIP map[string]string,
) {
	ipToPK = map[string]string{selfMeshIP: selfPubKey}
	ipToHost = map[string]string{selfMeshIP: selfHostname}
	hostToIP = map[string]string{selfHostname: selfMeshIP}

	for _, peer := range peers {
		if peer.NodeId == selfNodeID {
			continue
		}
		ipToPK[peer.MeshIp] = peer.WgPublicKey
		ipToHost[peer.MeshIp] = peer.Hostname
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

// buildProxyServices maps the effective ZetaServices to proxy.ServiceConfig,
// pulling each service's resolved allowed pubkeys from the self-peer entry
// echoed back in the NetworkMap. Pure — reads only its arguments, touches no
// Agent field.
func buildProxyServices(svcs []config.ZetaService, nm *zetapb.NetworkMap, selfNodeID string) []proxy.ServiceConfig {
	svcPKs := make(map[string][]string)
	for _, peer := range nm.Peers {
		if peer.NodeId == selfNodeID {
			for _, svc := range peer.Services {
				svcPKs[svc.Name] = svc.AllowedPubkeys
			}
		}
	}
	var proxySvcs []proxy.ServiceConfig
	for _, s := range svcs {
		proxySvcs = append(proxySvcs, proxy.ServiceConfig{
			Name:       s.Name,
			TargetAddr: s.Target,
			AllowedPKs: svcPKs[s.Name],
		})
	}
	return proxySvcs
}
