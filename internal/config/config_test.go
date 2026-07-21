package config

import (
	"strings"
	"testing"
	"time"
)

func TestRTMPURLAndPath(t *testing.T) {
	c := Config{RTMPPublicAddr: "192.168.130.129:1935", RTMPPathPrefix: "/local"}
	if got := c.RTMPURL("0a1b2c3d"); got != "rtmp://192.168.130.129:1935/local/0a1b2c3d" {
		t.Fatalf("unexpected URL: %s", got)
	}
	if got := c.PathName("0a1b2c3d"); got != "local/0a1b2c3d" {
		t.Fatalf("unexpected path: %s", got)
	}
}

func TestLoadFromEnv(t *testing.T) {
	t.Setenv("NANIT_SESSION_FILE", "/tmp/session.json")
	t.Setenv("NANIT_BABY_UIDS", "0a1b2c3d, 4e5f6a7b")
	t.Setenv("NANIT_RTMP_PUBLIC_ADDR", "192.168.130.129:1935")
	t.Setenv("NANIT_CHECK_INTERVAL", "5s")
	t.Setenv("NANIT_MISSING_PUBLISHER_RESTART_RETRIES", "4")
	t.Setenv("NANIT_RETRY_BACKOFF_INITIAL", "10s")
	t.Setenv("NANIT_RETRY_BACKOFF_MAX", "2m")
	t.Setenv("NANIT_CONNECTION_LIMIT_BACKOFF", "1m")
	t.Setenv("NANIT_LOG_LEVEL", "warning")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(c.BabyUIDs) != 2 || c.BabyUIDs[1] != "4e5f6a7b" {
		t.Fatalf("unexpected baby UIDs: %#v", c.BabyUIDs)
	}
	if c.CheckInterval != 5*time.Second {
		t.Fatalf("unexpected interval: %s", c.CheckInterval)
	}
	if c.MissingPublisherRestartRetries != 4 {
		t.Fatalf("unexpected missing-publisher retries: %d", c.MissingPublisherRestartRetries)
	}
	if c.RetryBackoffInitial != 10*time.Second || c.RetryBackoffMax != 2*time.Minute {
		t.Fatalf("unexpected backoff config: initial=%s max=%s", c.RetryBackoffInitial, c.RetryBackoffMax)
	}
}

func TestLoadRejectsMalformedValues(t *testing.T) {
	t.Setenv("NANIT_BABY_UIDS", "0a1b2c3d")
	t.Setenv("NANIT_RTMP_PUBLIC_ADDR", "192.168.130.129:1935")
	t.Setenv("NANIT_CHECK_INTERVAL", "20 s")
	t.Setenv("NANIT_MISSING_PUBLISHER_RESTART_RETRIES", "three")
	t.Setenv("NANIT_LOG_LEVEL", "verbose")
	_, err := Load()
	if err == nil {
		t.Fatal("expected error for malformed env values")
	}
	for _, key := range []string{"NANIT_CHECK_INTERVAL", "NANIT_MISSING_PUBLISHER_RESTART_RETRIES", "NANIT_LOG_LEVEL"} {
		if !strings.Contains(err.Error(), key) {
			t.Fatalf("error %q does not mention %s", err, key)
		}
	}
}

func TestValidateRejectsUnsafeOrAmbiguousValues(t *testing.T) {
	base := Config{
		SessionFile:                    "/x/session.json",
		BabyUIDs:                       []string{"baby"},
		RTMPPublicAddr:                 "host:1935",
		RTMPPathPrefix:                 "/local",
		MediaMTXAPIURL:                 "http://127.0.0.1:9997",
		CheckInterval:                  time.Second,
		MissingGrace:                   time.Second,
		ReRequestInterval:              time.Second,
		MissingPublisherRestartRetries: 1,
		RetryBackoffInitial:            time.Second,
		RetryBackoffMax:                time.Minute,
		ConnectionLimitBackoff:         time.Minute,
		RequestTimeout:                 time.Second,
		HealthAddr:                     ":8080",
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid base config: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{name: "duplicate UID", mutate: func(c *Config) { c.BabyUIDs = []string{"baby", "baby"} }, want: "duplicate UID"},
		{name: "unsafe UID", mutate: func(c *Config) { c.BabyUIDs = []string{"../baby"} }, want: "invalid UID"},
		{name: "UID placeholder", mutate: func(c *Config) { c.BabyUIDs = []string{"REPLACE_WITH_UID"} }, want: "placeholder"},
		{name: "RTMP placeholder", mutate: func(c *Config) { c.RTMPPublicAddr = "REPLACE_WITH_IP:1935" }, want: "placeholder"},
		{name: "RTMP missing port", mutate: func(c *Config) { c.RTMPPublicAddr = "host" }, want: "host:port"},
		{name: "RTMP whitespace", mutate: func(c *Config) { c.RTMPPublicAddr = "bad host:1935" }, want: "plain host:port"},
		{name: "RTMP path", mutate: func(c *Config) { c.RTMPPublicAddr = "host/path:1935" }, want: "plain host:port"},
		{name: "RTMP userinfo", mutate: func(c *Config) { c.RTMPPublicAddr = "user@host:1935" }, want: "plain host:port"},
		{name: "RTMP query", mutate: func(c *Config) { c.RTMPPublicAddr = "host?x:1935" }, want: "plain host:port"},
		{name: "invalid prefix", mutate: func(c *Config) { c.RTMPPathPrefix = "/local/../escape" }, want: "invalid path segment"},
		{name: "API credentials", mutate: func(c *Config) { c.MediaMTXAPIURL = "http://user:pass@host:9997" }, want: "without credentials"},
		{name: "API path", mutate: func(c *Config) { c.MediaMTXAPIURL = "http://host:9997/v3" }, want: "without credentials"},
		{name: "health port", mutate: func(c *Config) { c.HealthAddr = ":70000" }, want: "invalid port"},
		{name: "backoff order", mutate: func(c *Config) { c.RetryBackoffInitial = 2 * time.Minute }, want: "must not exceed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := base
			cfg.BabyUIDs = append([]string(nil), base.BabyUIDs...)
			tt.mutate(&cfg)
			err := cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestValidateAcceptsIPv6RTMPAddress(t *testing.T) {
	if err := validateHostPort("[2001:db8::1]:1935"); err != nil {
		t.Fatal(err)
	}
}
