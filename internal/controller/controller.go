package controller

import (
	"context"
	"errors"
	"log/slog"
	"math/rand"
	"sync"
	"time"

	indie "github.com/indiefan/home_assistant_nanit/pkg/client"
	"github.com/keatsfonam/nanit-controller/internal/config"
	"github.com/keatsfonam/nanit-controller/internal/mediamtx"
	"github.com/keatsfonam/nanit-controller/internal/nanit"
	"github.com/keatsfonam/nanit-controller/internal/session"
)

type nanitService interface {
	EnsureAuthorized(ctx context.Context, bootstrapRefreshToken, rejectedAccessToken string) error
	FetchBabies(ctx context.Context, bootstrapRefreshToken string) ([]session.Baby, error)
}

type mediaChecker interface {
	PathReady(ctx context.Context, name string) (bool, error)
}

type streamConn interface {
	SendStreaming(ctx context.Context, rtmpURL string, status indie.Streaming_Status, timeout time.Duration) error
	Done() <-chan struct{}
	Close() error
}

type dialWSFunc func(ctx context.Context, cameraUID, accessToken string, log *slog.Logger) (streamConn, error)

type Controller struct {
	cfg      config.Config
	store    *session.Store
	nanit    nanitService
	mediamtx mediaChecker
	log      *slog.Logger
	status   *StatusRegistry
	dialWS   dialWSFunc
	sleep    func(context.Context, time.Duration)
}

func New(cfg config.Config, store *session.Store, log *slog.Logger) *Controller {
	return &Controller{
		cfg:      cfg,
		store:    store,
		nanit:    nanit.NewClient(store, log.With("component", "nanit")),
		mediamtx: mediamtx.New(cfg.MediaMTXAPIURL),
		log:      log,
		status:   NewStatusRegistry(),
		dialWS: func(ctx context.Context, cameraUID, accessToken string, log *slog.Logger) (streamConn, error) {
			return nanit.DialWS(ctx, cameraUID, accessToken, log)
		},
		sleep: sleep,
	}
}

func (c *Controller) Status() *StatusRegistry { return c.status }

func (c *Controller) Run(ctx context.Context) error {
	babies, err := c.fetchBabiesWithFallback(ctx)
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
			c.status.RegisterMissing(uid)
			c.log.Warn("configured baby UID not found in Nanit account", "baby_uid", uid)
			continue
		}
		c.status.RegisterBaby(baby, c.cfg.PathName(baby.UID), c.cfg.RTMPURL(baby.UID))
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

// fetchBabiesWithFallback falls back to the session-cached baby list when
// discovery fails, and retries with backoff when there is no cache, so a
// Nanit outage at startup doesn't crash-loop the pod.
func (c *Controller) fetchBabiesWithFallback(ctx context.Context) ([]session.Baby, error) {
	retry := newExponentialBackoff(c.cfg.RetryBackoffMax, 0.2, nil)
	for {
		babies, err := c.nanit.FetchBabies(ctx, c.cfg.BootstrapRefreshToken)
		if err == nil {
			return babies, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if cached := c.store.Snapshot().Babies; len(cached) > 0 {
			c.log.Warn("fetch babies failed, using cached baby list from session", "error", err)
			return cached, nil
		}
		d := retry.Next(c.cfg.RetryBackoffInitial)
		c.log.Warn("fetch babies failed, retrying", "error", err, "retry_in", d)
		c.sleep(ctx, d)
	}
}

type reconcileOutcome string

const (
	reconcileDisconnected    reconcileOutcome = "websocket_disconnected"
	reconcileConnectionLimit reconcileOutcome = "connection_limit"
	reconcileRequestsFailing reconcileOutcome = "requests_failing"
)

// maxConsecutiveSendFailures bounds streaming requests on a connection that
// accepts writes but never succeeds; past it the socket is torn down and redialed.
const maxConsecutiveSendFailures = 3

func (c *Controller) runBaby(ctx context.Context, baby session.Baby) {
	log := c.log.With("baby_uid", baby.UID, "camera_uid", baby.CameraUID, "baby_name", baby.Name)
	retry := newExponentialBackoff(c.cfg.RetryBackoffMax, 0.2, rand.New(rand.NewSource(time.Now().UnixNano())))
	for ctx.Err() == nil {
		if err := c.nanit.EnsureAuthorized(ctx, c.cfg.BootstrapRefreshToken, ""); err != nil {
			log.Error("authorization failed", "error", err)
			c.status.Update(baby.UID, func(st *CameraStatus) {
				st.State = "authorization_failed"
				st.LastError = err.Error()
				st.WebsocketConnected = false
			})
			c.sleepWithStatus(ctx, baby.UID, retry.Next(c.cfg.RetryBackoffInitial))
			continue
		}
		s := c.store.Snapshot()
		ws, err := c.dialWS(ctx, baby.CameraUID, s.AuthToken, log)
		if err != nil {
			log.Error("websocket connection failed", "error", err)
			// Only burn a refresh-token rotation when the handshake was
			// rejected for auth reasons; network blips don't need it.
			if errors.Is(err, nanit.ErrDialUnauthorized) {
				_ = c.nanit.EnsureAuthorized(ctx, c.cfg.BootstrapRefreshToken, s.AuthToken)
			}
			c.status.Update(baby.UID, func(st *CameraStatus) {
				st.State = "websocket_dial_failed"
				st.LastError = err.Error()
				st.WebsocketConnected = false
			})
			c.sleepWithStatus(ctx, baby.UID, retry.Next(c.cfg.RetryBackoffInitial))
			continue
		}
		c.status.Update(baby.UID, func(st *CameraStatus) {
			st.State = "websocket_connected"
			st.WebsocketConnected = true
			st.LastError = ""
		})
		outcome := c.reconcile(ctx, baby, ws, log, retry)
		// Always release the WebSocket before any retry/backoff sleep. This is especially
		// important after Nanit reports mobile app connection-limit errors.
		_ = ws.Close()
		c.status.Update(baby.UID, func(st *CameraStatus) {
			st.WebsocketConnected = false
		})
		if ctx.Err() != nil {
			return
		}
		base := c.cfg.RetryBackoffInitial
		if outcome == reconcileConnectionLimit {
			base = c.cfg.ConnectionLimitBackoff
		}
		c.sleepWithStatus(ctx, baby.UID, retry.Next(base))
	}
}

func (c *Controller) sleepWithStatus(ctx context.Context, babyUID string, d time.Duration) {
	until := time.Now().Add(d)
	c.status.Update(babyUID, func(st *CameraStatus) {
		st.State = "backing_off"
		st.BackoffUntil = &until
		st.NextRetryAt = &until
	})
	c.sleep(ctx, d)
	c.status.Update(babyUID, func(st *CameraStatus) {
		st.BackoffUntil = nil
		st.NextRetryAt = nil
	})
}

func (c *Controller) reconcile(ctx context.Context, baby session.Baby, ws streamConn, log *slog.Logger, retry *exponentialBackoff) reconcileOutcome {
	rtmpURL := c.cfg.RTMPURL(baby.UID)
	path := c.cfg.PathName(baby.UID)
	lastRequest := time.Time{}
	missingSince := time.Now()
	missingRetryCount := 0
	consecutiveSendFailures := 0

	sendStreaming := func(status indie.Streaming_Status, reason string) reconcileOutcome {
		now := time.Now()
		statusName := streamingStatusName(status)
		log.Info("requesting local streaming", "target", rtmpURL, "path", path, "reason", reason, "status", statusName)
		c.status.Update(baby.UID, func(st *CameraStatus) {
			st.State = "requesting_streaming"
			st.LastRequestStatus = statusName
			st.LastRequestReason = reason
			st.LastRequestAt = &now
		})
		err := ws.SendStreaming(ctx, rtmpURL, status, c.cfg.RequestTimeout)
		switch {
		case err == nil:
			consecutiveSendFailures = 0
			log.Info("local streaming request accepted", "path", path, "status", statusName)
			successAt := time.Now()
			c.status.Update(baby.UID, func(st *CameraStatus) {
				st.State = "streaming_requested"
				st.LastSuccessAt = &successAt
				st.LastError = ""
				if status == indie.Streaming_STARTED {
					st.ConsecutiveConnectionLimitFailures = 0
				}
			})
			return ""
		case errors.Is(err, nanit.ErrConnectionLimit):
			log.Warn("too many Nanit mobile app connections", "error", err)
			c.status.Update(baby.UID, func(st *CameraStatus) {
				st.State = "connection_limited"
				st.LastError = err.Error()
				st.ConsecutiveConnectionLimitFailures++
			})
			return reconcileConnectionLimit
		default:
			consecutiveSendFailures++
			log.Warn("local streaming request failed", "error", err, "status", statusName, "consecutive_failures", consecutiveSendFailures)
			c.status.Update(baby.UID, func(st *CameraStatus) {
				st.State = "request_failed"
				st.LastError = err.Error()
			})
			if consecutiveSendFailures >= maxConsecutiveSendFailures {
				log.Warn("streaming requests keep failing, reconnecting websocket", "consecutive_failures", consecutiveSendFailures)
				return reconcileRequestsFailing
			}
			return ""
		}
	}

	requestStarted := func(reason string) reconcileOutcome {
		if !lastRequest.IsZero() && time.Since(lastRequest) < c.cfg.ReRequestInterval {
			return ""
		}
		lastRequest = time.Now()
		return sendStreaming(indie.Streaming_STARTED, reason)
	}

	if out := requestStarted("websocket_connected"); out != "" {
		return out
	}
	ticker := time.NewTicker(c.cfg.CheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return reconcileDisconnected
		case <-ws.Done():
			log.Warn("websocket disconnected")
			c.status.Update(baby.UID, func(st *CameraStatus) {
				st.State = "websocket_disconnected"
				st.WebsocketConnected = false
			})
			return reconcileDisconnected
		case <-ticker.C:
			ready, err := c.mediamtx.PathReady(ctx, path)
			if err != nil {
				log.Warn("mediamtx path check failed", "path", path, "error", err)
				c.status.Update(baby.UID, func(st *CameraStatus) {
					st.State = "mediamtx_check_failed"
					st.LastError = err.Error()
				})
				continue
			}
			if ready {
				now := time.Now()
				missingSince = time.Time{}
				missingRetryCount = 0
				retry.Reset()
				log.Debug("mediamtx path is ready", "path", path)
				c.status.Update(baby.UID, func(st *CameraStatus) {
					st.State = "ready"
					st.PublisherPresent = true
					st.PublisherLastSeen = &now
					st.MissingSince = nil
					st.MissingRetryCount = 0
					st.LastError = ""
					st.ConsecutiveConnectionLimitFailures = 0
				})
				continue
			}

			now := time.Now()
			c.status.Update(baby.UID, func(st *CameraStatus) {
				st.State = "publisher_missing"
				st.PublisherPresent = false
				st.MissingRetryCount = missingRetryCount
			})
			if missingSince.IsZero() {
				missingSince = now
				log.Info("mediamtx path missing", "path", path)
				c.status.Update(baby.UID, func(st *CameraStatus) { st.MissingSince = &now })
				continue
			}
			c.status.Update(baby.UID, func(st *CameraStatus) { st.MissingSince = &missingSince })
			if time.Since(missingSince) < c.cfg.MissingGrace {
				continue
			}
			if !lastRequest.IsZero() && time.Since(lastRequest) < c.cfg.ReRequestInterval {
				continue
			}

			missingRetryCount++
			c.status.Update(baby.UID, func(st *CameraStatus) { st.MissingRetryCount = missingRetryCount })
			if c.cfg.MissingPublisherRestartRetries > 0 && missingRetryCount >= c.cfg.MissingPublisherRestartRetries {
				log.Warn("resetting local streaming after missing publisher retries", "path", path, "missing_retry_count", missingRetryCount)
				if out := sendStreaming(indie.Streaming_STOPPED, "mediamtx_path_missing_reset"); out != "" {
					return out
				}
				c.sleep(ctx, 2*time.Second)
				if ctx.Err() != nil {
					return reconcileDisconnected
				}
				lastRequest = time.Now()
				missingRetryCount = 0
				c.status.Update(baby.UID, func(st *CameraStatus) { st.MissingRetryCount = 0 })
				if out := sendStreaming(indie.Streaming_STARTED, "mediamtx_path_missing_reset"); out != "" {
					return out
				}
				continue
			}

			if out := requestStarted("mediamtx_path_missing"); out != "" {
				return out
			}
		}
	}
}

func streamingStatusName(status indie.Streaming_Status) string {
	switch status {
	case indie.Streaming_STARTED:
		return "STARTED"
	case indie.Streaming_STOPPED:
		return "STOPPED"
	default:
		return status.String()
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
