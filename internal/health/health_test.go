package health

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strconv"
	"testing"

	"github.com/derekgould/multi-dev-proxy/internal/config"
)

func TestBuildNilDefaultsToTCPPort(t *testing.T) {
	ln, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	probe := Build(nil, port, "")
	if !probe() {
		t.Error("expected probe to succeed on open port")
	}

	ln.Close()
	if probe() {
		t.Error("expected probe to fail on closed port")
	}
}

func TestBuildTCP(t *testing.T) {
	ln, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	probe := Build(&config.HealthCheck{TCP: port}, 1, "")
	if !probe() {
		t.Error("expected TCP probe to hit configured port, not defaultPort")
	}
}

func TestBuildHTTP(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		healthy bool
	}{
		{"200", http.StatusOK, true},
		{"301", http.StatusMovedPermanently, true},
		{"399", 399, true},
		{"400", http.StatusBadRequest, false},
		{"500", http.StatusInternalServerError, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
			}))
			defer srv.Close()
			probe := Build(&config.HealthCheck{HTTP: srv.URL}, 0, "")
			got := probe()
			if got != tc.healthy {
				t.Errorf("status %d: got healthy=%v, want %v", tc.status, got, tc.healthy)
			}
		})
	}
}

func TestBuildHTTPUnreachable(t *testing.T) {
	ln, _ := net.Listen("tcp", "localhost:0")
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	probe := Build(&config.HealthCheck{HTTP: "http://localhost:" + strconv.Itoa(port)}, 0, "")
	if probe() {
		t.Error("expected probe to fail when server is down")
	}
}

func TestBuildCommand(t *testing.T) {
	if _, err := exec.LookPath("true"); err != nil {
		t.Skip("'true' not available")
	}
	probe := Build(&config.HealthCheck{Command: "true"}, 0, "")
	if !probe() {
		t.Error("expected 'true' to be healthy")
	}

	probe = Build(&config.HealthCheck{Command: "false"}, 0, "")
	if probe() {
		t.Error("expected 'false' to be unhealthy")
	}

	probe = Build(&config.HealthCheck{Command: "does-not-exist-xyzzy"}, 0, "")
	if probe() {
		t.Error("expected missing binary to be unhealthy")
	}
}

func TestBuildCommandQuoted(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("'sh' not available")
	}
	probe := Build(&config.HealthCheck{Command: `sh -c "exit 0"`}, 0, "")
	if !probe() {
		t.Error("expected quoted command to parse and succeed")
	}
}

func TestBuildDockerSkipsWhenDockerMissing(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		// Without docker installed we can still assert the probe doesn't
		// panic and simply reports unhealthy.
		probe := Build(&config.HealthCheck{Docker: true}, 0, t.TempDir())
		if probe() {
			t.Error("expected docker probe to fail when docker is not installed")
		}
		return
	}
	// With docker installed, an empty temp dir has no compose project, so the
	// probe should report unhealthy (exit nonzero or empty output).
	probe := Build(&config.HealthCheck{Docker: true}, 0, t.TempDir())
	if probe() {
		t.Error("expected docker probe to fail in dir with no compose project")
	}
}

func TestTCPProbeClosedPort(t *testing.T) {
	// Grab a port, release it, probe should fail.
	ln, _ := net.Listen("tcp", "localhost:0")
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	if tcpProbe(port) {
		t.Errorf("expected probe on closed port %d to fail", port)
	}
}

func TestSplitArgsBasic(t *testing.T) {
	got, err := splitArgs(`docker compose ps -q`)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"docker", "compose", "ps", "-q"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestSplitArgsQuoted(t *testing.T) {
	got, err := splitArgs(`sh -c "echo hello world"`)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"sh", "-c", "echo hello world"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestSplitArgsUnterminated(t *testing.T) {
	if _, err := splitArgs(`foo "bar`); err == nil {
		t.Error("expected error for unterminated quote")
	}
}
