package control

// Client maintains a persistent gRPC stream to the coordinator.
type Client struct{}

func New(addr string) (*Client, error) {
	// TODO: dial coordinator, perform Noise handshake, open Sync stream
	return &Client{}, nil
}

func (c *Client) Close() error { return nil }
