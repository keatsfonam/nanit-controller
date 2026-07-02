package controller

import (
	"context"
	"io"
	"log/slog"
	"reflect"
	"sync"
	"testing"
	"time"

	"git.keatsfonam.com/lab/nanit-controller/internal/config"
	"git.keatsfonam.com/lab/nanit-controller/internal/nanit"
	"git.keatsfonam.com/lab/nanit-controller/internal/session"
	indie "github.com/indiefan/home_assistant_nanit/pkg/client"
)

type fakeNanitService struct{}

func (fakeNanitService) EnsureAuthorized(context.Context, string, bool) error { return nil }
func (fakeNanitService) FetchBabies(context.Context, string) ([]session.Baby, error) {
	return nil, nil
}

type fakeMediaChecker struct {
	ready bool
	err   error
}

func (f fakeMediaChecker) PathReady(context.Context, string) (bool, error) { return f.ready, f.err }

type fakeStreamConn struct {
	mu      sync.Mutex
	done    chan struct{}
	closed  bool
	calls   []indie.Streaming_Status
	errFor  map[indie.Streaming_Status]error
	onSend  func(status indie.Streaming_Status)
	onClose func()
}

func newFakeStreamConn() *fakeStreamConn {
	return &fakeStreamConn{done: make(chan struct{}), errFor: map[indie.Streaming_Status]error{}}
}

func (f *fakeStreamConn) SendStreaming(_ context.Context, _ string, status indie.Streaming_Status, _ time.Duration) error {
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

func (f *fakeStreamConn) Calls() []indie.Streaming_Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]indie.Streaming_Status(nil), f.calls...)
}

func testController(t *testing.T, cfg config.Config) *Controller {
	t.Helper()
	store := session.NewStore(t.TempDir() + "/session.json")
	return &Controller{
		cfg:      cfg,
		store:    store,
		nanit:    fakeNanitService{},
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
	ctrl.status.RegisterBaby(baby, cfg.PathName(baby.UID), cfg.RTMPURL(baby.UID))
	ws := newFakeStreamConn()
	ws.errFor[indie.Streaming_STARTED] = nanit.ErrConnectionLimit

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
	ctrl.status.RegisterBaby(baby, cfg.PathName(baby.UID), cfg.RTMPURL(baby.UID))
	ws := newFakeStreamConn()
	stopAfterResetStarted := false
	ws.onSend = func(status indie.Streaming_Status) {
		calls := ws.Calls()
		if len(calls) >= 4 && calls[len(calls)-2] == indie.Streaming_STOPPED && status == indie.Streaming_STARTED {
			stopAfterResetStarted = true
			_ = ws.Close()
		}
	}

	out := ctrl.reconcile(context.Background(), baby, ws, ctrl.log, newExponentialBackoff(time.Second, 0, nil))
	if out != reconcileDisconnected || !stopAfterResetStarted {
		t.Fatalf("unexpected outcome=%s stopAfterResetStarted=%v", out, stopAfterResetStarted)
	}
	want := []indie.Streaming_Status{indie.Streaming_STARTED, indie.Streaming_STARTED, indie.Streaming_STOPPED, indie.Streaming_STARTED}
	if got := ws.Calls(); !reflect.DeepEqual(got[:len(want)], want) {
		t.Fatalf("unexpected streaming requests: got %v want prefix %v", got, want)
	}
}

func TestReconcileReturnsConnectionLimitDuringReset(t *testing.T) {
	cfg := testConfig()
	cfg.MissingPublisherRestartRetries = 1
	ctrl := testController(t, cfg)
	ctrl.mediamtx = fakeMediaChecker{ready: false}
	baby := session.Baby{UID: "baby", CameraUID: "camera", Name: "Baby"}
	ctrl.status.RegisterBaby(baby, cfg.PathName(baby.UID), cfg.RTMPURL(baby.UID))
	ws := newFakeStreamConn()
	ws.errFor[indie.Streaming_STOPPED] = nanit.ErrConnectionLimit

	out := ctrl.reconcile(context.Background(), baby, ws, ctrl.log, newExponentialBackoff(time.Second, 0, nil))
	if out != reconcileConnectionLimit {
		t.Fatalf("outcome=%s, want %s", out, reconcileConnectionLimit)
	}
	st := ctrl.status.Snapshot().Cameras[0]
	if st.ConsecutiveConnectionLimitFailures != 1 || st.State != "connection_limited" {
		t.Fatalf("unexpected status: %#v", st)
	}
}
