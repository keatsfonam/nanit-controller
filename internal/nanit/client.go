package nanit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/keatsfonam/nanit-controller/internal/session"
)

var ErrExpiredRefreshToken = errors.New("refresh token expired")

type Client struct {
	http    *http.Client
	store   *session.Store
	log     *slog.Logger
	baseURL string
	// refreshMu serializes token refreshes: Nanit rotates refresh tokens, so
	// concurrent refreshes with the same token can invalidate the session.
	refreshMu sync.Mutex
}

type authResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

type babiesResponse struct {
	Babies []session.Baby `json:"babies"`
}

func NewClient(store *session.Store, log *slog.Logger) *Client {
	return &Client{http: &http.Client{Timeout: 15 * time.Second}, store: store, log: log, baseURL: "https://api.nanit.com"}
}

func (c *Client) EnsureAuthorized(ctx context.Context, bootstrapRefreshToken string, force bool) error {
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()
	// Re-check under the lock: another goroutine may have refreshed while we waited.
	s := c.store.Snapshot()
	if s.AuthToken != "" {
		age := time.Since(s.AuthTime)
		if !force && age < 45*time.Minute {
			return nil
		}
		// A token this fresh postdates whatever failure prompted the force.
		if force && age < 30*time.Second {
			return nil
		}
	}
	refresh := strings.TrimSpace(s.RefreshToken)
	if refresh == "" {
		refresh = strings.TrimSpace(bootstrapRefreshToken)
	}
	if refresh == "" {
		return errors.New("no refresh token in session or bootstrap secret")
	}
	return c.refresh(ctx, refresh)
}

func (c *Client) refresh(ctx context.Context, refreshToken string) error {
	body, err := json.Marshal(map[string]string{"refresh_token": refreshToken})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/tokens/refresh", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return ErrExpiredRefreshToken
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		return fmt.Errorf("refresh token request failed: status=%d body=%q", res.StatusCode, string(b))
	}
	var ar authResponse
	if err := json.NewDecoder(res.Body).Decode(&ar); err != nil {
		return err
	}
	if ar.AccessToken == "" || ar.RefreshToken == "" {
		return errors.New("refresh response missing token")
	}
	if err := c.store.Update(func(s *session.Session) {
		s.AuthToken = ar.AccessToken
		s.RefreshToken = ar.RefreshToken
		s.AuthTime = time.Now()
	}); err != nil {
		return err
	}
	c.log.Info("authorized", "refresh_token_suffix", tokenSuffix(ar.RefreshToken), "access_token_suffix", tokenSuffix(ar.AccessToken))
	return nil
}

func (c *Client) FetchBabies(ctx context.Context, bootstrapRefreshToken string) ([]session.Baby, error) {
	for attempt := 0; attempt < 2; attempt++ {
		if err := c.EnsureAuthorized(ctx, bootstrapRefreshToken, attempt > 0); err != nil {
			return nil, err
		}
		s := c.store.Snapshot()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/babies", nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", s.AuthToken)
		res, err := c.http.Do(req)
		if err != nil {
			return nil, err
		}
		if res.StatusCode == http.StatusUnauthorized {
			_ = res.Body.Close()
			continue
		}
		if res.StatusCode != http.StatusOK {
			b, _ := io.ReadAll(io.LimitReader(res.Body, 512))
			_ = res.Body.Close()
			return nil, fmt.Errorf("fetch babies failed: status=%d body=%q", res.StatusCode, string(b))
		}
		var br babiesResponse
		err = json.NewDecoder(res.Body).Decode(&br)
		_ = res.Body.Close()
		if err != nil {
			return nil, err
		}
		_ = c.store.Update(func(s *session.Session) { s.Babies = br.Babies })
		return br.Babies, nil
	}
	return nil, errors.New("fetch babies failed after token refresh")
}

func tokenSuffix(v string) string {
	if len(v) <= 4 {
		return "****"
	}
	return "..." + v[len(v)-4:]
}
