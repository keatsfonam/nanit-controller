package controller

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/keatsfonam/nanit-controller/internal/session"
)

type CameraStatus struct {
	BabyUID                            string     `json:"baby_uid"`
	CameraUID                          string     `json:"camera_uid,omitempty"`
	BabyName                           string     `json:"baby_name,omitempty"`
	Path                               string     `json:"path,omitempty"`
	RTMPURL                            string     `json:"rtmp_url,omitempty"`
	State                              string     `json:"state"`
	WebsocketConnected                 bool       `json:"websocket_connected"`
	PublisherPresent                   bool       `json:"publisher_present"`
	PublisherLastSeen                  *time.Time `json:"publisher_last_seen,omitempty"`
	MissingSince                       *time.Time `json:"missing_since,omitempty"`
	MissingRetryCount                  int        `json:"missing_retry_count"`
	LastRequestStatus                  string     `json:"last_request_status,omitempty"`
	LastRequestReason                  string     `json:"last_request_reason,omitempty"`
	LastRequestAt                      *time.Time `json:"last_request_at,omitempty"`
	LastSuccessAt                      *time.Time `json:"last_success_at,omitempty"`
	LastError                          string     `json:"last_error,omitempty"`
	ConsecutiveConnectionLimitFailures int        `json:"consecutive_connection_limit_failures"`
	BackoffUntil                       *time.Time `json:"backoff_until,omitempty"`
	NextRetryAt                        *time.Time `json:"next_retry_at,omitempty"`
	UpdatedAt                          time.Time  `json:"updated_at"`
}

type StatusSnapshot struct {
	Status  string         `json:"status"`
	Cameras []CameraStatus `json:"cameras"`
}

type StatusRegistry struct {
	mu      sync.RWMutex
	cameras map[string]CameraStatus
}

func NewStatusRegistry() *StatusRegistry {
	return &StatusRegistry{cameras: map[string]CameraStatus{}}
}

func (r *StatusRegistry) RegisterBaby(baby session.Baby, path, rtmpURL string) {
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	st := r.cameras[baby.UID]
	st.BabyUID = baby.UID
	st.CameraUID = baby.CameraUID
	st.BabyName = baby.Name
	st.Path = path
	st.RTMPURL = rtmpURL
	if st.State == "" {
		st.State = "registered"
	}
	st.UpdatedAt = now
	r.cameras[baby.UID] = st
}

func (r *StatusRegistry) RegisterMissing(uid string) {
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	st := r.cameras[uid]
	st.BabyUID = uid
	st.State = "baby_not_found"
	st.LastError = "configured baby UID not found in Nanit account"
	st.UpdatedAt = now
	r.cameras[uid] = st
}

func (r *StatusRegistry) Update(uid string, fn func(*CameraStatus)) {
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	st := r.cameras[uid]
	st.BabyUID = uid
	fn(&st)
	st.UpdatedAt = now
	r.cameras[uid] = st
}

func (r *StatusRegistry) Snapshot() StatusSnapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	cameras := make([]CameraStatus, 0, len(r.cameras))
	status := "ok"
	for _, st := range r.cameras {
		cameras = append(cameras, st)
		if st.State != "ready" && st.State != "registered" {
			status = "degraded"
		}
	}
	return StatusSnapshot{Status: status, Cameras: cameras}
}

func (r *StatusRegistry) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(r.Snapshot())
}
