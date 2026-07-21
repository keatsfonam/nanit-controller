package mediamtx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const maxResponseSize = 1 << 20

type Client struct {
	base string
	http *http.Client
}

func New(base string) *Client {
	return &Client{base: strings.TrimRight(base, "/"), http: &http.Client{Timeout: 5 * time.Second}}
}

type pathsResponse struct {
	Items []Path `json:"items"`
}

type Path struct {
	Name   string          `json:"name"`
	Online *bool           `json:"online,omitempty"`
	Ready  bool            `json:"ready"`
	Source json.RawMessage `json:"source"`
}

func (c *Client) PathReady(ctx context.Context, name string) (bool, error) {
	paths, err := c.Paths(ctx)
	if err != nil {
		return false, err
	}
	for _, p := range paths {
		if p.Name == name {
			if p.Online != nil {
				return *p.Online, nil
			}
			return p.Ready || hasSource(p.Source), nil
		}
	}
	return false, nil
}

func hasSource(raw json.RawMessage) bool {
	s := strings.TrimSpace(string(raw))
	return s != "" && s != "null" && s != "{}"
}

func (c *Client) Paths(ctx context.Context) ([]Path, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/v3/paths/list", nil)
	if err != nil {
		return nil, err
	}
	res, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("mediamtx paths list status %d", res.StatusCode)
	}

	limited := &io.LimitedReader{R: res.Body, N: maxResponseSize + 1}
	decoder := json.NewDecoder(limited)
	var pr pathsResponse
	if err := decoder.Decode(&pr); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("mediamtx response contains multiple JSON values")
		}
		return nil, err
	}
	if limited.N == 0 {
		return nil, fmt.Errorf("mediamtx response exceeds %d bytes", maxResponseSize)
	}
	return pr.Items, nil
}
