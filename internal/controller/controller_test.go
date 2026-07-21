package controller

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/keatsfonam/nanit-controller/internal/config"
	"github.com/keatsfonam/nanit-controller/internal/nanit"
	"github.com/keatsfonam/nanit-controller/internal/protocol/nanitpb"
	"github.com/keatsfonam/nanit-controller/internal/session"
)

type fakeNanitService struct {
	mu          sync.Mutex
	forceCalls  int
	fetchCalls  int
	failFetches int
	babies      []session.Baby
	authErr     error
}

func (f *fakeNanitService) EnsureAuthorized(_ context.Context, _, rejectedAccessToken string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if rejectedAccessToken != "" {
		f.forceCalls++
	}
	return f.authErr
}

func (f *fakeNanitService) FetchBabies(context.Context, string) ([]session.Baby, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fetchCalls++
	if f.fetchCalls <= f.failFetches {
		return nil, errors.New("nanit api unavailable")
	}
	return f.babies, nil
}

func (f *fakeNanitService) counts() (fetch, force int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.fetchCalls, f.forceCalls
}

type fakeMediaChecker struct {
	ready   bool
	err     error
	onCheck func()
}

func (f fakeMediaChecker) PathReady(context.Context, string) (bool, error) {
	if f.onCheck != nil {
		f.onCheck()
	}
	return f.ready, f.err
}

type fakeStreamConn struct {
	mu      sync.Mutex
	done    chan struct{}
	closed  bool
	calls   []nanitpb.Streaming_Status
	errFor  map[nanitpb.Streaming_Status]error
	onSend  func(status nanitpb.Streaming_Status)
	onClose func()
}

func newFakeStreamConn() *fakeStreamConn {
	return &fakeStreamConn{done: make(chan struct{}), errFor: map[nanitpb.Streaming_Status]error{}}
}

func (f *fakeStreamConn) SendStreaming(_ context.Context, _ string, status nanitpb.Streaming_Status, _ time.Duration) error {
	f.mu.Lock()
	f.calls = append(f.calls, status)
	err := f.errFor[status]
	onSend := f.onSend
	f.mu.Unlock()
	if onSend != nil {
		onSend(status)
	}
	return err
}

func (f *fakeStreamConn) Done() <-chan struct{} { return f.done }

func (f *fakeStreamConn) Close() error {
	f.mu.Lock()
	if !f.closed {
		f.closed = true
		close(f.done)
	}
	onClose := f.onClose
	f.mu.Unlock()
	if onClose != nil {
		onClose()
	}
	return nil
}

func (f *fakeStreamConn) Calls() []nanitpb.Streaming_Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]nanitpb.Streaming_Status(nil), f.calls...)
}

func testController(t *testing.T, cfg config.Config) *Controller {
	t.Helper()
	store := session.NewStore(t.TempDir() + "/session.json")
	return &Controller{
		cfg:      cfg,
		store:    store,
		nanit:    &fakeNanitService{},
		mediamtx: fakeMediaChecker{},
		log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		status:   NewStatusRegistry(),
		sleep:    func(context.Context, time.Duration) {},
	}
}

func testConfig() config.Config {
	return config.Config{
		BootstrapRefreshToken:          "refresh",
		BabyUIDs:                       []string{"baby"},
		RTMPPublicAddr:                 "192.168.130.129:1935",
		RTMPPathPrefix:                 "/local",
		MediaMTXAPIURL:                 "http://127.0.0.1:9997",
		CheckInterval:                  time.Millisecond,
		MissingGrace:                   time.Millisecond,
		ReRequestInterval:              time.Millisecond,
		MissingPublisherRestartRetries: 3,
		RetryBackoffInitial:            time.Millisecond,
		RetryBackoffMax:                time.Second,
		ConnectionLimitBackoff:         5 * time.Millisecond,
		RequestTimeout:                 time.Second,
	}
}

func TestRunBabyClosesWebsocketBeforeConnectionLimitBackoff(t *testing.T) {
	cfg := testConfig()
	ctrl := testController(t, cfg)
	baby := session.Baby{UID: "baby", CameraUID: "camera", Name: "Baby"}
	ctrl.status.RegisterBaby(baby.UID, cfg.PathName(baby.UID))
	ws := newFakeStreamConn()
	ws.errFor[nanitpb.Streaming_STARTED] = nanit.ErrConnectionLimit

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctrl.dialWS = func(context.Context, string, string, *slog.Logger) (streamConn, error) { return ws, nil }
	ctrl.sleep = func(context.Context, time.Duration) {
		if !ws.closed {
			t.Error("backoff sleep started before websocket was closed")
		}
		cancel()
	}

	ctrl.runBaby(ctx, baby)
	if !ws.closed {
		t.Fatal("expected websocket to be closed")
	}
	st := ctrl.status.Snapshot().Cameras[0]
	if st.ConsecutiveConnectionLimitFailures != 1 {
		t.Fatalf("expected one connection-limit failure, got %d", st.ConsecutiveConnectionLimitFailures)
	}
}

func TestReconcileSendsStoppedThenStartedAfterMissingPublisherRetries(t *testing.T) {
	cfg := testConfig()
	cfg.MissingPublisherRestartRetries = 2
	ctrl := testController(t, cfg)
	ctrl.mediamtx = fakeMediaChecker{ready: false}
	baby := session.Baby{UID: "baby", CameraUID: "camera", Name: "Baby"}
	ctrl.status.RegisterBaby(baby.UID, cfg.PathName(baby.UID))
	ws := newFakeStreamConn()
	stopAfterResetStarted := false
	ws.onSend = func(status nanitpb.Streaming_Status) {
		calls := ws.Calls()
		if len(calls) >= 4 && calls[len(calls)-2] == nanitpb.Streaming_STOPPED && status == nanitpb.Streaming_STARTED {
			stopAfterResetStarted = true
			_ = ws.Close()
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	out := ctrl.reconcile(ctx, baby, ws, ctrl.log, newExponentialBackoff(time.Second, 0, nil))
	if out != reconcileDisconnected || !stopAfterResetStarted {
		t.Fatalf("unexpected outcome=%s stopAfterResetStarted=%v", out, stopAfterResetStarted)
	}
	want := []nanitpb.Streaming_Status{nanitpb.Streaming_STARTED, nanitpb.Streaming_STARTED, nanitpb.Streaming_STARTED, nanitpb.Streaming_STOPPED, nanitpb.Streaming_STARTED}
	if got := ws.Calls(); !reflect.DeepEqual(got[:len(want)], want) {
		t.Fatalf("unexpected streaming requests: got %v want prefix %v", got, want)
	}
}

func TestReconcileStartsMissingGraceAtFirstObservedAbsence(t *testing.T) {
	cfg := testConfig()
	cfg.CheckInterval = 20 * time.Millisecond
	cfg.MissingGrace = time.Millisecond
	cfg.ReRequestInterval = time.Nanosecond
	cfg.MissingPublisherRestartRetries = 0
	ctrl := testController(t, cfg)
	baby := session.Baby{UID: "baby", CameraUID: "camera", Name: "Baby"}
	ctrl.status.RegisterBaby(baby.UID, cfg.PathName(baby.UID))
	ws := newFakeStreamConn()
	ctrl.mediamtx = fakeMediaChecker{ready: false, onCheck: func() { _ = ws.Close() }}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	out := ctrl.reconcile(ctx, baby, ws, ctrl.log, newExponentialBackoff(time.Second, 0, nil))
	if out != reconcileDisconnected {
		t.Fatalf("outcome = %s, want %s", out, reconcileDisconnected)
	}
	if got := ws.Calls(); !reflect.DeepEqual(got, []nanitpb.Streaming_Status{nanitpb.Streaming_STARTED}) {
		t.Fatalf("streaming requests = %v, want only initial request", got)
	}
}

func TestReconcileReconnectsAfterConsecutiveSendFailures(t *testing.T) {
	cfg := testConfig()
	cfg.MissingPublisherRestartRetries = 0
	ctrl := testController(t, cfg)
	ctrl.mediamtx = fakeMediaChecker{ready: false}
	baby := session.Baby{UID: "baby", CameraUID: "camera", Name: "Baby"}
	ctrl.status.RegisterBaby(baby.UID, cfg.PathName(baby.UID))
	ws := newFakeStreamConn()
	ws.errFor[nanitpb.Streaming_STARTED] = errors.New("stream request rejected")

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	out := ctrl.reconcile(ctx, baby, ws, ctrl.log, newExponentialBackoff(time.Second, 0, nil))
	if out != reconcileRequestsFailing {
		t.Fatalf("outcome=%s, want %s", out, reconcileRequestsFailing)
	}
	if got := len(ws.Calls()); got != maxConsecutiveSendFailures {
		t.Fatalf("streaming requests=%d, want %d", got, maxConsecutiveSendFailures)
	}
}

func TestRunInitializesDegradedStatusForInvalidDiscovery(t *testing.T) {
	tests := []struct {
		name   string
		babies []session.Baby
		state  string
	}{
		{name: "baby missing", state: "baby_not_found"},
		{name: "camera UID missing", babies: []session.Baby{{UID: "baby"}}, state: "camera_uid_missing"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := testConfig()
			cfg.BabyUIDs = []string{"baby"}
			ctrl := testController(t, cfg)
			ctrl.nanit = &fakeNanitService{babies: tt.babies}
			ctx, cancel := context.WithCancel(context.Background())
			errCh := make(chan error, 1)
			go func() { errCh <- ctrl.Run(ctx) }()

			deadline := time.NewTimer(time.Second)
			defer deadline.Stop()
			ticker := time.NewTicker(time.Millisecond)
			defer ticker.Stop()
			for {
				snapshot := ctrl.Status().Snapshot()
				if snapshot.Status == "degraded" && len(snapshot.Cameras) == 1 {
					if snapshot.Cameras[0].State != tt.state {
						t.Fatalf("unexpected camera status: %#v", snapshot.Cameras[0])
					}
					break
				}
				select {
				case <-deadline.C:
					t.Fatalf("controller did not initialize status: %#v", snapshot)
				case <-ticker.C:
				}
			}
			cancel()
			if err := <-errCh; !errors.Is(err, context.Canceled) {
				t.Fatalf("Run error = %v, want context canceled", err)
			}
		})
	}
}

func TestFetchBabiesFallsBackToCachedSession(t *testing.T) {
	ctrl := testController(t, testConfig())
	cached := []session.Baby{{UID: "baby", CameraUID: "camera", Name: "Baby"}}
	if err := ctrl.store.Update(func(s *session.Session) { s.Babies = cached }); err != nil {
		t.Fatal(err)
	}
	svc := &fakeNanitService{failFetches: 100}
	ctrl.nanit = svc

	babies, err := ctrl.fetchBabiesWithFallback(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(babies, cached) {
		t.Fatalf("babies=%#v, want cached %#v", babies, cached)
	}
	if fetch, _ := svc.counts(); fetch != 1 {
		t.Fatalf("fetch calls=%d, want 1", fetch)
	}
}

func TestFetchBabiesRetriesWithoutCache(t *testing.T) {
	ctrl := testController(t, testConfig())
	want := []session.Baby{{UID: "baby", CameraUID: "camera", Name: "Baby"}}
	svc := &fakeNanitService{failFetches: 2, babies: want}
	ctrl.nanit = svc

	babies, err := ctrl.fetchBabiesWithFallback(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(babies, want) {
		t.Fatalf("babies=%#v, want %#v", babies, want)
	}
	if fetch, _ := svc.counts(); fetch != 3 {
		t.Fatalf("fetch calls=%d, want 3", fetch)
	}
}

func TestRunBabyForcesRefreshOnlyOnAuthDialFailure(t *testing.T) {
	cases := []struct {
		name       string
		dialErr    error
		wantForced int
	}{
		{name: "network error", dialErr: errors.New("connection refused"), wantForced: 0},
		{name: "auth error", dialErr: fmt.Errorf("%w: status=401", nanit.ErrDialUnauthorized), wantForced: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testConfig()
			ctrl := testController(t, cfg)
			if err := ctrl.store.Update(func(s *session.Session) {
				s.AuthToken = "rejected-access-token"
				s.AuthTime = time.Now()
			}); err != nil {
				t.Fatal(err)
			}
			svc := &fakeNanitService{}
			ctrl.nanit = svc
			baby := session.Baby{UID: "baby", CameraUID: "camera", Name: "Baby"}
			ctrl.status.RegisterBaby(baby.UID, cfg.PathName(baby.UID))

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctrl.dialWS = func(context.Context, string, string, *slog.Logger) (streamConn, error) {
				return nil, tc.dialErr
			}
			ctrl.sleep = func(context.Context, time.Duration) { cancel() }

			ctrl.runBaby(ctx, baby)
			if _, forced := svc.counts(); forced != tc.wantForced {
				t.Fatalf("force refresh calls=%d, want %d", forced, tc.wantForced)
			}
		})
	}
}

func TestReconcileReturnsConnectionLimitDuringReset(t *testing.T) {
	cfg := testConfig()
	cfg.MissingPublisherRestartRetries = 1
	ctrl := testController(t, cfg)
	ctrl.mediamtx = fakeMediaChecker{ready: false}
	baby := session.Baby{UID: "baby", CameraUID: "camera", Name: "Baby"}
	ctrl.status.RegisterBaby(baby.UID, cfg.PathName(baby.UID))
	ws := newFakeStreamConn()
	ws.errFor[nanitpb.Streaming_STOPPED] = nanit.ErrConnectionLimit

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	out := ctrl.reconcile(ctx, baby, ws, ctrl.log, newExponentialBackoff(time.Second, 0, nil))
	if out != reconcileConnectionLimit {
		t.Fatalf("outcome=%s, want %s", out, reconcileConnectionLimit)
	}
	st := ctrl.status.Snapshot().Cameras[0]
	if st.ConsecutiveConnectionLimitFailures != 1 || st.State != "connection_limited" {
		t.Fatalf("unexpected status: %#v", st)
	}
}
