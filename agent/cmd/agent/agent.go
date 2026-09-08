package main

import (
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kreativethinker/zeta/agent/internal/config"
	"github.com/kreativethinker/zeta/agent/internal/dns"
	"github.com/kreativethinker/zeta/agent/internal/firewall"
	"github.com/kreativethinker/zeta/agent/internal/pathsel"
	"github.com/kreativethinker/zeta/agent/internal/proxy"
	"github.com/kreativethinker/zeta/agent/internal/relay"
	"github.com/kreativethinker/zeta/agent/internal/state"
	"github.com/kreativethinker/zeta/agent/internal/wg"
	"github.com/kreativethinker/zeta/proto/zetapb"
)

const (
	// pathHandshakeTimeout is how long a freshly-applied direct endpoint gets
	// before pathsel falls back to relaying that peer through the controller.
	pathHandshakeTimeout = 20 * time.Second
	// pathRetryInterval is how long a peer stays on relay before pathsel
	// tries the direct endpoint again.
	pathRetryInterval = 2 * time.Minute
)

// Agent owns one node's mesh state: WireGuard peers, DNS records, proxy
// routes, and firewall rules, all kept in sync with the controller's
// NetworkMap and the local zetafile/Docker-discovered services.
type Agent struct {
	cfg      *config.Config
	st       *state.State
	wgMgr    *wg.Manager
	resolver *dns.Resolver
	gateway  *proxy.Gateway
	fwMgr    *firewall.Manager // nil if nft unavailable

	zf         atomic.Pointer[config.Zetafile]
	dockerSvcs atomic.Pointer[[]config.ZetaService]
	hostToIP   atomic.Pointer[map[string]string]
	proxyPort  int

	relayClient *relay.Client
	pathMgr     *pathsel.Manager

	peersMu   sync.Mutex
	lastPeers []wg.PeerConfig // guarded by peersMu; direct (pre-override) peer configs from the last NetworkMap

	mu   sync.Mutex
	send chan<- *zetapb.SyncUpdate // guarded by mu; nil when disconnected
}

func NewAgent(cfg *config.Config, st *state.State, wgMgr *wg.Manager, resolver *dns.Resolver, gateway *proxy.Gateway, fwMgr *firewall.Manager) *Agent {
	_, portStr, _ := net.SplitHostPort(cfg.Proxy.Addr)
	proxyPort, _ := strconv.Atoi(portStr)
	return &Agent{
		cfg:         cfg,
		st:          st,
		wgMgr:       wgMgr,
		resolver:    resolver,
		gateway:     gateway,
		fwMgr:       fwMgr,
		proxyPort:   proxyPort,
		relayClient: relay.New(cfg.WireGuard.ListenPort),
		pathMgr:     pathsel.New(pathHandshakeTimeout, pathRetryInterval),
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
