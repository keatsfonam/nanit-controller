package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReadinessIsUnavailableUntilInitialized(t *testing.T) {
	registry := NewStatusRegistry()
	res := httptest.NewRecorder()
	registry.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("status code = %d, want 503", res.Code)
	}
	var snapshot StatusSnapshot
	if err := json.Unmarshal(res.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != "initializing" || len(snapshot.Cameras) != 0 {
		t.Fatalf("unexpected snapshot: %#v", snapshot)
	}

	statusRes := httptest.NewRecorder()
	registry.ServeStatusHTTP(statusRes, httptest.NewRequest(http.MethodGet, "/statusz", nil))
	if statusRes.Code != http.StatusOK {
		t.Fatalf("status endpoint code = %d, want 200", statusRes.Code)
	}
}

func TestReadyzKeepsCameraDegradationAtHTTP200(t *testing.T) {
	registry := NewStatusRegistry()
	registry.RegisterBaby("baby", "local/baby")
	registry.SetInitialized()
	registry.Update("baby", func(st *CameraStatus) {
		st.State = "connection_limited"
		st.LastError = "nanit mobile app connection limit"
		st.ConsecutiveConnectionLimitFailures = 2
	})

	res := httptest.NewRecorder()
	registry.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200", res.Code)
	}
	if got := res.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("content-type = %q", got)
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

func TestSnapshotSortsCamerasAndMinimizesIdentityData(t *testing.T) {
	registry := NewStatusRegistry()
	registry.RegisterBaby("z-baby", "local/z-baby")
	registry.RegisterBaby("a-baby", "local/a-baby")
	registry.Update("z-baby", func(st *CameraStatus) { st.State = "ready" })
	registry.Update("a-baby", func(st *CameraStatus) { st.State = "ready" })
	registry.SetInitialized()

	snapshot := registry.Snapshot()
	if snapshot.Status != "ok" || len(snapshot.Cameras) != 2 {
		t.Fatalf("unexpected snapshot: %#v", snapshot)
	}
	if snapshot.Cameras[0].BabyUID != "a-baby" || snapshot.Cameras[1].BabyUID != "z-baby" {
		t.Fatalf("camera order is not deterministic: %#v", snapshot.Cameras)
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"camera_uid", "baby_name", "rtmp_url"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("status JSON exposes %q: %s", forbidden, data)
		}
	}
}
