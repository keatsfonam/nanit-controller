package controller

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"git.keatsfonam.com/lab/nanit-controller/internal/config"
	"git.keatsfonam.com/lab/nanit-controller/internal/mediamtx"
	"git.keatsfonam.com/lab/nanit-controller/internal/nanit"
	"git.keatsfonam.com/lab/nanit-controller/internal/session"
	indie "github.com/indiefan/home_assistant_nanit/pkg/client"
)

type Controller struct {
	cfg      config.Config
	store    *session.Store
	nanit    *nanit.Client
	mediamtx *mediamtx.Client
	log      *slog.Logger
}

func New(cfg config.Config, store *session.Store, log *slog.Logger) *Controller {
	return &Controller{cfg: cfg, store: store, nanit: nanit.NewClient(store, log.With("component", "nanit")), mediamtx: mediamtx.New(cfg.MediaMTXAPIURL), log: log}
}

func (c *Controller) Run(ctx context.Context) error {
	babies, err := c.nanit.FetchBabies(ctx, c.cfg.BootstrapRefreshToken)
	if err != nil {
		return err
	}
	byUID := map[string]session.Baby{}
	for _, b := range babies {
		byUID[b.UID] = b
	}

	var wg sync.WaitGroup
	for _, uid := range c.cfg.BabyUIDs {
		baby, ok := byUID[uid]
		if !ok {
			c.log.Warn("configured baby UID not found in Nanit account", "baby_uid", uid)
			continue
		}
		wg.Add(1)
		go func(b session.Baby) {
			defer wg.Done()
			c.runBaby(ctx, b)
		}(baby)
	}
	<-ctx.Done()
	wg.Wait()
	return ctx.Err()
}

func (c *Controller) runBaby(ctx context.Context, baby session.Baby) {
	log := c.log.With("baby_uid", baby.UID, "camera_uid", baby.CameraUID, "baby_name", baby.Name)
	for ctx.Err() == nil {
		if err := c.nanit.EnsureAuthorized(ctx, c.cfg.BootstrapRefreshToken, false); err != nil {
			log.Error("authorization failed", "error", err)
			sleep(ctx, time.Minute)
			continue
		}
		s := c.store.Snapshot()
		ws, err := nanit.DialWS(ctx, baby.CameraUID, s.AuthToken, log)
		if err != nil {
			log.Error("websocket connection failed", "error", err)
			_ = c.nanit.EnsureAuthorized(ctx, c.cfg.BootstrapRefreshToken, true)
			sleep(ctx, time.Minute)
			continue
		}
		c.reconcile(ctx, baby, ws, log)
		_ = ws.Close()
		sleep(ctx, 15*time.Second)
	}
}

func (c *Controller) reconcile(ctx context.Context, baby session.Baby, ws *nanit.WS, log *slog.Logger) {
	rtmpURL := c.cfg.RTMPURL(baby.UID)
	path := c.cfg.PathName(baby.UID)
	lastRequest := time.Time{}
	missingSince := time.Now()

	request := func(reason string) {
		if time.Since(lastRequest) < c.cfg.ReRequestInterval {
			return
		}
		lastRequest = time.Now()
		log.Info("requesting local streaming", "target", rtmpURL, "path", path, "reason", reason)
		err := ws.SendStreaming(ctx, rtmpURL, indie.Streaming_STARTED, c.cfg.RequestTimeout)
		switch {
		case err == nil:
			log.Info("local streaming successfully requested", "path", path)
		case errors.Is(err, nanit.ErrConnectionLimit):
			log.Warn("too many Nanit mobile app connections", "error", err, "backoff", c.cfg.ConnectionLimitBackoff)
			sleep(ctx, c.cfg.ConnectionLimitBackoff)
		default:
			log.Warn("local streaming request failed", "error", err)
		}
	}

	request("websocket_connected")
	ticker := time.NewTicker(c.cfg.CheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ws.Done():
			log.Warn("websocket disconnected")
			return
		case <-ticker.C:
			ready, err := c.mediamtx.PathReady(ctx, path)
			if err != nil {
				log.Warn("mediamtx path check failed", "path", path, "error", err)
				continue
			}
			if ready {
				missingSince = time.Time{}
				log.Debug("mediamtx path is ready", "path", path)
				continue
			}
			if missingSince.IsZero() {
				missingSince = time.Now()
				log.Info("mediamtx path missing", "path", path)
				continue
			}
			if time.Since(missingSince) >= c.cfg.MissingGrace {
				request("mediamtx_path_missing")
			}
		}
	}
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
