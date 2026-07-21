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

var (
	ErrExpiredRefreshToken     = errors.New("refresh token expired")
	ErrRefreshOutcomeUncertain = errors.New("refresh token rotation outcome uncertain")
	ErrSessionPersistence      = errors.New("session persistence failed after token rotation")
)

const maxAPIResponseSize = 1 << 20

type Client struct {
	http    *http.Client
	store   *session.Store
	log     *slog.Logger
	baseURL string

	// refreshMu serializes token rotations. fatalErr makes a persistence failure
	// sticky so the client never retries a refresh token that Nanit consumed.
	refreshMu sync.Mutex
	fatalErr  error
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

// EnsureAuthorized refreshes a missing or stale access token. When an API call
// rejects a token, pass that exact token as rejectedAccessToken. If another
// goroutine rotated it while this call waited for refreshMu, no second rotation
// is needed.
func (c *Client) EnsureAuthorized(ctx context.Context, bootstrapRefreshToken, rejectedAccessToken string) error {
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()

	if c.fatalErr != nil {
		return c.fatalErr
	}
	s := c.store.Snapshot()
	if rejectedAccessToken != "" {
		if s.AuthToken != "" && s.AuthToken != rejectedAccessToken {
			return nil
		}
	} else if s.AuthToken != "" {
		age := time.Since(s.AuthTime)
		if age >= 0 && age < 45*time.Minute {
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
	if err := c.refresh(ctx, refresh); err != nil {
		if errors.Is(err, ErrRefreshOutcomeUncertain) || errors.Is(err, ErrSessionPersistence) {
			c.fatalErr = err
		}
		return err
	}
	return nil
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
		return fmt.Errorf("%w: request failed: %v", ErrRefreshOutcomeUncertain, err)
	}
	defer func() { _ = res.Body.Close() }()

	if res.StatusCode == http.StatusNotFound {
		return ErrExpiredRefreshToken
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return fmt.Errorf("%w: response status=%d", ErrRefreshOutcomeUncertain, res.StatusCode)
	}
	var ar authResponse
	if err := decodeJSON(res.Body, &ar); err != nil {
		return fmt.Errorf("%w: decode response: %v", ErrRefreshOutcomeUncertain, err)
	}
	if ar.AccessToken == "" || ar.RefreshToken == "" {
		return fmt.Errorf("%w: response missing token", ErrRefreshOutcomeUncertain)
	}
	if err := c.store.Update(func(s *session.Session) {
		s.AuthToken = ar.AccessToken
		s.RefreshToken = ar.RefreshToken
		s.AuthTime = time.Now()
	}); err != nil {
		return fmt.Errorf("%w: %v", ErrSessionPersistence, err)
	}
	c.log.Info("authorized")
	return nil
}

func (c *Client) FetchBabies(ctx context.Context, bootstrapRefreshToken string) ([]session.Baby, error) {
	rejectedAccessToken := ""
	for attempt := 0; attempt < 2; attempt++ {
		if err := c.EnsureAuthorized(ctx, bootstrapRefreshToken, rejectedAccessToken); err != nil {
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
			rejectedAccessToken = s.AuthToken
			continue
		}
		if res.StatusCode != http.StatusOK {
			_ = res.Body.Close()
			return nil, fmt.Errorf("fetch babies failed: status=%d", res.StatusCode)
		}
		var br babiesResponse
		err = decodeJSON(res.Body, &br)
		_ = res.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("decode babies response: %w", err)
		}
		if err := c.store.Update(func(s *session.Session) { s.Babies = br.Babies }); err != nil {
			return nil, fmt.Errorf("persist baby cache: %w", err)
		}
		return br.Babies, nil
	}
	return nil, errors.New("fetch babies failed after token refresh")
}

func decodeJSON(r io.Reader, dst any) error {
	limited := &io.LimitedReader{R: r, N: maxAPIResponseSize + 1}
	decoder := json.NewDecoder(limited)
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("response contains multiple JSON values")
		}
		return err
	}
	if limited.N == 0 {
		return fmt.Errorf("response exceeds %d bytes", maxAPIResponseSize)
	}
	return nil
}
