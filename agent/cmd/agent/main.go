package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"net/http"
	"strings"

	"github.com/kreativethinker/zeta/agent/internal/agentapi"
	"github.com/kreativethinker/zeta/agent/internal/config"
	"github.com/kreativethinker/zeta/agent/internal/control"
	"github.com/kreativethinker/zeta/agent/internal/dns"
	"github.com/kreativethinker/zeta/agent/internal/nat"
	"github.com/kreativethinker/zeta/agent/internal/proxy"
	"github.com/kreativethinker/zeta/agent/internal/route"
	"github.com/kreativethinker/zeta/agent/internal/state"
	"github.com/kreativethinker/zeta/agent/internal/wg"
	"github.com/kreativethinker/zeta/proto/zetapb"
)

func main() {
	cfgPath := flag.String("config", "", "path to zeta-agent.yaml (optional)")
	zetafilePath := flag.String("zetafile", "", "path to zetafile.yml (default: ./zetafile.yml)")
	preauthKey := flag.String("preauth-key", "", "enrollment token (required on first run)")
	coordAddr := flag.String("coordinator", "", "override coordinator address")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		slog.Error("loading config", "err", err)
		os.Exit(1)
	}
	if *coordAddr != "" {
		cfg.Coordinator.Addr = *coordAddr
	}

	st, err := state.Load(cfg.State.Path)
	if err != nil {
		slog.Error("loading state", "err", err)
		os.Exit(1)
	}

	client, err := control.New(cfg.Coordinator.Addr, nil)
	if err != nil {
		slog.Error("connecting to coordinator", "err", err)
		os.Exit(1)
	}
	defer client.Close()

	if st == nil {
		if *preauthKey == "" {
			slog.Error("not enrolled and --preauth-key not provided")
			os.Exit(1)
		}
		slog.Info("enrolling with coordinator", "addr", cfg.Coordinator.Addr)
		st, err = enroll(client, *preauthKey)
		if err != nil {
			slog.Error("enrollment failed", "err", err)
			os.Exit(1)
		}
		if err := state.Save(cfg.State.Path, st); err != nil {
			slog.Error("saving state", "err", err)
			os.Exit(1)
		}
		slog.Info("enrolled", "node_id", st.NodeID, "mesh_ip", st.MeshIP)
	} else {
		slog.Info("loaded existing state", "node_id", st.NodeID, "mesh_ip", st.MeshIP)
	}

	wgMgr, err := wg.New(cfg.WireGuard.Interface)
	if err != nil {
		slog.Error("creating WireGuard manager", "err", err)
		os.Exit(1)
	}
	defer wgMgr.Close()

	if err := wgMgr.EnsureInterface(); err != nil {
		slog.Error("ensuring WireGuard interface", "err", err)
		os.Exit(1)
	}
	if err := wgMgr.Configure(st.WGPrivateKey, cfg.WireGuard.ListenPort); err != nil {
		slog.Error("configuring WireGuard", "err", err)
		os.Exit(1)
	}
	if err := wgMgr.AssignAddress(st.MeshIP, "100.64.0.0/10"); err != nil {
		slog.Error("assigning mesh address", "err", err)
		os.Exit(1)
	}
	if err := route.AddMeshRoute("100.64.0.0/10", cfg.WireGuard.Interface); err != nil {
		slog.Warn("adding mesh route", "err", err)
	}

	zf, err := config.LoadZetafile(*zetafilePath)
	if err != nil {
		slog.Warn("loading zetafile", "err", err)
		zf = &config.Zetafile{}
	}

	resolver := dns.New(cfg.DNS.ListenAddr, cfg.DNS.Upstream)
	if err := resolver.Start(); err != nil {
		slog.Warn("starting DNS resolver", "err", err)
	} else {
		teardownDNS, err := dns.SetupSystemDNS(cfg.DNS.ListenAddr, cfg.WireGuard.Interface, "mesh")
		if err != nil {
			slog.Warn("configuring system DNS", "err", err)
		}
		defer teardownDNS()
	}
	defer resolver.Stop()

	proxyMgr := proxy.New()
	// Always trust the local mesh IP as a proxy source so that a reverse proxy
	// like Caddy running on the same host (connecting via the mesh IP) has its
	// X-Forwarded-For header honoured to identify the real mesh client.
	trustedProxies := append(cfg.Proxy.TrustedProxies, st.MeshIP)
	proxyMgr.SetTrustedProxies(trustedProxies)
	if err := proxyMgr.Start(cfg.Proxy.Addr); err != nil {
		slog.Error("starting proxy", "err", err)
		os.Exit(1)
	}
	defer proxyMgr.Stop()

	// Agent HTTP UI — sendCh is not yet open here; announceServices is called
	// inside runSyncLoop. We pass a callback that gets wired once the stream opens.
	var sendChRef chan<- *zetapb.SyncUpdate
	agentHandler := agentapi.New(st, *zetafilePath, proxyMgr, func(updated *config.Zetafile) {
		zf = updated
		if sendChRef != nil {
			announceServices(sendChRef, updated)
		}
	})
	go func() {
		slog.Info("agent UI listening", "addr", cfg.HTTP.Addr)
		if err := http.ListenAndServe(cfg.HTTP.Addr, agentHandler); err != nil {
			slog.Warn("agent UI stopped", "err", err)
		}
	}()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	slog.Info("zeta agent up", "interface", cfg.WireGuard.Interface, "mesh_ip", st.MeshIP)

	runSyncLoop(ctx, client, st, wgMgr, resolver, proxyMgr, zf, cfg, &sendChRef)

	if err := wgMgr.ApplyPeers(nil); err != nil {
		slog.Warn("clearing WireGuard peers on exit", "err", err)
	}
	if err := route.RemoveMeshRoute("100.64.0.0/10", cfg.WireGuard.Interface); err != nil {
		slog.Warn("removing mesh route on exit", "err", err)
	}
}

func enroll(client *control.Client, preauthKey string) (*state.State, error) {
	privKey, pubKey, err := state.GenerateKeyPair()
	if err != nil {
		return nil, fmt.Errorf("generating keypair: %w", err)
	}

	hostname, _ := os.Hostname()
	cfg, err := client.Register(context.Background(), &zetapb.RegisterRequest{
		WgPublicKey:  pubKey,
		Hostname:     hostname,
		Os:           "linux",
		PreauthKey:   preauthKey,
		AgentVersion: "dev",
	})
	if err != nil {
		return nil, err
	}

	return &state.State{
		NodeID:       cfg.NodeId,
		MeshIP:       cfg.MeshIp,
		Domain:       cfg.Domain,
		WGPrivateKey: privKey,
		WGPublicKey:  pubKey,
		CertPEM:      string(cfg.CertPem),
		CertKeyPEM:   string(cfg.KeyPem),
		CAPEM:        string(cfg.CaPem),
	}, nil
}

func runSyncLoop(
	ctx context.Context,
	client *control.Client,
	st *state.State,
	wgMgr *wg.Manager,
	resolver *dns.Resolver,
	proxyMgr *proxy.Manager,
	zf *config.Zetafile,
	cfg *config.Config,
	sendChPtr *chan<- *zetapb.SyncUpdate,
) {
	backoff := time.Second
	const maxBackoff = 60 * time.Second

	for {
		sendCh, recvCh, err := client.OpenSync(ctx, st.NodeID)
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

		// streamCtx is cancelled when this stream attempt ends (disconnect or shutdown).
		// It stops the per-stream goroutines before we close sendCh.
		streamCtx, streamCancel := context.WithCancel(ctx)

		// Expose sendCh to the agent API callback so UI-triggered changes can re-announce.
		*sendChPtr = sendCh

		// Announce services declared in zetafile.yml immediately on connect.
		if len(zf.Services) > 0 {
			announceServices(sendCh, zf)
		}

		// STUN loop: discover external endpoint every 30s and report to coordinator.
		stunDone := make(chan struct{})
		go func() {
			defer close(stunDone)
			ticker := time.NewTicker(30 * time.Second)
			defer ticker.Stop()
			reportEndpoint(sendCh, cfg)
			for {
				select {
				case <-streamCtx.Done():
					return
				case <-ticker.C:
					reportEndpoint(sendCh, cfg)
				}
			}
		}()

		// Ping loop: keepalive every 30s.
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
				*sendChPtr = nil
				streamCancel()
				<-stunDone
				<-pingDone
				close(sendCh)
				return
			case msg, ok := <-recvCh:
				if !ok {
					// Stream disconnected — stop goroutines, then reconnect.
					*sendChPtr = nil
					streamCancel()
					<-stunDone
					<-pingDone
					close(sendCh)
					goto reconnect
				}
				handleSyncResponse(msg, st, wgMgr, resolver, proxyMgr, zf, cfg)
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

func announceServices(sendCh chan<- *zetapb.SyncUpdate, zf *config.Zetafile) {
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

func reportEndpoint(sendCh chan<- *zetapb.SyncUpdate, cfg *config.Config) {
	endpoint, err := nat.DiscoverEndpoint(nat.DefaultSTUN, cfg.WireGuard.ListenPort)
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

func handleSyncResponse(
	msg *zetapb.SyncResponse,
	st *state.State,
	wgMgr *wg.Manager,
	resolver *dns.Resolver,
	proxyMgr *proxy.Manager,
	zf *config.Zetafile,
	cfg *config.Config,
) {
	switch p := msg.Payload.(type) {
	case *zetapb.SyncResponse_NetworkMap:
		nm := p.NetworkMap

		// Build mesh-IP → pubkey and mesh-IP → hostname maps for proxy ACL/logging.
		// Include self so the proxy can identify connections from the local mesh IP.
		selfHostname := st.Domain
		if idx := strings.Index(selfHostname, "."); idx != -1 {
			selfHostname = selfHostname[:idx]
		}
		ipToPK := map[string]string{st.MeshIP: st.WGPublicKey}
		ipToHost := map[string]string{st.MeshIP: selfHostname}
		var peers []wg.PeerConfig
		for _, peer := range nm.Peers {
			if peer.NodeId == st.NodeID {
				continue
			}
			ipToPK[peer.MeshIp] = peer.WgPublicKey
			ipToHost[peer.MeshIp] = peer.Hostname
			peers = append(peers, wg.PeerConfig{
				PublicKey:  peer.WgPublicKey,
				AllowedIPs: []string{peer.MeshIp + "/32"},
				Endpoint:   peer.Endpoint,
			})
		}

		if err := wgMgr.ApplyPeers(peers); err != nil {
			slog.Error("applying WireGuard peers", "err", err)
		}

		if nm.Dns != nil {
			resolver.UpdateFromNetworkMap(nm.Peers, nm.Dns.MeshDomain)
		}

		// Reconcile proxy listeners for services this agent hosts (from zetafile).
		// allowed_pubkeys come from the NetworkMap (resolved by coordinator).
		if len(zf.Services) > 0 {
			// Build a name→allowedPKs index from the NetworkMap (our own peer entry).
			svcPKs := make(map[string][]string)
			for _, peer := range nm.Peers {
				if peer.NodeId == st.NodeID {
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
			proxyMgr.Sync(proxySvcs, ipToPK, ipToHost)
		}

		slog.Info("network map updated", "peers", len(peers))

	case *zetapb.SyncResponse_Relay:
		slog.Debug("relay offer received (Phase 3 stub)")
	case *zetapb.SyncResponse_IceSignal:
		slog.Debug("ICE signal received (Phase 3 stub)")
	}
}

func min(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
