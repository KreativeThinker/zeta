package main

import (
	"context"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kreativethinker/zeta/agent/internal/config"
	"github.com/kreativethinker/zeta/agent/internal/control"
	"github.com/kreativethinker/zeta/agent/internal/dns"
	"github.com/kreativethinker/zeta/agent/internal/firewall"
	"github.com/kreativethinker/zeta/agent/internal/nat"
	"github.com/kreativethinker/zeta/agent/internal/proxy"
	"github.com/kreativethinker/zeta/agent/internal/state"
	"github.com/kreativethinker/zeta/agent/internal/wg"
	"github.com/kreativethinker/zeta/proto/zetapb"
)

type Agent struct {
	cfg      *config.Config
	st       *state.State
	wgMgr    *wg.Manager
	resolver *dns.Resolver
	proxyMgr *proxy.Manager
	fwMgr    *firewall.Manager // nil if nft unavailable

	zf        atomic.Pointer[config.Zetafile]
	hostToIP  atomic.Pointer[map[string]string]
	proxyPort int

	mu   sync.Mutex
	send chan<- *zetapb.SyncUpdate // guarded by mu; nil when disconnected
}

func NewAgent(cfg *config.Config, st *state.State, wgMgr *wg.Manager, resolver *dns.Resolver, proxyMgr *proxy.Manager, fwMgr *firewall.Manager) *Agent {
	_, portStr, _ := net.SplitHostPort(cfg.Proxy.Addr)
	proxyPort, _ := strconv.Atoi(portStr)
	return &Agent{
		cfg:       cfg,
		st:        st,
		wgMgr:     wgMgr,
		resolver:  resolver,
		proxyMgr:  proxyMgr,
		fwMgr:     fwMgr,
		proxyPort: proxyPort,
	}
}

func (a *Agent) SetZetafile(zf *config.Zetafile) {
	a.zf.Store(zf)
}

func (a *Agent) UpdateZetafile(zf *config.Zetafile) {
	a.zf.Store(zf)
	a.mu.Lock()
	ch := a.send
	a.mu.Unlock()
	if ch != nil {
		a.announceServices(ch)
	}
	a.applyFirewall()
}

func (a *Agent) setSend(ch chan<- *zetapb.SyncUpdate) {
	a.mu.Lock()
	a.send = ch
	a.mu.Unlock()
}

func (a *Agent) announceServices(sendCh chan<- *zetapb.SyncUpdate) {
	zf := a.zf.Load()
	if zf == nil {
		return
	}
	decls := make([]*zetapb.ServiceDecl, 0, len(zf.Services))
	for _, s := range zf.Services {
		decls = append(decls, &zetapb.ServiceDecl{
			Name:             s.Name,
			TargetAddr:       s.Target,
			AllowedHostnames: s.Access,
		})
	}
	select {
	case sendCh <- &zetapb.SyncUpdate{
		Payload: &zetapb.SyncUpdate_Services{
			Services: &zetapb.ServiceAnnounce{Services: decls},
		},
	}:
	default:
	}
}

func (a *Agent) reportEndpoint(sendCh chan<- *zetapb.SyncUpdate) {
	endpoint, err := nat.DiscoverEndpoint(nat.DefaultSTUN, a.cfg.WireGuard.ListenPort)
	if err != nil {
		slog.Warn("STUN discovery failed", "err", err)
		return
	}
	select {
	case sendCh <- &zetapb.SyncUpdate{
		Payload: &zetapb.SyncUpdate_Endpoint{
			Endpoint: &zetapb.EndpointUpdate{Endpoint: endpoint},
		},
	}:
	default:
	}
}

func (a *Agent) applyFirewall() {
	if a.fwMgr == nil {
		return
	}
	zf := a.zf.Load()
	hostToIPPtr := a.hostToIP.Load()

	// Derive per-service allow rules from access lists + current peer IPs.
	var serviceRules []firewall.Rule
	if zf != nil && hostToIPPtr != nil {
		hostToIP := *hostToIPPtr
		for _, svc := range zf.Services {
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

func (a *Agent) handleSyncResponse(msg *zetapb.SyncResponse) {
	switch p := msg.Payload.(type) {
	case *zetapb.SyncResponse_NetworkMap:
		nm := p.NetworkMap
		zf := a.zf.Load()

		selfHostname := a.st.Domain
		if idx := strings.Index(selfHostname, "."); idx != -1 {
			selfHostname = selfHostname[:idx]
		}
		ipToPK := map[string]string{a.st.MeshIP: a.st.WGPublicKey}
		ipToHost := map[string]string{a.st.MeshIP: selfHostname}
		hostToIP := map[string]string{selfHostname: a.st.MeshIP}

		var peers []wg.PeerConfig
		for _, peer := range nm.Peers {
			if peer.NodeId == a.st.NodeID {
				continue
			}
			ipToPK[peer.MeshIp] = peer.WgPublicKey
			ipToHost[peer.MeshIp] = peer.Hostname
			hostToIP[peer.Hostname] = peer.MeshIp
			peers = append(peers, wg.PeerConfig{
				PublicKey:  peer.WgPublicKey,
				AllowedIPs: []string{peer.MeshIp + "/32"},
				Endpoint:   peer.Endpoint,
			})
		}

		if err := a.wgMgr.ApplyPeers(peers); err != nil {
			slog.Error("applying WireGuard peers", "err", err)
		}
		if nm.Dns != nil {
			a.resolver.UpdateFromNetworkMap(nm.Peers, nm.Dns.MeshDomain)
		}

		if zf != nil && len(zf.Services) > 0 {
			svcPKs := make(map[string][]string)
			for _, peer := range nm.Peers {
				if peer.NodeId == a.st.NodeID {
					for _, svc := range peer.Services {
						svcPKs[svc.Name] = svc.AllowedPubkeys
					}
				}
			}
			var proxySvcs []proxy.ServiceConfig
			for _, s := range zf.Services {
				proxySvcs = append(proxySvcs, proxy.ServiceConfig{
					Name:       s.Name,
					TargetAddr: s.Target,
					AllowedPKs: svcPKs[s.Name],
				})
			}
			a.proxyMgr.Sync(proxySvcs, ipToPK, ipToHost)
		}

		// Update peer map and recompute firewall rules.
		a.hostToIP.Store(&hostToIP)
		a.applyFirewall()

		slog.Info("network map updated", "peers", len(peers))

	case *zetapb.SyncResponse_Relay:
		slog.Debug("relay offer received (Phase 3 stub)")
	case *zetapb.SyncResponse_IceSignal:
		slog.Debug("ICE signal received (Phase 3 stub)")
	}
}

func (a *Agent) Run(ctx context.Context, client *control.Client) {
	const maxBackoff = 60 * time.Second
	backoff := time.Second

	for {
		sendCh, recvCh, err := client.OpenSync(ctx, a.st.NodeID)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Warn("opening sync stream", "err", err, "retry_in", backoff)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff = min(backoff*2, maxBackoff)
			continue
		}
		backoff = time.Second

		streamCtx, streamCancel := context.WithCancel(ctx)
		a.setSend(sendCh)

		if zf := a.zf.Load(); zf != nil && len(zf.Services) > 0 {
			a.announceServices(sendCh)
		}

		stunDone := make(chan struct{})
		go func() {
			defer close(stunDone)
			ticker := time.NewTicker(30 * time.Second)
			defer ticker.Stop()
			a.reportEndpoint(sendCh)
			for {
				select {
				case <-streamCtx.Done():
					return
				case <-ticker.C:
					a.reportEndpoint(sendCh)
				}
			}
		}()

		pingDone := make(chan struct{})
		go func() {
			defer close(pingDone)
			ticker := time.NewTicker(30 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-streamCtx.Done():
					return
				case <-ticker.C:
					select {
					case sendCh <- &zetapb.SyncUpdate{Payload: &zetapb.SyncUpdate_Ping{Ping: &zetapb.PingUpdate{}}}:
					default:
					}
				}
			}
		}()

		for {
			select {
			case <-ctx.Done():
				a.setSend(nil)
				streamCancel()
				<-stunDone
				<-pingDone
				close(sendCh)
				return
			case msg, ok := <-recvCh:
				if !ok {
					a.setSend(nil)
					streamCancel()
					<-stunDone
					<-pingDone
					close(sendCh)
					goto reconnect
				}
				a.handleSyncResponse(msg)
			}
		}

	reconnect:
		if ctx.Err() != nil {
			return
		}
		slog.Warn("sync stream disconnected, reconnecting", "retry_in", backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, maxBackoff)
	}
}
