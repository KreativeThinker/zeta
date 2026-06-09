package control

import (
	"context"
	"fmt"

	"github.com/kreativethinker/zeta/proto/zetapb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

type Client struct {
	conn *grpc.ClientConn
	stub zetapb.CoordinatorServiceClient
}

func New(addr string, caPEM []byte) (*Client, error) {
	// Phase 2: plaintext. TLS upgrade happens when coordinator has a cert.
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("dialing coordinator %s: %w", addr, err)
	}
	return &Client{
		conn: conn,
		stub: zetapb.NewCoordinatorServiceClient(conn),
	}, nil
}

func (c *Client) GetServerKey(ctx context.Context) ([]byte, error) {
	resp, err := c.stub.GetServerKey(ctx, &zetapb.Empty{})
	if err != nil {
		return nil, err
	}
	return []byte(resp.PublicKey), nil
}

func (c *Client) Register(ctx context.Context, req *zetapb.RegisterRequest) (*zetapb.NodeConfig, error) {
	resp, err := c.stub.Register(ctx, req)
	if err != nil {
		return nil, err
	}
	switch r := resp.Result.(type) {
	case *zetapb.RegisterResponse_Config:
		return r.Config, nil
	case *zetapb.RegisterResponse_Pending:
		return nil, fmt.Errorf("enrollment pending: auth not yet approved")
	default:
		return nil, fmt.Errorf("unexpected register response type: %T", r)
	}
}

// OpenSync opens the bidirectional Sync stream. Returns send/recv channels.
// Both channels are closed when the stream ends.
func (c *Client) OpenSync(ctx context.Context, nodeID string) (
	send chan<- *zetapb.SyncUpdate,
	recv <-chan *zetapb.SyncResponse,
	err error,
) {
	md := metadata.Pairs("node-id", nodeID)
	streamCtx := metadata.NewOutgoingContext(ctx, md)

	stream, err := c.stub.Sync(streamCtx)
	if err != nil {
		return nil, nil, fmt.Errorf("opening sync stream: %w", err)
	}

	sendCh := make(chan *zetapb.SyncUpdate, 8)
	recvCh := make(chan *zetapb.SyncResponse, 8)

	// Sender: drain sendCh → stream.Send
	go func() {
		for msg := range sendCh {
			if err := stream.Send(msg); err != nil {
				return
			}
		}
		_ = stream.CloseSend()
	}()

	// Receiver: stream.Recv → recvCh
	go func() {
		defer close(recvCh)
		for {
			msg, err := stream.Recv()
			if err != nil {
				return
			}
			recvCh <- msg
		}
	}()

	return sendCh, recvCh, nil
}

func (c *Client) Close() error {
	return c.conn.Close()
}
