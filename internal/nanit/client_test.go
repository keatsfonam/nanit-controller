package nanit

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/keatsfonam/nanit-controller/internal/session"
)

// The refresh endpoint rotates tokens: each refresh token is single-use, so a
// second request with an already-consumed token gets 404, mirroring Nanit.
func newRotatingTokenServer(t *testing.T, bootstrap string) (*httptest.Server, func() int) {
	t.Helper()
	var mu sync.Mutex
	calls := 0
	valid := bootstrap
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tokens/refresh" {
			t.Errorf("unexpected request path %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var body struct {
			RefreshToken string `json:"refresh_token"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode refresh request: %v", err)
		}
		mu.Lock()
		defer mu.Unlock()
		calls++
		if body.RefreshToken != valid {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		valid = fmt.Sprintf("refresh-%d", calls)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"access_token":  fmt.Sprintf("access-%d", calls),
			"refresh_token": valid,
		})
	}))
	t.Cleanup(srv.Close)
	return srv, func() int {
		mu.Lock()
		defer mu.Unlock()
		return calls
	}
}

func TestEnsureAuthorizedSingleFlightsConcurrentRefreshes(t *testing.T) {
	srv, calls := newRotatingTokenServer(t, "bootstrap")
	store := session.NewStore(t.TempDir() + "/session.json")
	c := NewClient(store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	c.baseURL = srv.URL

	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = c.EnsureAuthorized(context.Background(), "bootstrap", false)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d: %v", i, err)
		}
	}
	if got := calls(); got != 1 {
		t.Fatalf("refresh calls=%d, want 1 (concurrent refreshes must be single-flighted)", got)
	}
	if s := store.Snapshot(); s.AuthToken != "access-1" || s.RefreshToken != "refresh-1" {
		t.Fatalf("unexpected session tokens: %#v", s)
	}
}

func TestEnsureAuthorizedForceSkipsFreshlyRotatedToken(t *testing.T) {
	srv, calls := newRotatingTokenServer(t, "bootstrap")
	store := session.NewStore(t.TempDir() + "/session.json")
	c := NewClient(store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	c.baseURL = srv.URL

	if err := c.EnsureAuthorized(context.Background(), "bootstrap", false); err != nil {
		t.Fatal(err)
	}
	// A force right after a successful refresh means another goroutine already
	// re-authorized; it must not consume the fresh rotation again.
	if err := c.EnsureAuthorized(context.Background(), "bootstrap", true); err != nil {
		t.Fatal(err)
	}
	if got := calls(); got != 1 {
		t.Fatalf("refresh calls=%d, want 1", got)
	}
}
