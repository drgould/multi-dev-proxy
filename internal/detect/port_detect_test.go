package detect

import (
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func TestDetectPort(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantPort   int
		wantScheme string
		wantErr    bool
	}{
		{"vite output", "  Local: http://localhost:3000/\n", 3000, "http", false},
		{"nextjs output", "- Local: http://localhost:3000\n", 3000, "http", false},
		{"rails output", "Listening on http://127.0.0.1:3000\n", 3000, "http", false},
		{"generic http", "http://0.0.0.0:4001\n", 4001, "http", false},
		{"bare port number only", "Listening on port 3000\n", 0, "", true},
		{"no port in output", "Starting server...\n", 0, "", true},
		{"empty input", "", 0, "", true},
		{"https URL", "https://localhost:8443/\n", 8443, "https", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := strings.NewReader(tt.input)
			result, err := DetectPort(r, 500*time.Millisecond)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got port %d", result.Port)
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if result.Port != tt.wantPort {
					t.Errorf("got port %d, want %d", result.Port, tt.wantPort)
				}
				if result.Scheme != tt.wantScheme {
					t.Errorf("got scheme %q, want %q", result.Scheme, tt.wantScheme)
				}
			}
		})
	}
}

func TestDetectPortTimeout(t *testing.T) {
	pr, _ := io.Pipe()
	defer pr.Close()
	_, err := DetectPort(pr, 100*time.Millisecond)
	if !errors.Is(err, ErrPortNotDetected) {
		t.Errorf("expected ErrPortNotDetected, got %v", err)
	}
}
