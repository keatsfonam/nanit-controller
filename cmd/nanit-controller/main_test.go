package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/keatsfonam/nanit-controller/internal/controller"
)

func TestHealthRoutes(t *testing.T) {
	status := controller.NewStatusRegistry()
	srv := httptest.NewServer(newHealthServer(":0", status).Handler)
	t.Cleanup(srv.Close)

	for _, tc := range []struct {
		path string
		code int
		body string
	}{
		{path: "/healthz", code: http.StatusOK, body: "ok\n"},
		{path: "/readyz", code: http.StatusServiceUnavailable},
		{path: "/statusz", code: http.StatusOK},
	} {
		res, err := http.Get(srv.URL + tc.path)
		if err != nil {
			t.Fatal(err)
		}
		body, readErr := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		if res.StatusCode != tc.code {
			t.Errorf("%s status = %d, want %d", tc.path, res.StatusCode, tc.code)
		}
		if tc.body != "" && string(body) != tc.body {
			t.Errorf("%s body = %q, want %q", tc.path, body, tc.body)
		}
	}
}
