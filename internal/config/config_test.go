package config

import (
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
}

func TestValidateRequiresAllowlist(t *testing.T) {
	c := Config{SessionFile: "/x", RTMPPublicAddr: "host:1935", MediaMTXAPIURL: "http://x", CheckInterval: time.Second, MissingGrace: time.Second, ReRequestInterval: time.Second, RequestTimeout: time.Second}
	if err := c.Validate(); err == nil {
		t.Fatal("expected error")
	}
}
