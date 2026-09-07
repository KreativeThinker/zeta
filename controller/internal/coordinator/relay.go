package coordinator

import (
	"log/slog"

	"github.com/kreativethinker/zeta/proto/zetapb"
)

// RegisterRelayStream adds a node's relay send channel.
func (c *Coordinator) RegisterRelayStream(nodeID string, ch chan *zetapb.RelayFrame) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.relayStreams[nodeID] = ch
}

// UnregisterRelayStream removes a node's relay send channel.
func (c *Coordinator) UnregisterRelayStream(nodeID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.relayStreams, nodeID)
}

// HandleRelayFrame forwards an opaque WireGuard packet from one node to
// another's relay stream, by node ID. The payload is never inspected.
func (c *Coordinator) HandleRelayFrame(fromNodeID string, frame *zetapb.RelayFrame) {
	c.mu.RLock()
	ch, ok := c.relayStreams[frame.ToNodeId]
	c.mu.RUnlock()
	if !ok {
		return
	}
	msg := &zetapb.RelayFrame{FromNodeId: fromNodeID, Payload: frame.Payload}
	select {
	case ch <- msg:
	default:
		slog.Debug("dropping relay frame (channel full)", "to_node_id", frame.ToNodeId)
	}
}
