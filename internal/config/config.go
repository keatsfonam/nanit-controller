package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	SessionFile                    string
	BootstrapRefreshToken          string
	BabyUIDs                       []string
	RTMPPublicAddr                 string
	RTMPPathPrefix                 string
	MediaMTXAPIURL                 string
	CheckInterval                  time.Duration
	MissingGrace                   time.Duration
	ReRequestInterval              time.Duration
	MissingPublisherRestartRetries int
	RetryBackoffInitial            time.Duration
	RetryBackoffMax                time.Duration
	ConnectionLimitBackoff         time.Duration
	RequestTimeout                 time.Duration
	HealthAddr                     string
	LogLevel                       slog.Level
}

func Load() (Config, error) {
	cfg := Config{
		SessionFile:                    getEnv("NANIT_SESSION_FILE", "/data/session.json"),
		BootstrapRefreshToken:          strings.TrimSpace(os.Getenv("NANIT_BOOTSTRAP_REFRESH_TOKEN")),
		BabyUIDs:                       splitCSV(os.Getenv("NANIT_BABY_UIDS")),
		RTMPPublicAddr:                 strings.TrimSpace(os.Getenv("NANIT_RTMP_PUBLIC_ADDR")),
		RTMPPathPrefix:                 cleanPrefix(getEnv("NANIT_RTMP_PATH_PREFIX", "/local")),
		MediaMTXAPIURL:                 strings.TrimRight(getEnv("NANIT_MEDIAMTX_API_URL", "http://127.0.0.1:9997"), "/"),
		CheckInterval:                  getDuration("NANIT_CHECK_INTERVAL", 20*time.Second),
		MissingGrace:                   getDuration("NANIT_MISSING_GRACE", 30*time.Second),
		ReRequestInterval:              getDuration("NANIT_REREQUEST_INTERVAL", 60*time.Second),
		MissingPublisherRestartRetries: getInt("NANIT_MISSING_PUBLISHER_RESTART_RETRIES", 3),
		RetryBackoffInitial:            getDuration("NANIT_RETRY_BACKOFF_INITIAL", 15*time.Second),
		RetryBackoffMax:                getDuration("NANIT_RETRY_BACKOFF_MAX", 15*time.Minute),
		ConnectionLimitBackoff:         getDuration("NANIT_CONNECTION_LIMIT_BACKOFF", 5*time.Minute),
		RequestTimeout:                 getDuration("NANIT_REQUEST_TIMEOUT", 30*time.Second),
		HealthAddr:                     getEnv("NANIT_HEALTH_ADDR", ":8080"),
		LogLevel:                       parseLevel(getEnv("NANIT_LOG_LEVEL", "info")),
	}
	return cfg, cfg.Validate()
}

func (c Config) Validate() error {
	var errs []string
	if c.SessionFile == "" {
		errs = append(errs, "NANIT_SESSION_FILE is required")
	}
	if len(c.BabyUIDs) == 0 {
		errs = append(errs, "NANIT_BABY_UIDS is required")
	}
	if c.RTMPPublicAddr == "" {
		errs = append(errs, "NANIT_RTMP_PUBLIC_ADDR is required")
	}
	if c.MediaMTXAPIURL == "" {
		errs = append(errs, "NANIT_MEDIAMTX_API_URL is required")
	}
	if c.CheckInterval <= 0 || c.MissingGrace <= 0 || c.ReRequestInterval <= 0 || c.RetryBackoffInitial <= 0 || c.RetryBackoffMax <= 0 || c.ConnectionLimitBackoff <= 0 || c.RequestTimeout <= 0 {
		errs = append(errs, "duration settings must be positive")
	}
	if c.MissingPublisherRestartRetries < 0 {
		errs = append(errs, "NANIT_MISSING_PUBLISHER_RESTART_RETRIES must be non-negative")
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

func (c Config) RTMPURL(babyUID string) string {
	return fmt.Sprintf("rtmp://%s%s/%s", c.RTMPPublicAddr, c.RTMPPathPrefix, babyUID)
}

func (c Config) PathName(babyUID string) string {
	return strings.TrimPrefix(c.RTMPPathPrefix, "/") + "/" + babyUID
}

func getEnv(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func splitCSV(v string) []string {
	var out []string
	for _, part := range strings.Split(v, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func cleanPrefix(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || v == "/" {
		return "/local"
	}
	if !strings.HasPrefix(v, "/") {
		v = "/" + v
	}
	return strings.TrimRight(v, "/")
}

func getDuration(key string, def time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}

func getInt(key string, def int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return i
}

func parseLevel(v string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "debug", "trace":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
