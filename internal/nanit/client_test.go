package nanit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/keatsfonam/nanit-controller/internal/session"
)

// Nanit rotates refresh tokens: every accepted token is single-use.
func newRotatingTokenServer(t *testing.T, bootstrap string) (*httptest.Server, func() int) {
	t.Helper()
	var mu sync.Mutex
	calls := 0
	valid := bootstrap
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tokens/refresh" {
			http.NotFound(w, r)
			return
		}
		var body struct {
			RefreshToken string `json:"refresh_token"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
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

func newTestClient(store *session.Store) *Client {
	return NewClient(store, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestEnsureAuthorizedSingleFlightsConcurrentRefreshes(t *testing.T) {
	srv, calls := newRotatingTokenServer(t, "bootstrap")
	store := session.NewStore(filepath.Join(t.TempDir(), "session.json"))
	c := newTestClient(store)
	c.baseURL = srv.URL

	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = c.EnsureAuthorized(context.Background(), "bootstrap", "")
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d: %v", i, err)
		}
	}
	if got := calls(); got != 1 {
		t.Fatalf("refresh calls = %d, want 1", got)
	}
	if s := store.Snapshot(); s.AuthToken != "access-1" || s.RefreshToken != "refresh-1" {
		t.Fatalf("unexpected session tokens: %#v", s)
	}
}

func TestEnsureAuthorizedComparesRejectedToken(t *testing.T) {
	srv, calls := newRotatingTokenServer(t, "bootstrap")
	store := session.NewStore(filepath.Join(t.TempDir(), "session.json"))
	c := newTestClient(store)
	c.baseURL = srv.URL

	if err := c.EnsureAuthorized(context.Background(), "bootstrap", ""); err != nil {
		t.Fatal(err)
	}
	if err := c.EnsureAuthorized(context.Background(), "bootstrap", "already-replaced"); err != nil {
		t.Fatal(err)
	}
	if got := calls(); got != 1 {
		t.Fatalf("refresh calls after stale rejection = %d, want 1", got)
	}
	if err := c.EnsureAuthorized(context.Background(), "bootstrap", "access-1"); err != nil {
		t.Fatal(err)
	}
	if got := calls(); got != 2 {
		t.Fatalf("refresh calls after current-token rejection = %d, want 2", got)
	}
}

func TestEnsureAuthorizedTreatsFutureAuthTimeAsStale(t *testing.T) {
	srv, calls := newRotatingTokenServer(t, "bootstrap")
	store := session.NewStore(filepath.Join(t.TempDir(), "session.json"))
	if err := store.Update(func(s *session.Session) {
		s.AuthToken = "expired"
		s.RefreshToken = "bootstrap"
		s.AuthTime = time.Now().Add(time.Hour)
	}); err != nil {
		t.Fatal(err)
	}
	c := newTestClient(store)
	c.baseURL = srv.URL

	if err := c.EnsureAuthorized(context.Background(), "", ""); err != nil {
		t.Fatal(err)
	}
	if got := calls(); got != 1 {
		t.Fatalf("refresh calls = %d, want 1", got)
	}
}

func TestFetchBabiesRefreshesNewlyRejectedToken(t *testing.T) {
	var mu sync.Mutex
	refreshCalls := 0
	babiesCalls := 0
	validRefresh := "bootstrap"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/tokens/refresh":
			var body struct {
				RefreshToken string `json:"refresh_token"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			refreshCalls++
			if body.RefreshToken != validRefresh {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			validRefresh = fmt.Sprintf("refresh-%d", refreshCalls)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"access_token":  fmt.Sprintf("access-%d", refreshCalls),
				"refresh_token": validRefresh,
			})
		case "/babies":
			babiesCalls++
			if r.Header.Get("Authorization") != "access-2" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"babies": []map[string]string{{"uid": "baby", "camera_uid": "camera"}}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	store := session.NewStore(filepath.Join(t.TempDir(), "session.json"))
	c := newTestClient(store)
	c.baseURL = srv.URL
	babies, err := c.FetchBabies(context.Background(), "bootstrap")
	if err != nil {
		t.Fatal(err)
	}
	if len(babies) != 1 || babies[0].UID != "baby" {
		t.Fatalf("unexpected babies: %#v", babies)
	}
	mu.Lock()
	defer mu.Unlock()
	if refreshCalls != 2 || babiesCalls != 2 {
		t.Fatalf("refresh calls = %d, babies calls = %d; want 2 each", refreshCalls, babiesCalls)
	}
}

func TestEnsureAuthorizedMakesPersistenceFailureSticky(t *testing.T) {
	srv, calls := newRotatingTokenServer(t, "bootstrap")
	dir := t.TempDir()
	path := filepath.Join(dir, "session.json")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	store := session.NewStore(path)
	c := newTestClient(store)
	c.baseURL = srv.URL

	err := c.EnsureAuthorized(context.Background(), "bootstrap", "")
	if !errors.Is(err, ErrSessionPersistence) || !strings.Contains(err.Error(), "recoverable copy") {
		t.Fatalf("expected sticky persistence error with recovery path, got %v", err)
	}
	first := err.Error()
	if err := c.EnsureAuthorized(context.Background(), "bootstrap", ""); err == nil || err.Error() != first {
		t.Fatalf("second error = %v, want original sticky error", err)
	}
	if got := calls(); got != 1 {
		t.Fatalf("refresh calls = %d, want 1", got)
	}
	if got := store.Snapshot().AuthToken; got != "" {
		t.Fatalf("uncommitted access token published in memory: %q", got)
	}
}

func TestEnsureAuthorizedMakesUncertainRefreshSticky(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		_, _ = w.Write([]byte(`{"access_token":`))
	}))
	t.Cleanup(srv.Close)

	store := session.NewStore(filepath.Join(t.TempDir(), "session.json"))
	c := newTestClient(store)
	c.baseURL = srv.URL
	first := c.EnsureAuthorized(context.Background(), "bootstrap", "")
	if !errors.Is(first, ErrRefreshOutcomeUncertain) {
		t.Fatalf("first error = %v, want ErrRefreshOutcomeUncertain", first)
	}
	second := c.EnsureAuthorized(context.Background(), "bootstrap", "")
	if second == nil || second.Error() != first.Error() {
		t.Fatalf("second error = %v, want original sticky error", second)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("refresh calls = %d, want 1", calls)
	}
}

func TestFetchBabiesReturnsCachePersistenceError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.json")
	store := session.NewStore(path)
	if err := store.Update(func(s *session.Session) {
		s.AuthToken = "access"
		s.AuthTime = time.Now()
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"babies": []map[string]string{{"uid": "baby", "camera_uid": "camera"}}})
	}))
	t.Cleanup(srv.Close)
	c := newTestClient(store)
	c.baseURL = srv.URL

	if _, err := c.FetchBabies(context.Background(), ""); err == nil || !strings.Contains(err.Error(), "persist baby cache") {
		t.Fatalf("expected baby-cache persistence error, got %v", err)
	}
}
