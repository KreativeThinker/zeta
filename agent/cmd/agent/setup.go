package main

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/kreativethinker/zeta/agent/internal/config"
	"github.com/kreativethinker/zeta/agent/internal/dns"
	"github.com/kreativethinker/zeta/agent/internal/dockerdiscovery"
	"github.com/kreativethinker/zeta/agent/internal/firewall"
	"github.com/kreativethinker/zeta/agent/internal/proxy"
	"github.com/kreativethinker/zeta/agent/internal/route"
	"github.com/kreativethinker/zeta/agent/internal/state"
	"github.com/kreativethinker/zeta/agent/internal/wg"
)

// setupWireGuard creates the WireGuard interface, configures it with this
// node's key/mesh IP, and adds the mesh route.
func setupWireGuard(cfg *config.Config, st *state.State) (*wg.Manager, error) {
	wgMgr, err := wg.New(cfg.WireGuard.Interface)
	if err != nil {
		return nil, fmt.Errorf("creating WireGuard manager: %w", err)
	}
	if err := wgMgr.EnsureInterface(); err != nil {
		return nil, fmt.Errorf("ensuring WireGuard interface: %w", err)
	}
	if err := wgMgr.Configure(st.WGPrivateKey, cfg.WireGuard.ListenPort); err != nil {
		return nil, fmt.Errorf("configuring WireGuard: %w", err)
	}
	if err := wgMgr.AssignAddress(st.MeshIP, "100.64.0.0/10"); err != nil {
		return nil, fmt.Errorf("assigning mesh address: %w", err)
	}
	if err := route.AddMeshRoute("100.64.0.0/10", cfg.WireGuard.Interface); err != nil {
		slog.Warn("adding mesh route", "err", err)
	}
	return wgMgr, nil
}

// setupResolver starts the DNS resolver and, if possible, points the system
// resolver at it. Returns a cleanup func safe to defer unconditionally.
func setupResolver(cfg *config.Config) (*dns.Resolver, func()) {
	resolver := dns.New(cfg.DNS.ListenAddr, cfg.DNS.Upstream)
	var teardownDNS func()
	if err := resolver.Start(); err != nil {
		slog.Warn("starting DNS resolver", "err", err)
	} else {
		td, err := dns.SetupSystemDNS(cfg.DNS.ListenAddr, cfg.WireGuard.Interface, "mesh")
		if err != nil {
			slog.Warn("configuring system DNS", "err", err)
		}
		teardownDNS = td
	}
	return resolver, func() {
		resolver.Stop()
		if teardownDNS != nil {
			teardownDNS()
		}
	}
}

// setupProxy starts the mesh-facing gateway that forwards to the local
// Caddy instance (caddy-docker-proxy), which does the actual per-service
// routing from container labels.
func setupProxy(cfg *config.Config) (*proxy.Gateway, error) {
	gateway := proxy.New(cfg.Proxy.CaddyAddr)
	if err := gateway.Start(cfg.Proxy.Addr); err != nil {
		return nil, fmt.Errorf("starting proxy gateway: %w", err)
	}
	return gateway, nil
}

// setupFirewall selects and initializes the firewall backend configured in
// the zetafile (auto-detected if unset).
func setupFirewall(cfg *config.Config, zf *config.Zetafile) *firewall.Manager {
	return firewall.New(cfg.WireGuard.Interface, zf.Firewall.Backend)
}

// setupDockerDiscovery starts Docker Compose label service discovery in the
// background, if a Docker daemon is reachable. Non-fatal if not — most
// agents don't run on a Docker host.
func setupDockerDiscovery(ctx context.Context, a *Agent) {
	dw, err := dockerdiscovery.New()
	if err != nil {
		slog.Info("docker discovery unavailable, skipping", "err", err)
		return
	}
	if svcs, err := dw.Discover(ctx); err != nil {
		slog.Warn("initial docker service discovery failed", "err", err)
	} else {
		a.UpdateDockerServices(svcs)
	}
	go dw.Watch(ctx, a.UpdateDockerServices)
}
