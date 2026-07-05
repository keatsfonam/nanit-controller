package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/keatsfonam/nanit-controller/internal/session"
)

func TestStatusRegistryServesReadyzJSONWithHTTP200(t *testing.T) {
	registry := NewStatusRegistry()
	registry.RegisterBaby(session.Baby{UID: "baby", CameraUID: "camera", Name: "Baby"}, "local/baby", "rtmp://relay/local/baby")
	registry.Update("baby", func(st *CameraStatus) {
		st.State = "connection_limited"
		st.LastError = "nanit mobile app connection limit"
		st.ConsecutiveConnectionLimitFailures = 2
	})

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	res := httptest.NewRecorder()
	registry.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status code=%d, want 200", res.Code)
	}
	if got := res.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("content-type=%q", got)
	}
	var snapshot StatusSnapshot
	if err := json.Unmarshal(res.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != "degraded" || len(snapshot.Cameras) != 1 {
		t.Fatalf("unexpected snapshot: %#v", snapshot)
	}
	cam := snapshot.Cameras[0]
	if cam.BabyUID != "baby" || cam.State != "connection_limited" || cam.ConsecutiveConnectionLimitFailures != 2 {
		t.Fatalf("unexpected camera status: %#v", cam)
	}
}
