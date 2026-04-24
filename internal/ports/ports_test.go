package ports

import (
	"net"
	"strconv"
	"testing"
)

func TestParseRange(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    PortRange
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid range",
			input:   "10000-60000",
			want:    PortRange{Start: 10000, End: 60000},
			wantErr: false,
		},
		{
			name:    "valid range with spaces",
			input:   " 10000 - 60000 ",
			want:    PortRange{Start: 10000, End: 60000},
			wantErr: false,
		},
		{
			name:    "reversed range",
			input:   "60000-10000",
			wantErr: true,
			errMsg:  "start must be less than end",
		},
		{
			name:    "equal start and end",
			input:   "5000-5000",
			wantErr: true,
			errMsg:  "start must be less than end",
		},
		{
			name:    "non-numeric start",
			input:   "abc-60000",
			wantErr: true,
			errMsg:  "start is not a number",
		},
		{
			name:    "non-numeric end",
			input:   "10000-xyz",
			wantErr: true,
			errMsg:  "end is not a number",
		},
		{
			name:    "missing dash",
			input:   "10000 60000",
			wantErr: true,
			errMsg:  "expected format start-end",
		},
		{
			name:    "too small range",
			input:   "5000-5005",
			wantErr: true,
			errMsg:  "range must span at least 10 ports",
		},
		{
			name:    "start below 1024",
			input:   "100-200",
			wantErr: true,
			errMsg:  "ports must be in range 1024-65535",
		},
		{
			name:    "end above 65535",
			input:   "60000-70000",
			wantErr: true,
			errMsg:  "ports must be in range 1024-65535",
		},
		{
			name:    "minimum valid range",
			input:   "1024-1033",
			want:    PortRange{Start: 1024, End: 1033},
			wantErr: false,
		},
		{
			name:    "maximum valid range",
			input:   "1024-65535",
			want:    PortRange{Start: 1024, End: 65535},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseRange(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseRange(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if tt.wantErr && tt.errMsg != "" {
				if err == nil || !contains(err.Error(), tt.errMsg) {
					t.Errorf("ParseRange(%q) error = %v, want error containing %q", tt.input, err, tt.errMsg)
				}
				return
			}
			if got != tt.want {
				t.Errorf("ParseRange(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestIsPortFree(t *testing.T) {
	tests := []struct {
		name string
		test func(t *testing.T)
	}{
		{
			name: "port is free when nothing is listening",
			test: func(t *testing.T) {
				// Find a free port first
				ln, err := net.Listen("tcp", ":0")
				if err != nil {
					t.Fatalf("failed to find free port: %v", err)
				}
				addr := ln.Addr().(*net.TCPAddr)
				port := addr.Port
				ln.Close()

				// After closing, port should be free
				if !IsPortFree(port) {
					t.Errorf("IsPortFree(%d) = false, want true", port)
				}
			},
		},
		{
			name: "port is not free when something is listening",
			test: func(t *testing.T) {
				// Bind to a port
				ln, err := net.Listen("tcp", ":0")
				if err != nil {
					t.Fatalf("failed to bind: %v", err)
				}
				defer ln.Close()

				addr := ln.Addr().(*net.TCPAddr)
				port := addr.Port

				// Port should not be free while listening
				if IsPortFree(port) {
					t.Errorf("IsPortFree(%d) = true, want false (port is in use)", port)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, tt.test)
	}
}

func TestFindFreePort(t *testing.T) {
	tests := []struct {
		name    string
		r       PortRange
		exclude []int
		check   func(t *testing.T, port int, err error)
	}{
		{
			name:    "finds free port in range",
			r:       PortRange{Start: 20000, End: 30000},
			exclude: []int{},
			check: func(t *testing.T, port int, err error) {
				if err != nil {
					t.Errorf("FindFreePort() error = %v, want nil", err)
					return
				}
				if port < 20000 || port > 30000 {
					t.Errorf("FindFreePort() = %d, want port in range [20000, 30000]", port)
				}
				if !IsPortFree(port) {
					t.Errorf("FindFreePort() returned port %d that is not actually free", port)
				}
			},
		},
		{
			name:    "excludes specified ports",
			r:       PortRange{Start: 20000, End: 30000},
			exclude: []int{20000, 20001, 20002},
			check: func(t *testing.T, port int, err error) {
				if err != nil {
					t.Errorf("FindFreePort() error = %v, want nil", err)
					return
				}
				for _, excluded := range []int{20000, 20001, 20002} {
					if port == excluded {
						t.Errorf("FindFreePort() returned excluded port %d", port)
					}
				}
			},
		},
		{
			name:    "returns error when all ports exhausted",
			r:       PortRange{Start: 20000, End: 20002},
			exclude: []int{20000, 20001, 20002},
			check: func(t *testing.T, port int, err error) {
				if err == nil {
					t.Errorf("FindFreePort() error = nil, want error (all ports excluded)")
					return
				}
				if port != 0 {
					t.Errorf("FindFreePort() = %d, want 0 on error", port)
				}
				if !contains(err.Error(), "no free port found") {
					t.Errorf("FindFreePort() error = %v, want error containing 'no free port found'", err)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			port, err := FindFreePort(tt.r, tt.exclude)
			tt.check(t, port, err)
		})
	}
}

func TestFindFreePortExhausted(t *testing.T) {
	// Create a tiny range with all ports excluded
	r := PortRange{Start: 20000, End: 20002}
	exclude := []int{20000, 20001, 20002}

	port, err := FindFreePort(r, exclude)

	if err == nil {
		t.Errorf("FindFreePort() with all ports excluded: error = nil, want error")
	}
	if port != 0 {
		t.Errorf("FindFreePort() with all ports excluded: port = %d, want 0", port)
	}
	if err != nil && !contains(err.Error(), "no free port found") {
		t.Errorf("FindFreePort() error = %v, want error containing 'no free port found'", err)
	}
}

func TestIsUDPPortFree(t *testing.T) {
	t.Run("port is free when nothing is listening", func(t *testing.T) {
		pc, err := net.ListenPacket("udp", ":0")
		if err != nil {
			t.Fatalf("failed to find free udp port: %v", err)
		}
		port := pc.LocalAddr().(*net.UDPAddr).Port
		pc.Close()
		if !IsUDPPortFree(port) {
			t.Errorf("IsUDPPortFree(%d) = false, want true", port)
		}
	})

	t.Run("port is not free when something is listening", func(t *testing.T) {
		pc, err := net.ListenPacket("udp", ":0")
		if err != nil {
			t.Fatalf("failed to bind udp: %v", err)
		}
		defer pc.Close()
		port := pc.LocalAddr().(*net.UDPAddr).Port
		if IsUDPPortFree(port) {
			t.Errorf("IsUDPPortFree(%d) = true, want false (port is in use)", port)
		}
	})
}

func TestFindFreeUDPPort(t *testing.T) {
	r := PortRange{Start: 20000, End: 30000}
	port, err := FindFreeUDPPort(r, nil)
	if err != nil {
		t.Fatalf("FindFreeUDPPort() error = %v", err)
	}
	if port < r.Start || port > r.End {
		t.Errorf("FindFreeUDPPort() = %d, want port in range [%d, %d]", port, r.Start, r.End)
	}
	// The returned port should actually be UDP-bindable.
	pc, err := net.ListenPacket("udp", ":"+strconv.Itoa(port))
	if err != nil {
		t.Errorf("FindFreeUDPPort() returned port %d that is not UDP-bindable: %v", port, err)
	} else {
		pc.Close()
	}
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
