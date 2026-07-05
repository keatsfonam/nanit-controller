package config

import (
	"strings"
	"testing"
	"time"
)

func TestRTMPURLAndPath(t *testing.T) {
	c := Config{RTMPPublicAddr: "192.168.130.129:1935", RTMPPathPrefix: "/local"}
	if got := c.RTMPURL("ef693503"); got != "rtmp://192.168.130.129:1935/local/ef693503" {
		t.Fatalf("unexpected URL: %s", got)
	}
	if got := c.PathName("ef693503"); got != "local/ef693503" {
		t.Fatalf("unexpected path: %s", got)
	}
}

func TestLoadFromEnv(t *testing.T) {
	t.Setenv("NANIT_SESSION_FILE", "/tmp/session.json")
	t.Setenv("NANIT_BABY_UIDS", "ef693503, b968ee9f")
	t.Setenv("NANIT_RTMP_PUBLIC_ADDR", "192.168.130.129:1935")
	t.Setenv("NANIT_CHECK_INTERVAL", "5s")
	t.Setenv("NANIT_MISSING_PUBLISHER_RESTART_RETRIES", "4")
	t.Setenv("NANIT_RETRY_BACKOFF_INITIAL", "10s")
	t.Setenv("NANIT_RETRY_BACKOFF_MAX", "2m")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(c.BabyUIDs) != 2 || c.BabyUIDs[1] != "b968ee9f" {
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
	t.Setenv("NANIT_BABY_UIDS", "ef693503")
	t.Setenv("NANIT_RTMP_PUBLIC_ADDR", "192.168.130.129:1935")
	t.Setenv("NANIT_CHECK_INTERVAL", "20 s")
	t.Setenv("NANIT_MISSING_PUBLISHER_RESTART_RETRIES", "three")
	_, err := Load()
	if err == nil {
		t.Fatal("expected error for malformed env values")
	}
	for _, key := range []string{"NANIT_CHECK_INTERVAL", "NANIT_MISSING_PUBLISHER_RESTART_RETRIES"} {
		if !strings.Contains(err.Error(), key) {
			t.Fatalf("error %q does not mention %s", err, key)
		}
	}
}

func TestValidateRequiresAllowlist(t *testing.T) {
	c := Config{SessionFile: "/x", RTMPPublicAddr: "host:1935", MediaMTXAPIURL: "http://x", CheckInterval: time.Second, MissingGrace: time.Second, ReRequestInterval: time.Second, RetryBackoffInitial: time.Second, RetryBackoffMax: time.Minute, ConnectionLimitBackoff: time.Minute, RequestTimeout: time.Second}
	if err := c.Validate(); err == nil {
		t.Fatal("expected error")
	}
}
