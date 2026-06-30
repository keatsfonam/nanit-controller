package nanit

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	indie "github.com/indiefan/home_assistant_nanit/pkg/client"
	"google.golang.org/protobuf/proto"
)

var ErrConnectionLimit = errors.New("nanit mobile app connection limit")

type WS struct {
	conn      *websocket.Conn
	log       *slog.Logger
	nextID    int32
	pendingMu sync.Mutex
	pending   map[int32]chan responseResult
	closed    chan struct{}
	closeOnce sync.Once
}

type responseResult struct {
	res *indie.Response
	err error
}

func DialWS(ctx context.Context, cameraUID, accessToken string, log *slog.Logger) (*WS, error) {
	url := fmt.Sprintf("wss://api.nanit.com/focus/cameras/%s/user_connect", cameraUID)
	header := http.Header{}
	header.Set("Authorization", "Bearer "+accessToken)
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, url, header)
	if err != nil {
		return nil, err
	}
	w := &WS{conn: conn, log: log.With("camera_uid", cameraUID), pending: map[int32]chan responseResult{}, closed: make(chan struct{})}
	go w.readLoop()
	go w.keepAliveLoop()
	w.log.Info("connected to Nanit websocket")
	return w, nil
}

func (w *WS) Close() error {
	w.closeOnce.Do(func() { close(w.closed) })
	return w.conn.Close()
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
			msg := &indie.Message{Type: indie.Message_Type(indie.Message_KEEPALIVE).Enum()}
			if err := w.send(msg); err != nil {
				w.log.Warn("keepalive failed", "error", err)
				_ = w.Close()
				return
			}
		}
	}
}

func (w *WS) readLoop() {
	defer w.Close()
	for {
		_, data, err := w.conn.ReadMessage()
		if err != nil {
			w.log.Warn("websocket read failed", "error", err)
			w.failAll(err)
			return
		}
		var msg indie.Message
		if err := proto.Unmarshal(data, &msg); err != nil {
			w.log.Warn("malformed websocket protobuf", "error", err)
			continue
		}
		if msg.Type != nil && *msg.Type == indie.Message_RESPONSE && msg.Response != nil {
			w.handleResponse(msg.Response)
		}
	}
}

func (w *WS) handleResponse(res *indie.Response) {
	if res.RequestId == nil {
		return
	}
	id := *res.RequestId
	w.pendingMu.Lock()
	ch := w.pending[id]
	delete(w.pending, id)
	w.pendingMu.Unlock()
	if ch == nil {
		return
	}
	if res.StatusCode == nil {
		ch <- responseResult{res: res, err: errors.New("response missing status code")}
		return
	}
	if *res.StatusCode != http.StatusOK {
		msg := res.GetStatusMessage()
		if msg == "" {
			msg = fmt.Sprintf("unexpected status code %d", *res.StatusCode)
		}
		err := errors.New(msg)
		if msg == "Forbidden: Number of Mobile App connections above limit, declining connection" {
			err = fmt.Errorf("%w: %s", ErrConnectionLimit, msg)
		}
		ch <- responseResult{res: res, err: err}
		return
	}
	ch <- responseResult{res: res}
}

func (w *WS) failAll(err error) {
	w.pendingMu.Lock()
	defer w.pendingMu.Unlock()
	for id, ch := range w.pending {
		delete(w.pending, id)
		ch <- responseResult{err: err}
	}
}

func (w *WS) SendStreaming(ctx context.Context, rtmpURL string, status indie.Streaming_Status, timeout time.Duration) error {
	id := atomic.AddInt32(&w.nextID, 1)
	msg := &indie.Message{
		Type: indie.Message_Type(indie.Message_REQUEST).Enum(),
		Request: &indie.Request{
			Id:   &id,
			Type: indie.RequestType(indie.RequestType_PUT_STREAMING).Enum(),
			Streaming: &indie.Streaming{
				Id:       indie.StreamIdentifier(indie.StreamIdentifier_MOBILE).Enum(),
				RtmpUrl:  &rtmpURL,
				Status:   indie.Streaming_Status(status).Enum(),
				Attempts: int32p(1),
			},
		},
	}
	ch := make(chan responseResult, 1)
	w.pendingMu.Lock()
	w.pending[id] = ch
	w.pendingMu.Unlock()
	if err := w.send(msg); err != nil {
		w.pendingMu.Lock()
		delete(w.pending, id)
		w.pendingMu.Unlock()
		return err
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-w.closed:
		return errors.New("websocket closed")
	case <-timer.C:
		w.pendingMu.Lock()
		delete(w.pending, id)
		w.pendingMu.Unlock()
		return errors.New("request timeout")
	case rr := <-ch:
		return rr.err
	}
}

func (w *WS) send(msg *indie.Message) error {
	b, err := proto.Marshal(msg)
	if err != nil {
		return err
	}
	return w.conn.WriteMessage(websocket.BinaryMessage, b)
}

func int32p(v int32) *int32 { return &v }
