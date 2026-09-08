// Package proxy runs the mesh-facing gateway: a pure pass-through reverse
// proxy from zeta's mesh port to the local Caddy instance (caddy-docker-proxy),
// which does the actual per-service Host-header routing from container
// labels. Zeta does no routing, ACL, or dialing of its own here — Caddy binds
// private (mesh-only) sites to a loopback-only port that only this gateway
// can reach, and public sites directly on the public interface, bypassing
// this gateway entirely.
package proxy

import (
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
)

// Gateway forwards every request it receives to the local Caddy instance,
// Host header untouched, so Caddy's own per-service hostname labels match.
type Gateway struct {
	rp     *httputil.ReverseProxy
	server *http.Server
}

// New creates a Gateway that forwards to caddyAddr (host:port).
func New(caddyAddr string) *Gateway {
	target := &url.URL{Scheme: "http", Host: caddyAddr}
	return &Gateway{rp: httputil.NewSingleHostReverseProxy(target)}
}

func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.rp.ServeHTTP(w, r)
}

// Start begins listening on addr.
func (g *Gateway) Start(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	g.server = &http.Server{Handler: g}
	slog.Info("proxy gateway listening", "addr", addr)
	go g.server.Serve(ln) //nolint:errcheck
	return nil
}

// Stop shuts down the gateway listener.
func (g *Gateway) Stop() {
	if g.server != nil {
		g.server.Close()
	}
}
