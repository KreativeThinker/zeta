package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kreativethinker/zeta/controller/internal/api"
	"github.com/kreativethinker/zeta/controller/internal/ca"
	"github.com/kreativethinker/zeta/controller/internal/config"
	"github.com/kreativethinker/zeta/controller/internal/coordinator"
	"github.com/kreativethinker/zeta/controller/internal/db"
)

// Injected via ldflags: -X main.Version=... -X main.Commit=...
var (
	Version = "dev"
	Commit  = "unknown"
)

func main() {
	// Mirror version into the REST layer.
	api.Version = Version
	api.Commit = Commit

	cfgPath := flag.String("config", "zeta.yaml", "path to config file")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	slog.Info("zeta controller starting", "version", Version, "commit", Commit)

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		slog.Error("loading config", "err", err)
		os.Exit(1)
	}

	database, err := db.Open(cfg.DB.Path)
	if err != nil {
		slog.Error("opening database", "err", err)
		os.Exit(1)
	}
	defer database.Close()
	slog.Info("database opened", "path", cfg.DB.Path)

	authority, err := ca.LoadOrCreate(database, cfg.CA.CertFile, cfg.CA.KeyFile)
	if err != nil {
		slog.Error("loading CA", "err", err)
		os.Exit(1)
	}
	slog.Info("CA ready")

	coord := coordinator.New(database, authority, cfg)

	// Phase 1: plaintext gRPC (no TLS certs generated for gRPC yet).
	// Phase 2 will issue a server cert from the CA and enable TLS.
	grpcSrv, err := api.NewGRPCServer(coord, authority.CertPEM(), nil, nil)
	if err != nil {
		slog.Error("creating gRPC server", "err", err)
		os.Exit(1)
	}

	httpHandler := api.NewHTTPServer(coord, database)
	httpSrv := &http.Server{
		Addr:         cfg.HTTP.Addr,
		Handler:      httpHandler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start gRPC server.
	go func() {
		if err := grpcSrv.Serve(cfg.GRPC.Addr); err != nil {
			slog.Error("gRPC server stopped", "err", err)
		}
	}()

	// Start HTTP server.
	go func() {
		slog.Info("HTTP server listening", "addr", cfg.HTTP.Addr)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("HTTP server stopped", "err", err)
		}
	}()

	// Wait for termination signal.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	slog.Info("shutting down", "signal", sig)

	// Graceful shutdown: stop accepting new connections, wait up to 10s for in-flight.
	grpcSrv.GracefulStop()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(ctx); err != nil {
		slog.Warn("HTTP shutdown error", "err", err)
	}

	slog.Info("stopped")
}
