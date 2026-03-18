package proxy

import (
	"net/http"
	"testing"
)

func TestFixWebSocketHeaders(t *testing.T) {
	tests := []struct {
		name   string
		input  http.Header
		want   http.Header
	}{
		{
			name: "fixes Sec-Websocket-Key",
			input: http.Header{"Sec-Websocket-Key": []string{"dGhlIHNhbXBsZSBub25jZQ=="}},
			want:  http.Header{"Sec-WebSocket-Key": []string{"dGhlIHNhbXBsZSBub25jZQ=="}},
		},
		{
			name: "fixes multiple headers",
			input: http.Header{
				"Sec-Websocket-Key":     []string{"key"},
				"Sec-Websocket-Version": []string{"13"},
			},
			want: http.Header{
				"Sec-WebSocket-Key":     []string{"key"},
				"Sec-WebSocket-Version": []string{"13"},
			},
		},
		{
			name:  "non-websocket headers untouched",
			input: http.Header{"Content-Type": []string{"application/json"}},
			want:  http.Header{"Content-Type": []string{"application/json"}},
		},
		{
			name:  "empty header map",
			input: http.Header{},
			want:  http.Header{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			FixWebSocketHeaders(tt.input)
			for k, v := range tt.want {
				got := tt.input[k]
				if len(got) != len(v) || (len(v) > 0 && got[0] != v[0]) {
					t.Errorf("header %q: got %v, want %v", k, got, v)
				}
			}
			// Verify wrong-cased headers are gone
			for wrong := range wsHeaders {
				if tt.input[wrong] != nil {
					t.Errorf("wrong-cased header %q still present", wrong)
				}
			}
		})
	}
}

func TestIsWebSocketUpgrade(t *testing.T) {
	tests := []struct {
		name string
		req  *http.Request
		want bool
	}{
		{
			name: "websocket upgrade",
			req: &http.Request{Header: http.Header{
				"Upgrade":    []string{"websocket"},
				"Connection": []string{"Upgrade"},
			}},
			want: true,
		},
		{
			name: "plain HTTP",
			req:  &http.Request{Header: http.Header{"Content-Type": []string{"text/html"}}},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsWebSocketUpgrade(tt.req); got != tt.want {
				t.Errorf("IsWebSocketUpgrade() = %v, want %v", got, tt.want)
			}
		})
	}
}
