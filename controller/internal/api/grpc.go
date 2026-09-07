package api

import (
	"context"
	"io"
	"log/slog"
	"net"

	"github.com/kreativethinker/zeta/controller/internal/coordinator"
	"github.com/kreativethinker/zeta/proto/zetapb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
)

// GRPCServer wraps grpc.Server and implements CoordinatorServiceServer.
type GRPCServer struct {
	zetapb.UnimplementedCoordinatorServiceServer
	coord *coordinator.Coordinator
	caPEM []byte
	srv   *grpc.Server
}

// NewGRPCServer creates a gRPC server. If certPEM/keyPEM are non-nil the server
// uses TLS; otherwise it runs in plaintext (dev mode).
func NewGRPCServer(coord *coordinator.Coordinator, caPEM, certPEM, keyPEM []byte) (*GRPCServer, error) {
	var opts []grpc.ServerOption

	if len(certPEM) > 0 && len(keyPEM) > 0 {
		creds, err := credentials.NewServerTLSFromFile("", "") // won't be called
		_ = creds
		// Use in-memory TLS config instead.
		tlsCfg, err := tlsConfigFromPEM(certPEM, keyPEM)
		if err != nil {
			return nil, err
		}
		opts = append(opts, grpc.Creds(credentials.NewTLS(tlsCfg)))
	}

	gs := grpc.NewServer(opts...)
	s := &GRPCServer{coord: coord, caPEM: caPEM, srv: gs}
	zetapb.RegisterCoordinatorServiceServer(gs, s)
	reflection.Register(gs)
	return s, nil
}

// Serve starts listening on addr. Blocks until the server stops.
func (s *GRPCServer) Serve(addr string) error {
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	slog.Info("gRPC server listening", "addr", addr)
	return s.srv.Serve(lis)
}

// GracefulStop initiates a graceful shutdown.
func (s *GRPCServer) GracefulStop() {
	s.srv.GracefulStop()
}

// ── RPC implementations ───────────────────────────────────────────────────────

func (s *GRPCServer) GetServerKey(_ context.Context, _ *zetapb.Empty) (*zetapb.ServerKeyResponse, error) {
	return &zetapb.ServerKeyResponse{PublicKey: string(s.caPEM)}, nil
}

func (s *GRPCServer) Register(_ context.Context, req *zetapb.RegisterRequest) (*zetapb.RegisterResponse, error) {
	if req.WgPublicKey == "" {
		return nil, status.Error(codes.InvalidArgument, "wg_public_key required")
	}
	if req.Hostname == "" {
		return nil, status.Error(codes.InvalidArgument, "hostname required")
	}

	cfg, err := s.coord.Enroll(req)
	if err != nil {
		slog.Warn("enrollment failed", "err", err, "hostname", req.Hostname)
		return nil, status.Errorf(codes.InvalidArgument, "enrollment failed: %v", err)
	}

	return &zetapb.RegisterResponse{
		Result: &zetapb.RegisterResponse_Config{Config: cfg},
	}, nil
}

func (s *GRPCServer) Sync(stream zetapb.CoordinatorService_SyncServer) error {
	nodeID, err := nodeIDFromMetadata(stream.Context())
	if err != nil {
		return status.Error(codes.Unauthenticated, err.Error())
	}

	ch := make(chan *zetapb.SyncResponse, 8)
	s.coord.RegisterStream(nodeID, ch)
	defer s.coord.UnregisterStream(nodeID)

	sendDone := make(chan struct{})
	go func() {
		defer close(sendDone)
		pumpSend(stream.Send, ch, "sync", nodeID)
	}()

	pumpRecv(stream.Recv, "sync", nodeID, func(upd *zetapb.SyncUpdate) {
		if err := s.coord.HandleSyncUpdate(nodeID, upd); err != nil {
			slog.Warn("handling sync update", "node_id", nodeID, "err", err)
		}
	})

	close(ch)
	<-sendDone
	return nil
}

func (s *GRPCServer) Relay(stream zetapb.CoordinatorService_RelayServer) error {
	nodeID, err := nodeIDFromMetadata(stream.Context())
	if err != nil {
		return status.Error(codes.Unauthenticated, err.Error())
	}

	ch := make(chan *zetapb.RelayFrame, 8)
	s.coord.RegisterRelayStream(nodeID, ch)
	defer s.coord.UnregisterRelayStream(nodeID)

	sendDone := make(chan struct{})
	go func() {
		defer close(sendDone)
		pumpSend(stream.Send, ch, "relay", nodeID)
	}()

	pumpRecv(stream.Recv, "relay", nodeID, func(frame *zetapb.RelayFrame) {
		s.coord.HandleRelayFrame(nodeID, frame)
	})

	close(ch)
	<-sendDone
	return nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

// pumpSend drains ch, writing each message to the stream via send, until ch
// is closed or send fails.
func pumpSend[T any](send func(T) error, ch <-chan T, logTag, nodeID string) {
	for msg := range ch {
		if err := send(msg); err != nil {
			slog.Debug(logTag+" send error", "node_id", nodeID, "err", err)
			return
		}
	}
}

// pumpRecv reads from the stream via recv, calling onMsg for each message,
// until EOF or an error.
func pumpRecv[T any](recv func() (T, error), logTag, nodeID string, onMsg func(T)) {
	for {
		msg, err := recv()
		if err == io.EOF {
			return
		}
		if err != nil {
			slog.Debug(logTag+" recv error", "node_id", nodeID, "err", err)
			return
		}
		onMsg(msg)
	}
}

func nodeIDFromMetadata(ctx context.Context) (string, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", nil // allow unauthenticated for now; Phase 2 enforces cert auth
	}
	vals := md.Get("node-id")
	if len(vals) > 0 && vals[0] != "" {
		return vals[0], nil
	}
	return "", nil
}
