package controller

import (
	"encoding/json"
	"net/http"
	"sort"
	"sync"
	"time"
)

type CameraStatus struct {
	BabyUID                            string     `json:"baby_uid"`
	Path                               string     `json:"path,omitempty"`
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
	mu          sync.RWMutex
	cameras     map[string]CameraStatus
	initialized bool
}

func NewStatusRegistry() *StatusRegistry {
	return &StatusRegistry{cameras: map[string]CameraStatus{}}
}

func (r *StatusRegistry) RegisterBaby(babyUID, path string) {
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	st := r.cameras[babyUID]
	st.BabyUID = babyUID
	st.Path = path
	if st.State == "" {
		st.State = "registered"
	}
	st.UpdatedAt = now
	r.cameras[babyUID] = st
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

func (r *StatusRegistry) SetInitialized() {
	r.mu.Lock()
	r.initialized = true
	r.mu.Unlock()
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
	for _, st := range r.cameras {
		cameras = append(cameras, st)
	}
	sort.Slice(cameras, func(i, j int) bool { return cameras[i].BabyUID < cameras[j].BabyUID })
	if !r.initialized {
		return StatusSnapshot{Status: "initializing", Cameras: cameras}
	}
	status := "ok"
	if len(cameras) == 0 {
		status = "degraded"
	}
	for _, st := range cameras {
		if st.State != "ready" {
			status = "degraded"
			break
		}
	}
	return StatusSnapshot{Status: status, Cameras: cameras}
}

// ServeHTTP is the readiness view. Startup is not ready until discovery has
// completed; camera-level degradation remains HTTP 200 to avoid restart churn.
func (r *StatusRegistry) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	snapshot := r.Snapshot()
	code := http.StatusOK
	if snapshot.Status == "initializing" {
		code = http.StatusServiceUnavailable
	}
	writeSnapshot(w, snapshot, code)
}

// ServeStatusHTTP always returns HTTP 200 for monitoring and diagnostics.
func (r *StatusRegistry) ServeStatusHTTP(w http.ResponseWriter, _ *http.Request) {
	writeSnapshot(w, r.Snapshot(), http.StatusOK)
}

func writeSnapshot(w http.ResponseWriter, snapshot StatusSnapshot, code int) {
	data, err := json.Marshal(snapshot)
	if err != nil {
		http.Error(w, "encode status", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = w.Write(append(data, '\n'))
}
