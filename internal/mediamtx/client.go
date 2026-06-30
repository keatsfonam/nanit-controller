package mediamtx

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

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
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("mediamtx paths list status %d", res.StatusCode)
	}
	var pr pathsResponse
	if err := json.NewDecoder(res.Body).Decode(&pr); err != nil {
		return nil, err
	}
	return pr.Items, nil
}
