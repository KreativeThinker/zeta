package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/kreativethinker/zeta/agent/internal/control"
	"github.com/kreativethinker/zeta/agent/internal/nat"
	"github.com/kreativethinker/zeta/proto/zetapb"
)

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

		relaySendCh, relayRecvCh, relayErr := client.OpenRelay(ctx, a.st.NodeID)
		if relayErr != nil {
			slog.Warn("opening relay stream", "err", relayErr)
		} else {
			a.relayClient.SetSend(relaySendCh)
		}

		relayDone := make(chan struct{})
		if relayRecvCh != nil {
			go func() {
				defer close(relayDone)
				a.relayClient.Run(relayRecvCh)
			}()
		} else {
			close(relayDone)
		}

		if zf := a.zf.Load(); zf != nil && len(a.effectiveServices()) > 0 {
			a.announceServices(sendCh)
		}

		stunDone := make(chan struct{})
		go a.runStunLoop(streamCtx, sendCh, stunDone)

		pingDone := make(chan struct{})
		go a.runPingLoop(streamCtx, sendCh, pingDone)

		pathDone := make(chan struct{})
		go a.runPathLoop(streamCtx, pathDone)

		teardown := func() {
			a.setSend(nil)
			a.relayClient.SetSend(nil)
			streamCancel()
			<-stunDone
			<-pingDone
			<-pathDone
			close(sendCh)
			if relaySendCh != nil {
				close(relaySendCh)
			}
			<-relayDone
		}

		for {
			select {
			case <-ctx.Done():
				teardown()
				return
			case msg, ok := <-recvCh:
				if !ok {
					teardown()
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

// runStunLoop periodically re-discovers this node's public endpoint via STUN
// and reports it to the controller, until ctx is canceled.
func (a *Agent) runStunLoop(ctx context.Context, sendCh chan<- *zetapb.SyncUpdate, done chan<- struct{}) {
	defer close(done)
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	a.reportEndpoint(sendCh)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.reportEndpoint(sendCh)
		}
	}
}

// runPingLoop sends a keepalive ping to the controller on an interval, until
// ctx is canceled.
func (a *Agent) runPingLoop(ctx context.Context, sendCh chan<- *zetapb.SyncUpdate, done chan<- struct{}) {
	defer close(done)
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			select {
			case sendCh <- &zetapb.SyncUpdate{Payload: &zetapb.SyncUpdate_Ping{Ping: &zetapb.PingUpdate{}}}:
			default:
			}
		}
	}
}

// runPathLoop periodically checks WireGuard handshake freshness and lets
// pathsel flip peers between direct and relay mode, until ctx is canceled.
func (a *Agent) runPathLoop(ctx context.Context, done chan<- struct{}) {
	defer close(done)
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			handshakes, err := a.wgMgr.PeerHandshakes()
			if err != nil {
				slog.Warn("reading peer handshakes", "err", err)
				continue
			}
			if a.pathMgr.Tick(handshakes, a.relayClient) {
				a.reapplyPeers()
			}
		}
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
