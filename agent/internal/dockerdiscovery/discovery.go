// Package dockerdiscovery turns Docker Compose service labels into zeta mesh
// services. Zeta itself no longer routes to containers directly — that's
// Caddy's job (caddy-docker-proxy, reading the same container labels). This
// package only extracts what zeta needs for mesh DNS registration and the
// (future) access-control service.
//
// Talks to the Docker Engine API directly over its unix socket via plain
// net/http+encoding/json — the full docker/docker client SDK pulls in a huge
// transitive dependency tree (opentelemetry, grpc-gateway, ...) for what is,
// underneath, a small JSON-over-HTTP API.
//
// Label schema (on the container, e.g. via `labels:` in compose.yml):
//
//	caddy         (required) full hostname Caddy routes this service on,
//	              e.g. "myservice.shire.mesh" (private/mesh, default) or
//	              "myservice.example.com" (public, opt-in below). The mesh
//	              service name registered with zeta is the first label of
//	              this hostname.
//	zeta.public   (optional) "true" to bind this site on Caddy's public
//	              interface instead of its private/mesh-only one. Defaults
//	              to private. Public services are never registered with
//	              zeta's mesh DNS — Caddy routes them independently.
//	zeta.access   (optional) comma-separated access list, same format as
//	              before; defaults to "*" (any enrolled mesh peer) since
//	              per-user ACLs are a later phase. Ignored for public
//	              services.
package dockerdiscovery

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"

	"github.com/kreativethinker/zeta/agent/internal/config"
)

const socketPath = "/var/run/docker.sock"

type Watcher struct {
	hc *http.Client
}

// New checks that the Docker daemon socket is reachable. Returns an error
// otherwise (e.g. non-Docker host) — callers should treat that as
// non-fatal and simply skip Docker-based discovery.
func New() (*Watcher, error) {
	w := &Watcher{
		hc: &http.Client{
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
				},
			},
		},
	}
	resp, err := w.hc.Get("http://unix/_ping")
	if err != nil {
		return nil, err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("docker daemon ping: unexpected status %d", resp.StatusCode)
	}
	return w, nil
}

type containerSummary struct {
	ID     string            `json:"Id"`
	Labels map[string]string `json:"Labels"`
}

// Discover lists running containers and returns a ZetaService for every one
// carrying the zeta.service.* labels.
func (w *Watcher) Discover(ctx context.Context) ([]config.ZetaService, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://unix/containers/json", nil)
	if err != nil {
		return nil, err
	}
	resp, err := w.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("docker /containers/json: status %d", resp.StatusCode)
	}

	var containers []containerSummary
	if err := json.NewDecoder(resp.Body).Decode(&containers); err != nil {
		return nil, err
	}

	var svcs []config.ZetaService
	for _, c := range containers {
		if svc, ok := parseContainer(c); ok {
			svcs = append(svcs, svc)
		}
	}
	return svcs, nil
}
