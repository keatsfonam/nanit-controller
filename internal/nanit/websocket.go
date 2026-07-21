package nanit

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/keatsfonam/nanit-controller/internal/protocol/nanitpb"
	"google.golang.org/protobuf/proto"
)

var (
	ErrConnectionLimit = errors.New("nanit mobile app connection limit")
	// ErrDialUnauthorized marks websocket handshakes rejected with 401/403,
	// i.e. failures a token refresh could fix.
	ErrDialUnauthorized = errors.New("nanit websocket dial unauthorized")
)

const (
	wsReadLimit    = 1 << 20
	wsReadTimeout  = 75 * time.Second
	wsWriteTimeout = 10 * time.Second
)

type WS struct {
	conn      *websocket.Conn
	log       *slog.Logger
	nextID    int32
	writeMu   sync.Mutex
	pendingMu sync.Mutex
	pending   map[int32]chan responseResult
	closed    chan struct{}
	closeOnce sync.Once
	closeErr  error
}

type responseResult struct {
	err error
}

func DialWS(ctx context.Context, cameraUID, accessToken string, log *slog.Logger) (*WS, error) {
	endpoint := fmt.Sprintf("wss://api.nanit.com/focus/cameras/%s/user_connect", url.PathEscape(cameraUID))
	return dialWS(ctx, endpoint, accessToken, log)
}

func dialWS(ctx context.Context, endpoint, accessToken string, log *slog.Logger) (*WS, error) {
	header := http.Header{}
	header.Set("Authorization", "Bearer "+accessToken)
	conn, res, err := websocket.DefaultDialer.DialContext(ctx, endpoint, header)
	if err != nil {
		if res != nil && (res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden) {
			return nil, fmt.Errorf("%w: status=%d: %v", ErrDialUnauthorized, res.StatusCode, err)
		}
		return nil, err
	}
	conn.SetReadLimit(wsReadLimit)
	_ = conn.SetReadDeadline(time.Now().Add(wsReadTimeout))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(wsReadTimeout))
	})
	conn.SetPingHandler(func(appData string) error {
		_ = conn.SetReadDeadline(time.Now().Add(wsReadTimeout))
		return conn.WriteControl(websocket.PongMessage, []byte(appData), time.Now().Add(wsWriteTimeout))
	})
	w := &WS{
		conn:    conn,
		log:     log.With("component", "websocket"),
		pending: map[int32]chan responseResult{},
		closed:  make(chan struct{}),
	}
	go w.readLoop()
	go w.keepAliveLoop()
	w.log.Info("connected to Nanit websocket")
	return w, nil
}

func (w *WS) Close() error {
	w.closeOnce.Do(func() {
		close(w.closed)
		w.closeErr = w.conn.Close()
	})
	return w.closeErr
}

func (w *WS) Done() <-chan struct{} { return w.closed }

func (w *WS) keepAliveLoop() {
	t := time.NewTicker(20 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-w.closed:
			return
		case <-t.C:
			msg := &nanitpb.Message{Type: nanitpb.Message_KEEPALIVE.Enum()}
			if err := w.send(msg); err != nil {
				w.log.Warn("keepalive failed", "error", err)
				_ = w.Close()
				return
			}
			// Nanit does not answer app-level keepalives; a WebSocket pong
			// refreshes the read deadline and detects half-open connections.
			if err := w.conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(wsWriteTimeout)); err != nil {
				w.log.Warn("ping failed", "error", err)
				_ = w.Close()
				return
			}
		}
	}
}

func (w *WS) readLoop() {
	defer func() { _ = w.Close() }()
	for {
		_, data, err := w.conn.ReadMessage()
		if err != nil {
			select {
			case <-w.closed:
				w.log.Debug("websocket reader stopped")
			default:
				w.log.Warn("websocket read failed", "error", err)
			}
			w.failAll(err)
			return
		}
		_ = w.conn.SetReadDeadline(time.Now().Add(wsReadTimeout))
		var msg nanitpb.Message
		if err := proto.Unmarshal(data, &msg); err != nil {
			w.log.Warn("malformed websocket protobuf", "error", err)
			continue
		}
		if msg.Type != nil && *msg.Type == nanitpb.Message_RESPONSE && msg.Response != nil {
			w.handleResponse(msg.Response)
		}
	}
}

func (w *WS) handleResponse(res *nanitpb.Response) {
	if res.RequestId == nil {
		return
	}
	ch := w.removePending(*res.RequestId)
	if ch == nil {
		return
	}
	ch <- responseResult{err: responseError(res)}
}

func responseError(res *nanitpb.Response) error {
	if res.StatusCode == nil {
		return errors.New("response missing status code")
	}
	if *res.StatusCode == http.StatusOK {
		return nil
	}
	if strings.Contains(strings.ToLower(res.GetStatusMessage()), "mobile app connections above limit") {
		return ErrConnectionLimit
	}
	return fmt.Errorf("websocket request rejected: status=%d", *res.StatusCode)
}

func (w *WS) failAll(err error) {
	w.pendingMu.Lock()
	defer w.pendingMu.Unlock()
	for id, ch := range w.pending {
		delete(w.pending, id)
		ch <- responseResult{err: err}
	}
}

func (w *WS) removePending(id int32) chan responseResult {
	w.pendingMu.Lock()
	defer w.pendingMu.Unlock()
	ch := w.pending[id]
	delete(w.pending, id)
	return ch
}

func (w *WS) SendStreaming(ctx context.Context, rtmpURL string, status nanitpb.Streaming_Status, timeout time.Duration) error {
	id := atomic.AddInt32(&w.nextID, 1)
	msg := &nanitpb.Message{
		Type: nanitpb.Message_REQUEST.Enum(),
		Request: &nanitpb.Request{
			Id:   &id,
			Type: nanitpb.RequestType_PUT_STREAMING.Enum(),
			Streaming: &nanitpb.Streaming{
				Id:       nanitpb.StreamIdentifier_MOBILE.Enum(),
				RtmpUrl:  &rtmpURL,
				Status:   status.Enum(),
				Attempts: int32p(1),
			},
		},
	}
	ch := make(chan responseResult, 1)
	w.pendingMu.Lock()
	w.pending[id] = ch
	w.pendingMu.Unlock()
	if err := w.send(msg); err != nil {
		w.removePending(id)
		return err
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		w.removePending(id)
		return ctx.Err()
	case <-w.closed:
		w.removePending(id)
		return errors.New("websocket closed")
	case <-timer.C:
		w.removePending(id)
		return errors.New("request timeout")
	case rr := <-ch:
		return rr.err
	}
}

func (w *WS) send(msg *nanitpb.Message) error {
	select {
	case <-w.closed:
		return errors.New("websocket closed")
	default:
	}
	b, err := proto.Marshal(msg)
	if err != nil {
		return err
	}
	w.writeMu.Lock()
	defer w.writeMu.Unlock()
	_ = w.conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
	return w.conn.WriteMessage(websocket.BinaryMessage, b)
}

func int32p(v int32) *int32 { return &v }
