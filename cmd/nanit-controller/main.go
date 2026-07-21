package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/keatsfonam/nanit-controller/internal/config"
	"github.com/keatsfonam/nanit-controller/internal/controller"
	"github.com/keatsfonam/nanit-controller/internal/session"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(2)
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(logger)
	if err := run(cfg, logger); err != nil {
		logger.Error("controller stopped", "error", err)
		os.Exit(1)
	}
	logger.Info("controller stopped")
}

func run(cfg config.Config, logger *slog.Logger) error {
	store := session.NewStore(cfg.SessionFile)
	if err := store.Load(); err != nil {
		return fmt.Errorf("load session %q: %w", cfg.SessionFile, err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ctrl := controller.New(cfg, store, logger)
	srv := newHealthServer(cfg.HealthAddr, ctrl.Status())
	listener, err := net.Listen("tcp", cfg.HealthAddr)
	if err != nil {
		return fmt.Errorf("listen for health requests on %q: %w", cfg.HealthAddr, err)
	}
	serverErr := make(chan error, 1)
	go func() {
		err := srv.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		serverErr <- err
		stop()
	}()

	logger.Info("starting Nanit controller", "camera_count", len(cfg.BabyUIDs), "mediamtx_api", cfg.MediaMTXAPIURL, "health_addr", listener.Addr().String())
	controllerErr := ctrl.Run(ctx)
	stop()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	shutdownErr := srv.Shutdown(shutdownCtx)
	healthErr := <-serverErr

	if controllerErr != nil && !errors.Is(controllerErr, context.Canceled) {
		return controllerErr
	}
	if healthErr != nil {
		return fmt.Errorf("health server failed: %w", healthErr)
	}
	if shutdownErr != nil {
		return fmt.Errorf("shut down health server: %w", shutdownErr)
	}
	return nil
}

func newHealthServer(addr string, status *controller.StatusRegistry) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.Handle("/readyz", status)
	mux.HandleFunc("/statusz", status.ServeStatusHTTP)
	return &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
}
