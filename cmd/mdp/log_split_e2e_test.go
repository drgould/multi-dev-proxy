package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestLogSplitRealDockerCompose is a real end-to-end test: it invokes the mdp
// binary against an actual `docker compose up` on a tiny two-service compose
// file and verifies each container's output lands in its own lane.
//
// Skipped when `-short` is passed or when `docker compose` / a docker daemon
// isn't available, so CI machines without docker still get a clean run.
func TestLogSplitRealDockerCompose(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real docker-compose e2e in -short mode")
	}
	if err := exec.Command("docker", "compose", "version").Run(); err != nil {
		t.Skipf("docker compose not available: %v", err)
	}
	// `info` probes the daemon; compose commands hang without it.
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skipf("docker daemon not reachable: %v", err)
	}

	// Build the mdp binary into a tempdir so the test runs against the
	// current code without relying on a globally installed binary.
	dir := t.TempDir()
	bin := filepath.Join(dir, "mdp")
	build := exec.Command("go", "build", "-o", bin, "./cmd/mdp")
	build.Dir = mustModuleRoot(t)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}

	composeYAML := `services:
  apisvc:
    image: alpine:3
    command: ["sh", "-c", "echo listening on port 8080; echo GET /health 200"]
  authsvc:
    image: alpine:3
    command: ["sh", "-c", "echo auth ready on :8081"]
`
	if err := os.WriteFile(filepath.Join(dir, "docker-compose.yml"), []byte(composeYAML), 0644); err != nil {
		t.Fatalf("write compose file: %v", err)
	}

	for _, tc := range []struct {
		name string
		env  []string
	}{
		{name: "ansi_never", env: []string{"NO_COLOR=1"}},
		{name: "ansi_auto", env: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// --abort-on-container-exit so compose returns promptly once both
			// short-lived commands finish. --pull missing avoids unnecessary
			// network traffic when the image is already cached.
			cmd := exec.Command(bin,
				"run", "--log-split=compose", "--",
				"docker", "compose", "up",
				"--abort-on-container-exit",
				"--pull", "missing",
			)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), tc.env...)
			var out, stderr bytes.Buffer
			cmd.Stdout = &out
			cmd.Stderr = &stderr

			timer := time.AfterFunc(90*time.Second, func() {
				if cmd.Process != nil {
					cmd.Process.Kill()
				}
			})
			defer timer.Stop()

			_ = cmd.Run() // non-zero is expected from abort-on-container-exit
			timer.Stop()

			defer func() {
				down := exec.Command("docker", "compose", "down", "--remove-orphans", "-v")
				down.Dir = dir
				_ = down.Run()
			}()

			combined := stripANSI(out.String() + stderr.String())

			// Each container's message must appear on its own lane, i.e. with
			// the container's short name as the line prefix (after our
			// splitWriter prepends the label).
			for _, want := range []string{
				"listening on port 8080",
				"GET /health 200",
				"auth ready on :8081",
			} {
				if !strings.Contains(combined, want) {
					t.Errorf("missing message %q in output:\n%s", want, combined)
				}
			}

			// Both container lanes should have been created. Compose's default
			// naming is <dir>-<service>-<replica>, so just check for the
			// service names appearing as line prefixes in our split output.
			for _, want := range []string{"apisvc", "authsvc"} {
				if !strings.Contains(combined, want) {
					t.Errorf("missing container lane %q in output:\n%s", want, combined)
				}
			}
		})
	}
}

// TestLogSplitRegexE2E exercises the full `--log-split=regex:...` path end to
// end: it builds the mdp binary, runs it against a shell subprocess that
// emits bracket-prefixed output (think honcho/foreman/kubectl-style), and
// verifies the output lands in per-lane colored prefixes while unrecognized
// lines fall through.
//
// Doesn't need docker or any daemon, so it runs unconditionally under
// `go test` (only skipped with `-short`).
func TestLogSplitRegexE2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping regex e2e in -short mode")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "mdp")
	build := exec.Command("go", "build", "-o", bin, "./cmd/mdp")
	build.Dir = mustModuleRoot(t)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}

	script := `printf '[api] listening on 8080\n[auth] ready\norphan line\n[api] GET /health 200\n'`
	cmd := exec.Command(bin,
		"run",
		`--log-split=regex:^\[(?P<name>[^\]]+)\]\s*(?P<msg>.*)$`,
		"--",
		"bash", "-c", script,
	)
	cmd.Dir = dir
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr

	timer := time.AfterFunc(20*time.Second, func() {
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
	})
	defer timer.Stop()
	_ = cmd.Run()
	timer.Stop()

	combined := stripANSI(out.String() + stderr.String())

	// Messages land in the split output.
	for _, want := range []string{
		"listening on 8080",
		"ready",
		"GET /health 200",
	} {
		if !strings.Contains(combined, want) {
			t.Errorf("missing message %q in combined output:\n%s", want, combined)
		}
	}
	// Lane labels appear as prefixes.
	for _, want := range []string{"api", "auth"} {
		if !strings.Contains(combined, want) {
			t.Errorf("missing lane label %q in combined output:\n%s", want, combined)
		}
	}
	// Orphan line (no bracket prefix) falls through unchanged.
	if !strings.Contains(combined, "orphan line") {
		t.Errorf("orphan fallback line missing from output:\n%s", combined)
	}
}

// TestLogSplitRegexInvalidFlagE2E verifies that a malformed --log-split
// value is rejected by the binary before the child command is started.
func TestLogSplitRegexInvalidFlagE2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping regex e2e in -short mode")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "mdp")
	build := exec.Command("go", "build", "-o", bin, "./cmd/mdp")
	build.Dir = mustModuleRoot(t)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}

	// Pattern is missing the required named captures — ParseLogSplitFlag
	// should reject it and the child command must not run.
	cmd := exec.Command(bin, "run", "--log-split=regex:^(.+)$", "--", "bash", "-c", "echo should-not-run")
	cmd.Dir = dir
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		t.Fatalf("expected non-zero exit for malformed --log-split; stdout:\n%s\nstderr:\n%s", out.String(), stderr.String())
	}
	combined := out.String() + stderr.String()
	if !strings.Contains(combined, "name") || !strings.Contains(combined, "msg") {
		t.Errorf("error should mention required `name`/`msg` captures; got:\n%s", combined)
	}
	if strings.Contains(combined, "should-not-run") {
		t.Errorf("child command ran despite invalid --log-split value:\n%s", combined)
	}
}

// mustModuleRoot walks up from the test file's package until it finds go.mod,
// returning the absolute module root. Used so `go build ./` runs against the
// repo root even though the test binary's cwd is arbitrary.
func mustModuleRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	cur := wd
	for {
		if _, err := os.Stat(filepath.Join(cur, "go.mod")); err == nil {
			return cur
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			t.Fatalf("go.mod not found walking up from %s", wd)
		}
		cur = parent
	}
}
