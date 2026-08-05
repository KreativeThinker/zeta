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
	sendMu sync.RWMutex
	send   chan<- *zetapb.RelayFrame

	mu    sync.Mutex
	peers map[string]*peerRelay // nodeID -> relay state
}

type peerRelay struct {
	conn *net.UDPConn

	mu         sync.Mutex
	remoteAddr *net.UDPAddr // last-seen WireGuard source addr on the loopback socket
}

// New creates a relay Client with no active stream. Call SetSend once a
// Relay stream is open before relaying will do anything.
func New() *Client {
	return &Client{peers: make(map[string]*peerRelay)}
}

// SetSend points outbound frames at a newly (re)established stream's send
// channel, or nil while disconnected.
func (c *Client) SetSend(send chan<- *zetapb.RelayFrame) {
	c.sendMu.Lock()
	c.send = send
	c.sendMu.Unlock()
}

// Run consumes inbound RelayFrames until recv is closed, dispatching each to
// the local loopback socket for its FromNodeId. Blocking; call in a goroutine
// per stream connection.
func (c *Client) Run(recv <-chan *zetapb.RelayFrame) {
	for frame := range recv {
		c.mu.Lock()
		pr, ok := c.peers[frame.FromNodeId]
		c.mu.Unlock()
		if !ok {
			continue
		}
		pr.mu.Lock()
		addr := pr.remoteAddr
		pr.mu.Unlock()
		if addr == nil {
			continue // WireGuard hasn't sent anything on this socket yet
		}
		if _, err := pr.conn.WriteToUDP(frame.Payload, addr); err != nil {
			slog.Debug("relay: writing to local wg socket", "peer", frame.FromNodeId, "err", err)
		}
	}
}

// RelayAddrFor returns the local loopback address WireGuard's peer Endpoint
// should be pointed at to relay traffic to peerNodeID via the controller.
// The underlying socket and forwarding goroutine are created lazily on first
// use and reused afterward, including across stream reconnects.
func (c *Client) RelayAddrFor(peerNodeID string) (*net.UDPAddr, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if pr, ok := c.peers[peerNodeID]; ok {
		return pr.conn.LocalAddr().(*net.UDPAddr), nil
	}

	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		return nil, fmt.Errorf("opening local relay socket for %s: %w", peerNodeID, err)
	}

	pr := &peerRelay{conn: conn}
	c.peers[peerNodeID] = pr

	go c.forward(peerNodeID, pr)

	return conn.LocalAddr().(*net.UDPAddr), nil
}

// forward reads WireGuard's outbound datagrams off the local loopback socket
// and relays them to peerNodeID via the current stream, if any.
func (c *Client) forward(peerNodeID string, pr *peerRelay) {
	buf := make([]byte, 65535)
	for {
		n, addr, err := pr.conn.ReadFromUDP(buf)
		if err != nil {
			return // socket closed
		}
		pr.mu.Lock()
		pr.remoteAddr = addr
		pr.mu.Unlock()

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
