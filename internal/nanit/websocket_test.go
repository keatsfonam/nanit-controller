package nanit

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/keatsfonam/nanit-controller/internal/protocol/nanitpb"
	"google.golang.org/protobuf/proto"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func websocketURL(server *httptest.Server) string {
	return "ws" + strings.TrimPrefix(server.URL, "http")
}

func TestDialWSAndSendStreaming(t *testing.T) {
	serverErr := make(chan error, 1)
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer access-token" {
			serverErr <- fmt.Errorf("authorization header = %q", got)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			serverErr <- err
			return
		}
		defer func() { _ = conn.Close() }()
		_, data, err := conn.ReadMessage()
		if err != nil {
			serverErr <- err
			return
		}
		var msg nanitpb.Message
		if err := proto.Unmarshal(data, &msg); err != nil {
			serverErr <- err
			return
		}
		request := msg.GetRequest()
		streaming := request.GetStreaming()
		if msg.GetType() != nanitpb.Message_REQUEST || request.GetType() != nanitpb.RequestType_PUT_STREAMING || streaming.GetId() != nanitpb.StreamIdentifier_MOBILE || streaming.GetStatus() != nanitpb.Streaming_STARTED || streaming.GetRtmpUrl() != "rtmp://relay:1935/local/baby" || streaming.GetAttempts() != 1 {
			serverErr <- fmt.Errorf("unexpected request: %v", &msg)
			return
		}
		code := int32(http.StatusOK)
		response := &nanitpb.Message{
			Type: nanitpb.Message_RESPONSE.Enum(),
			Response: &nanitpb.Response{
				RequestId:   request.Id,
				RequestType: nanitpb.RequestType_PUT_STREAMING.Enum(),
				StatusCode:  &code,
			},
		}
		data, err = proto.Marshal(response)
		if err == nil {
			err = conn.WriteMessage(websocket.BinaryMessage, data)
		}
		serverErr <- err
		_, _, _ = conn.ReadMessage()
	}))
	t.Cleanup(server.Close)

	ws, err := dialWS(context.Background(), websocketURL(server), "access-token", testLogger())
	if err != nil {
		t.Fatal(err)
	}
	if err := ws.SendStreaming(context.Background(), "rtmp://relay:1935/local/baby", nanitpb.Streaming_STARTED, time.Second); err != nil {
		t.Fatal(err)
	}
	if err := <-serverErr; err != nil {
		t.Fatal(err)
	}
	if err := ws.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ws.Done():
	case <-time.After(time.Second):
		t.Fatal("websocket Done channel did not close")
	}
	if err := ws.Close(); err != nil {
		t.Fatalf("second close returned a different error: %v", err)
	}
}

func TestDialWSClassifiesUnauthorizedHandshake(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(server.Close)

	_, err := dialWS(context.Background(), websocketURL(server), "rejected", testLogger())
	if !errors.Is(err, ErrDialUnauthorized) {
		t.Fatalf("error = %v, want ErrDialUnauthorized", err)
	}
}

func TestResponseErrorClassification(t *testing.T) {
	for _, message := range []string{
		"Forbidden: Number of Mobile App connections above limit, declining connection",
		"number of mobile app CONNECTIONS ABOVE LIMIT",
	} {
		code := int32(http.StatusForbidden)
		res := &nanitpb.Response{StatusCode: &code, StatusMessage: &message}
		if err := responseError(res); !errors.Is(err, ErrConnectionLimit) {
			t.Fatalf("message %q produced %v", message, err)
		}
	}

	secretMessage := "rejected for account private@example.invalid"
	code := int32(http.StatusForbidden)
	err := responseError(&nanitpb.Response{StatusCode: &code, StatusMessage: &secretMessage})
	if err == nil || strings.Contains(err.Error(), secretMessage) {
		t.Fatalf("remote status message leaked through error: %v", err)
	}
	if err := responseError(&nanitpb.Response{}); err == nil {
		t.Fatal("expected missing-status error")
	}
}

func TestSendStreamingTimeoutRemovesPendingRequest(t *testing.T) {
	upgrader := websocket.Upgrader{}
	received := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		if _, _, err := conn.ReadMessage(); err == nil {
			close(received)
		}
		<-release
	}))
	t.Cleanup(server.Close)

	ws, err := dialWS(context.Background(), websocketURL(server), "token", testLogger())
	if err != nil {
		t.Fatal(err)
	}
	err = ws.SendStreaming(context.Background(), "rtmp://relay:1935/local/baby", nanitpb.Streaming_STARTED, 25*time.Millisecond)
	if err == nil || err.Error() != "request timeout" {
		t.Fatalf("error = %v, want request timeout", err)
	}
	select {
	case <-received:
	case <-time.After(time.Second):
		t.Fatal("server did not receive request")
	}
	ws.pendingMu.Lock()
	pending := len(ws.pending)
	ws.pendingMu.Unlock()
	if pending != 0 {
		t.Fatalf("pending requests = %d, want 0", pending)
	}
	_ = ws.Close()
	close(release)
}
