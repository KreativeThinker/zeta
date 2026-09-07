// Package relay lets an agent fall back to controller-mediated relaying of
// WireGuard packets for a peer when direct UDP connectivity isn't working.
//
// WireGuard's kernel peer Endpoint must be a UDP address, so relaying works
// by pointing that Endpoint at a local loopback socket instead of the peer's
// real address. Client shuttles datagrams between that loopback socket and
// the controller's Relay gRPC stream, keyed by peer node ID.
package relay

import (
	"fmt"
	"log/slog"
	"net"
	"sync"

	"github.com/kreativethinker/zeta/proto/zetapb"
)

// Client multiplexes multiple relayed peers over a single RelayFrame stream
// to the controller. It's safe to reuse across reconnects: call SetSend with
// a fresh channel and start a new Run goroutine each time the underlying
// stream is re-established; existing per-peer loopback sockets and their
// WireGuard-assigned endpoints stay valid across that swap.
type Client struct {
	// wgAddr is this agent's own WireGuard interface's single listening
	// socket (127.0.0.1:<listen port>). WireGuard multiplexes all peers over
	// that one socket, so relayed inbound packets are always delivered there
	// — regardless of whether this agent has itself decided to relay the
	// sending peer, and regardless of any traffic having gone out yet. That
	// matters: the peer on the other end may have switched to relay while
	// this agent is still trying direct, and the first packet through must
	// still get delivered for a handshake to ever complete.
	wgAddr *net.UDPAddr

	sendMu sync.RWMutex
	send   chan<- *zetapb.RelayFrame

	mu    sync.Mutex
	peers map[string]*peerRelay // nodeID -> relay state
}

type peerRelay struct {
	conn *net.UDPConn
}

// New creates a relay Client for an agent whose WireGuard interface listens
// on wgListenPort. Call SetSend once a Relay stream is open before relaying
// will do anything.
func New(wgListenPort int) *Client {
	return &Client{
		wgAddr: &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: wgListenPort},
		peers:  make(map[string]*peerRelay),
	}
}

// SetSend points outbound frames at a newly (re)established stream's send
// channel, or nil while disconnected.
func (c *Client) SetSend(send chan<- *zetapb.RelayFrame) {
	c.sendMu.Lock()
	c.send = send
	c.sendMu.Unlock()
}

// Run consumes inbound RelayFrames until recv is closed, delivering each
// straight to WireGuard's listening socket. Blocking; call in a goroutine per
// stream connection.
func (c *Client) Run(recv <-chan *zetapb.RelayFrame) {
	for frame := range recv {
		pr, err := c.getOrCreate(frame.FromNodeId)
		if err != nil {
			slog.Warn("relay: provisioning inbound socket", "peer", frame.FromNodeId, "err", err)
			continue
		}
		if _, err := pr.conn.WriteToUDP(frame.Payload, c.wgAddr); err != nil {
			slog.Debug("relay: writing to local wg socket", "peer", frame.FromNodeId, "err", err)
		}
	}
}

// RelayAddrFor returns the local loopback address WireGuard's peer Endpoint
// should be pointed at to relay traffic to peerNodeID via the controller.
// The underlying socket and forwarding goroutine are created lazily on first
// use (from here or from an inbound frame in Run) and reused afterward,
// including across stream reconnects.
func (c *Client) RelayAddrFor(peerNodeID string) (*net.UDPAddr, error) {
	pr, err := c.getOrCreate(peerNodeID)
	if err != nil {
		return nil, err
	}
	return pr.conn.LocalAddr().(*net.UDPAddr), nil
}

func (c *Client) getOrCreate(peerNodeID string) (*peerRelay, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if pr, ok := c.peers[peerNodeID]; ok {
		return pr, nil
	}

	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		return nil, fmt.Errorf("opening local relay socket for %s: %w", peerNodeID, err)
	}

	pr := &peerRelay{conn: conn}
	c.peers[peerNodeID] = pr

	go c.forward(peerNodeID, pr)

	return pr, nil
}

// forward reads WireGuard's outbound datagrams off the local loopback socket
// and relays them to peerNodeID via the current stream, if any.
func (c *Client) forward(peerNodeID string, pr *peerRelay) {
	buf := make([]byte, 65535)
	for {
		n, _, err := pr.conn.ReadFromUDP(buf)
		if err != nil {
			return // socket closed
		}

		payload := make([]byte, n)
		copy(payload, buf[:n])

		c.sendMu.RLock()
		send := c.send
		c.sendMu.RUnlock()
		if send == nil {
			continue // no relay stream currently connected
		}
		select {
		case send <- &zetapb.RelayFrame{ToNodeId: peerNodeID, Payload: payload}:
		default:
			slog.Debug("relay: dropping outbound frame, send channel full", "peer", peerNodeID)
		}
	}
}

// Close shuts down all per-peer loopback sockets.
func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, pr := range c.peers {
		pr.conn.Close()
	}
	c.peers = make(map[string]*peerRelay)
}
