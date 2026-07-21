package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
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
	var errs []string
	logLevel, err := parseLevel(getEnv("NANIT_LOG_LEVEL", "info"))
	if err != nil {
		errs = append(errs, err.Error())
	}
	cfg := Config{
		SessionFile:                    getEnv("NANIT_SESSION_FILE", "/data/session.json"),
		BootstrapRefreshToken:          strings.TrimSpace(os.Getenv("NANIT_BOOTSTRAP_REFRESH_TOKEN")),
		BabyUIDs:                       splitCSV(os.Getenv("NANIT_BABY_UIDS")),
		RTMPPublicAddr:                 strings.TrimSpace(os.Getenv("NANIT_RTMP_PUBLIC_ADDR")),
		RTMPPathPrefix:                 cleanPrefix(getEnv("NANIT_RTMP_PATH_PREFIX", "/local")),
		MediaMTXAPIURL:                 strings.TrimRight(getEnv("NANIT_MEDIAMTX_API_URL", "http://127.0.0.1:9997"), "/"),
		CheckInterval:                  getDuration("NANIT_CHECK_INTERVAL", 20*time.Second, &errs),
		MissingGrace:                   getDuration("NANIT_MISSING_GRACE", 30*time.Second, &errs),
		ReRequestInterval:              getDuration("NANIT_REREQUEST_INTERVAL", 60*time.Second, &errs),
		MissingPublisherRestartRetries: getInt("NANIT_MISSING_PUBLISHER_RESTART_RETRIES", 3, &errs),
		RetryBackoffInitial:            getDuration("NANIT_RETRY_BACKOFF_INITIAL", 15*time.Second, &errs),
		RetryBackoffMax:                getDuration("NANIT_RETRY_BACKOFF_MAX", 15*time.Minute, &errs),
		ConnectionLimitBackoff:         getDuration("NANIT_CONNECTION_LIMIT_BACKOFF", 5*time.Minute, &errs),
		RequestTimeout:                 getDuration("NANIT_REQUEST_TIMEOUT", 30*time.Second, &errs),
		HealthAddr:                     getEnv("NANIT_HEALTH_ADDR", ":8080"),
		LogLevel:                       logLevel,
	}
	if len(errs) > 0 {
		return cfg, errors.New(strings.Join(errs, "; "))
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
	seenUIDs := make(map[string]struct{}, len(c.BabyUIDs))
	for _, uid := range c.BabyUIDs {
		switch {
		case isPlaceholder(uid):
			errs = append(errs, "NANIT_BABY_UIDS still contains a placeholder")
		case !validPathSegment(uid):
			errs = append(errs, fmt.Sprintf("NANIT_BABY_UIDS contains invalid UID %q", uid))
		}
		if _, exists := seenUIDs[uid]; exists {
			errs = append(errs, fmt.Sprintf("NANIT_BABY_UIDS contains duplicate UID %q", uid))
		}
		seenUIDs[uid] = struct{}{}
	}

	if c.RTMPPublicAddr == "" {
		errs = append(errs, "NANIT_RTMP_PUBLIC_ADDR is required")
	} else if isPlaceholder(c.RTMPPublicAddr) {
		errs = append(errs, "NANIT_RTMP_PUBLIC_ADDR still contains a placeholder")
	} else if err := validateHostPort(c.RTMPPublicAddr); err != nil {
		errs = append(errs, "NANIT_RTMP_PUBLIC_ADDR: "+err.Error())
	}
	if err := validatePathPrefix(c.RTMPPathPrefix); err != nil {
		errs = append(errs, "NANIT_RTMP_PATH_PREFIX: "+err.Error())
	}
	if err := validateMediaMTXURL(c.MediaMTXAPIURL); err != nil {
		errs = append(errs, "NANIT_MEDIAMTX_API_URL: "+err.Error())
	}
	if err := validateListenAddr(c.HealthAddr); err != nil {
		errs = append(errs, "NANIT_HEALTH_ADDR: "+err.Error())
	}

	if c.CheckInterval <= 0 || c.MissingGrace <= 0 || c.ReRequestInterval <= 0 || c.RetryBackoffInitial <= 0 || c.RetryBackoffMax <= 0 || c.ConnectionLimitBackoff <= 0 || c.RequestTimeout <= 0 {
		errs = append(errs, "duration settings must be positive")
	}
	if c.RetryBackoffInitial > c.RetryBackoffMax {
		errs = append(errs, "NANIT_RETRY_BACKOFF_INITIAL must not exceed NANIT_RETRY_BACKOFF_MAX")
	}
	if c.ConnectionLimitBackoff > c.RetryBackoffMax {
		errs = append(errs, "NANIT_CONNECTION_LIMIT_BACKOFF must not exceed NANIT_RETRY_BACKOFF_MAX")
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

func getDuration(key string, def time.Duration, errs *[]string) time.Duration {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		*errs = append(*errs, fmt.Sprintf("%s: invalid duration %q", key, v))
		return def
	}
	return d
}

func getInt(key string, def int, errs *[]string) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		*errs = append(*errs, fmt.Sprintf("%s: invalid integer %q", key, v))
		return def
	}
	return i
}

func parseLevel(v string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "debug", "trace":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf("NANIT_LOG_LEVEL: invalid level %q", v)
	}
}

func validateHostPort(value string) error {
	u, err := url.Parse("rtmp://" + value)
	if err != nil {
		return fmt.Errorf("must be a plain host:port: %w", err)
	}
	if u.User != nil || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return errors.New("must be a plain host:port without credentials, path, query, or fragment")
	}
	host, port, err := net.SplitHostPort(u.Host)
	if err != nil {
		return fmt.Errorf("must be host:port: %w", err)
	}
	if strings.TrimSpace(host) == "" {
		return errors.New("host is required")
	}
	return validatePort(port)
}

func validateListenAddr(value string) error {
	_, port, err := net.SplitHostPort(value)
	if err != nil {
		return fmt.Errorf("must be host:port or :port: %w", err)
	}
	return validatePort(port)
}

func validatePort(value string) error {
	port, err := strconv.Atoi(value)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("invalid port %q", value)
	}
	return nil
}

func validatePathPrefix(value string) error {
	if !strings.HasPrefix(value, "/") {
		return errors.New("must start with /")
	}
	for _, segment := range strings.Split(strings.TrimPrefix(value, "/"), "/") {
		if !validPathSegment(segment) {
			return fmt.Errorf("invalid path segment %q", segment)
		}
	}
	return nil
}

func validPathSegment(value string) bool {
	if value == "" || value == "." || value == ".." {
		return false
	}
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' && r != '_' {
			return false
		}
	}
	return true
}

func validateMediaMTXURL(value string) error {
	u, err := url.Parse(value)
	if err != nil {
		return err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("scheme must be http or https")
	}
	if u.Host == "" {
		return errors.New("host is required")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return errors.New("must be an API origin without credentials, query, fragment, or path")
	}
	return nil
}

func isPlaceholder(value string) bool {
	upper := strings.ToUpper(value)
	return strings.Contains(upper, "REPLACE_") || strings.Contains(upper, "CHANGE_ME") || strings.Contains(upper, "CHANGEME")
}
