package mediamtx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPathReady(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v3/paths/list" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"items":[{"name":"local/0a1b2c3d","online":true,"ready":false},{"name":"local/offline","online":false,"ready":true,"source":{"type":"rtmpSession"}},{"name":"local/source","ready":false,"source":{"type":"rtmpSession"}},{"name":"local/missing","ready":false,"source":null}]}`))
	}))
	defer srv.Close()
	c := New(srv.URL)
	ready, err := c.PathReady(context.Background(), "local/0a1b2c3d")
	if err != nil || !ready {
		t.Fatalf("ready=%v err=%v", ready, err)
	}
	ready, err = c.PathReady(context.Background(), "local/offline")
	if err != nil || ready {
		t.Fatalf("offline ready=%v err=%v", ready, err)
	}
	ready, err = c.PathReady(context.Background(), "local/source")
	if err != nil || !ready {
		t.Fatalf("source ready=%v err=%v", ready, err)
	}
	ready, err = c.PathReady(context.Background(), "local/missing")
	if err != nil || ready {
		t.Fatalf("missing ready=%v err=%v", ready, err)
	}
	ready, err = c.PathReady(context.Background(), "local/nope")
	if err != nil || ready {
		t.Fatalf("nope ready=%v err=%v", ready, err)
	}
}
