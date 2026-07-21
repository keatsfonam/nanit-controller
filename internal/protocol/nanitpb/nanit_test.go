package nanitpb

import (
	"encoding/hex"
	"testing"

	"google.golang.org/protobuf/proto"
)

func TestWireCompatibility(t *testing.T) {
	t.Parallel()

	requestID := int32(42)
	rtmpURL := "rtmp://192.0.2.10:1935/local/baby"
	attempts := int32(1)
	statusCode := int32(200)
	statusMessage := "OK"

	tests := []struct {
		name string
		msg  *Message
		want string
	}{
		{
			name: "streaming request",
			msg: &Message{
				Type: Message_REQUEST.Enum(),
				Request: &Request{
					Id:   &requestID,
					Type: RequestType_PUT_STREAMING.Enum(),
					Streaming: &Streaming{
						Id:       StreamIdentifier_MOBILE.Enum(),
						Status:   Streaming_STARTED.Enum(),
						RtmpUrl:  &rtmpURL,
						Attempts: &attempts,
					},
				},
			},
			want: "0801122f082a10022229080210001a2172746d703a2f2f3139322e302e322e31303a313933352f6c6f63616c2f626162792001",
		},
		{
			name: "response",
			msg: &Message{
				Type: Message_RESPONSE.Enum(),
				Response: &Response{
					RequestId:     &requestID,
					RequestType:   RequestType_PUT_STREAMING.Enum(),
					StatusCode:    &statusCode,
					StatusMessage: &statusMessage,
				},
			},
			want: "08021a0b082a100218c80122024f4b",
		},
		{
			name: "keepalive",
			msg:  &Message{Type: Message_KEEPALIVE.Enum()},
			want: "0800",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := proto.Marshal(tt.msg)
			if err != nil {
				t.Fatal(err)
			}
			if gotHex := hex.EncodeToString(got); gotHex != tt.want {
				t.Fatalf("wire bytes = %s, want %s", gotHex, tt.want)
			}

			var decoded Message
			if err := proto.Unmarshal(got, &decoded); err != nil {
				t.Fatal(err)
			}
			if !proto.Equal(tt.msg, &decoded) {
				t.Fatalf("round trip mismatch: got %v, want %v", &decoded, tt.msg)
			}
		})
	}
}
