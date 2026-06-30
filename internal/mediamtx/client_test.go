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
		_, _ = w.Write([]byte(`{"items":[{"name":"local/ef693503","ready":true},{"name":"local/missing","ready":false}]}`))
	}))
	defer srv.Close()
	c := New(srv.URL)
	ready, err := c.PathReady(context.Background(), "local/ef693503")
	if err != nil || !ready {
		t.Fatalf("ready=%v err=%v", ready, err)
	}
	ready, err = c.PathReady(context.Background(), "local/nope")
	if err != nil || ready {
		t.Fatalf("ready=%v err=%v", ready, err)
	}
}
