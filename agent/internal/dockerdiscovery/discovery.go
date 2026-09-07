// Package dockerdiscovery turns Docker Compose service labels into
// zeta mesh services, so a container can declare itself without a matching
// zetafile entry.
//
// Talks to the Docker Engine API directly over its unix socket via plain
// net/http+encoding/json — the full docker/docker client SDK pulls in a huge
// transitive dependency tree (opentelemetry, grpc-gateway, ...) for what is,
// underneath, a small JSON-over-HTTP API.
//
// Label schema (on the container, e.g. via `labels:` in compose.yml):
//
//	zeta.service.name    (required) mesh service name
//	zeta.service.port    (required) container port to proxy to
//	zeta.service.access  (optional) comma-separated access list, same format
//	                      as zetafile `access:` entries; defaults to "*"
//	                      (any enrolled mesh peer) since per-user ACLs are a
//	                      later phase.
package dockerdiscovery

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/kreativethinker/zeta/agent/internal/config"
)

const (
	labelName   = "zeta.service.name"
	labelPort   = "zeta.service.port"
	labelAccess = "zeta.service.access"

	socketPath = "/var/run/docker.sock"
	debounce   = 500 * time.Millisecond
)

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
	ID              string            `json:"Id"`
	Labels          map[string]string `json:"Labels"`
	NetworkSettings struct {
		Networks map[string]struct {
			IPAddress string `json:"IPAddress"`
		} `json:"Networks"`
	} `json:"NetworkSettings"`
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

// parseContainer extracts a ZetaService from a container's zeta.service.*
// labels. Returns ok=false if the container doesn't declare a mesh service.
func parseContainer(c containerSummary) (config.ZetaService, bool) {
	name := c.Labels[labelName]
	portStr := c.Labels[labelPort]
	if name == "" || portStr == "" {
		return config.ZetaService{}, false
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		slog.Warn("dockerdiscovery: invalid port label", "container", shortID(c.ID), "port", portStr)
		return config.ZetaService{}, false
	}
	ip := containerIP(c)
	if ip == "" {
		slog.Warn("dockerdiscovery: no network IP for container", "container", shortID(c.ID))
		return config.ZetaService{}, false
	}

	access := []string{"*"}
	if raw := c.Labels[labelAccess]; raw != "" {
		access = strings.Split(raw, ",")
		for i := range access {
			access[i] = strings.TrimSpace(access[i])
		}
	}

	return config.ZetaService{
		Name:   name,
		Target: net.JoinHostPort(ip, strconv.Itoa(port)),
		Access: access,
	}, true
}

func containerIP(c containerSummary) string {
	for _, n := range c.NetworkSettings.Networks {
		if n.IPAddress != "" {
			return n.IPAddress
		}
	}
	return ""
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// Watch re-runs Discover on every container start/stop/die event (debounced)
// and reports the resulting service list to onChange. Blocks until ctx is
// canceled.
func (w *Watcher) Watch(ctx context.Context, onChange func([]config.ZetaService)) {
	for {
		if err := w.streamEvents(ctx, onChange); err != nil && !errors.Is(err, context.Canceled) {
			slog.Warn("dockerdiscovery: event stream error, retrying", "err", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
			continue
		}
		if ctx.Err() != nil {
			return
		}
	}
}

func (w *Watcher) streamEvents(ctx context.Context, onChange func([]config.ZetaService)) error {
	q := "type=container&filters=" + `{"event":["start","stop","die"]}`
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://unix/events?"+q, nil)
	if err != nil {
		return err
	}
	resp, err := w.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("docker /events: status %d", resp.StatusCode)
	}

	var timer *time.Timer
	fire := func() {
		svcs, err := w.Discover(ctx)
		if err != nil {
			slog.Warn("dockerdiscovery: re-discovery failed", "err", err)
			return
		}
		onChange(svcs)
	}

	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		if timer != nil {
			timer.Stop()
		}
		timer = time.AfterFunc(debounce, fire)
	}
	if timer != nil {
		timer.Stop()
	}
	return sc.Err()
}
