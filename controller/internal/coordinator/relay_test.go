package coordinator

import (
	"testing"

	"github.com/kreativethinker/zeta/proto/zetapb"
)

func TestHandleRelayFrame(t *testing.T) {
	c := &Coordinator{relayStreams: make(map[string]chan *zetapb.RelayFrame)}

	chA := make(chan *zetapb.RelayFrame, 1)
	chB := make(chan *zetapb.RelayFrame, 1)
	c.RegisterRelayStream("nodeA", chA)
	c.RegisterRelayStream("nodeB", chB)

	c.HandleRelayFrame("nodeA", &zetapb.RelayFrame{ToNodeId: "nodeB", Payload: []byte("hello")})

	select {
	case frame := <-chB:
		if frame.FromNodeId != "nodeA" {
			t.Errorf("FromNodeId = %q, want nodeA", frame.FromNodeId)
		}
		if string(frame.Payload) != "hello" {
			t.Errorf("Payload = %q, want hello", frame.Payload)
		}
	default:
		t.Fatal("expected frame delivered to nodeB's channel")
	}

	select {
	case <-chA:
		t.Fatal("nodeA should not have received its own frame")
	default:
	}
}

func TestHandleRelayFrameUnknownDestination(t *testing.T) {
	c := &Coordinator{relayStreams: make(map[string]chan *zetapb.RelayFrame)}

	// Must not panic or block when the destination isn't connected.
	c.HandleRelayFrame("nodeA", &zetapb.RelayFrame{ToNodeId: "unknown", Payload: []byte("x")})
}

func TestUnregisterRelayStream(t *testing.T) {
	c := &Coordinator{relayStreams: make(map[string]chan *zetapb.RelayFrame)}

	chB := make(chan *zetapb.RelayFrame, 1)
	c.RegisterRelayStream("nodeB", chB)
	c.UnregisterRelayStream("nodeB")

	c.HandleRelayFrame("nodeA", &zetapb.RelayFrame{ToNodeId: "nodeB", Payload: []byte("late")})

	select {
	case <-chB:
		t.Fatal("unregistered stream should not receive frames")
	default:
	}
}
