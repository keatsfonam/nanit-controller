package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"git.keatsfonam.com/lab/nanit-controller/internal/config"
	"git.keatsfonam.com/lab/nanit-controller/internal/controller"
	"git.keatsfonam.com/lab/nanit-controller/internal/session"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(2)
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(logger)

	store := session.NewStore(cfg.SessionFile)
	if err := store.Load(); err != nil {
		logger.Error("failed to load session", "file", cfg.SessionFile, "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ctrl := controller.New(cfg, store, logger)
	go serveHealth(cfg.HealthAddr, ctrl.Status(), logger)

	logger.Info("starting Nanit controller", "baby_uids", cfg.BabyUIDs, "rtmp_addr", cfg.RTMPPublicAddr, "mediamtx_api", cfg.MediaMTXAPIURL)
	if err := ctrl.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("controller stopped", "error", err)
		os.Exit(1)
	}
	logger.Info("controller stopped")
}

func serveHealth(addr string, status *controller.StatusRegistry, logger *slog.Logger) {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok\n")) })
	mux.Handle("/readyz", status)
	if err := http.ListenAndServe(addr, mux); err != nil && err != http.ErrServerClosed {
		logger.Warn("health server failed", "error", err)
	}
}
