package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/kreativethinker/zeta/agent/internal/agentapi"
	"github.com/kreativethinker/zeta/agent/internal/config"
	"github.com/kreativethinker/zeta/agent/internal/control"
	"github.com/kreativethinker/zeta/agent/internal/dns"
	"github.com/kreativethinker/zeta/agent/internal/dockerdiscovery"
	"github.com/kreativethinker/zeta/agent/internal/firewall"
	"github.com/kreativethinker/zeta/agent/internal/proxy"
	"github.com/kreativethinker/zeta/agent/internal/route"
	"github.com/kreativethinker/zeta/agent/internal/state"
	"github.com/kreativethinker/zeta/agent/internal/wg"
	"github.com/kreativethinker/zeta/proto/zetapb"
)

func main() {
	cfgPath := flag.String("config", "", "path to zeta-agent.yaml (optional)")
	zetafilePath := flag.String("zetafile", "", "path to zetafile (default: searches zetafile.yml, config.zeta, zetafile.conf)")
	preauthKey := flag.String("preauth-key", "", "enrollment token (required on first run)")
	coordAddr := flag.String("coordinator", "", "override coordinator address")
	flag.Parse()

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

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

	resolvedZetafilePath := config.ResolveZetafilePath(*zetafilePath)
	zf, err := config.LoadZetafile(resolvedZetafilePath)
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
	trustedProxies := append(cfg.Proxy.TrustedProxies, st.MeshIP)
	proxyMgr.SetTrustedProxies(trustedProxies)
	if err := proxyMgr.Start(cfg.Proxy.Addr); err != nil {
		slog.Error("starting proxy", "err", err)
		os.Exit(1)
	}
	defer proxyMgr.Stop()

	fwMgr := firewall.New(cfg.WireGuard.Interface, zf.Firewall.Backend)
	defer fwMgr.Flush()

	a := NewAgent(cfg, st, wgMgr, resolver, proxyMgr, fwMgr)
	a.SetZetafile(zf)

	agentHandler := agentapi.New(st, resolvedZetafilePath, proxyMgr, a.UpdateZetafile)
	go func() {
		slog.Info("agent UI listening", "addr", cfg.HTTP.Addr)
		if err := http.ListenAndServe(cfg.HTTP.Addr, agentHandler); err != nil {
			slog.Warn("agent UI stopped", "err", err)
		}
	}()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if dw, err := dockerdiscovery.New(); err != nil {
		slog.Info("docker discovery unavailable, skipping", "err", err)
	} else {
		if svcs, err := dw.Discover(ctx); err != nil {
			slog.Warn("initial docker service discovery failed", "err", err)
		} else {
			a.UpdateDockerServices(svcs)
		}
		go dw.Watch(ctx, a.UpdateDockerServices)
	}

	if err := config.WatchZetafile(ctx, resolvedZetafilePath, a.UpdateZetafile); err != nil {
		slog.Warn("zetafile hot-reload unavailable", "err", err)
	} else {
		slog.Info("watching zetafile for changes", "path", resolvedZetafilePath)
	}

	slog.Info("zeta agent up", "interface", cfg.WireGuard.Interface, "mesh_ip", st.MeshIP)

	a.Run(ctx, client)

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
