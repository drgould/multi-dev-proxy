package main

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/derekgould/multi-dev-proxy/internal/config"
)

// stripANSI removes ANSI color escape sequences so assertions match on the
// text content alone.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && s[j] != 'm' {
				j++
			}
			if j < len(s) {
				i = j
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func TestSplitWriterRoutesComposeLines(t *testing.T) {
	var out, fallback bytes.Buffer
	splitter := newLogSplitter(parseComposePrefix, "")
	sw := newSplitWriter(&fallback, &out, splitter)

	sw.Write([]byte("api-1   | hello\nauth-1  | world\nnot a compose line\n"))
	sw.Flush()

	outStr := stripANSI(out.String())
	fbStr := stripANSI(fallback.String())

	for _, want := range []string{"api-1", "hello", "auth-1", "world"} {
		if !strings.Contains(outStr, want) {
			t.Errorf("compose-routed output missing %q: %q", want, outStr)
		}
	}
	if !strings.Contains(fbStr, "not a compose line") {
		t.Errorf("fallback missing non-matching line: %q", fbStr)
	}
	if strings.Contains(outStr, "not a compose line") {
		t.Errorf("non-matching line should not appear in compose output: %q", outStr)
	}
	if strings.Contains(fbStr, "hello") || strings.Contains(fbStr, "world") {
		t.Errorf("matching lines should not appear in fallback: %q", fbStr)
	}
}

func TestSplitWriterBuffersPartialLines(t *testing.T) {
	var out, fallback bytes.Buffer
	splitter := newLogSplitter(parseComposePrefix, "")
	sw := newSplitWriter(&fallback, &out, splitter)

	sw.Write([]byte("api-1   | hel"))
	if stripANSI(out.String()) != "" || fallback.String() != "" {
		t.Fatalf("partial line should not flush yet, got out=%q fb=%q", out.String(), fallback.String())
	}
	sw.Write([]byte("lo\n"))

	outStr := stripANSI(out.String())
	if !strings.Contains(outStr, "hello") {
		t.Errorf("expected reassembled 'hello', got %q", outStr)
	}
}

func TestSplitWriterFlushEmitsTrailingBuffer(t *testing.T) {
	var out, fallback bytes.Buffer
	splitter := newLogSplitter(parseComposePrefix, "")
	sw := newSplitWriter(&fallback, &out, splitter)

	// No trailing newline: should only surface on Flush.
	sw.Write([]byte("api-1   | partial"))
	if stripANSI(out.String()) != "" {
		t.Fatalf("pre-flush output should be empty, got %q", out.String())
	}
	sw.Flush()
	outStr := stripANSI(out.String())
	if !strings.Contains(outStr, "partial") {
		t.Errorf("Flush should emit trailing buffer, got %q", outStr)
	}
}

func TestSplitWriterSharedColorsAcrossStreams(t *testing.T) {
	splitter := newLogSplitter(parseComposePrefix, "")
	c1 := splitter.colorFor("api-1")
	c2 := splitter.colorFor("api-1")
	c3 := splitter.colorFor("auth-1")
	if c1 != c2 {
		t.Errorf("same name should yield same color: %q vs %q", c1, c2)
	}
	if c1 == c3 {
		t.Errorf("different names should yield different colors")
	}
}

// TestRunSoloFlushesOnNonZeroExit exercises the os.Exit-bypasses-defer path
// by re-invoking the test binary as a subprocess that calls runSolo with a
// command which emits a trailing compose-style line without a newline and
// exits non-zero. The parent verifies the subprocess stdout still contains
// that line (proving Flush ran before os.Exit).
func TestRunSoloFlushesOnNonZeroExit(t *testing.T) {
	if os.Getenv("MDP_TEST_RUNSOLO_NONZERO") == "1" {
		_ = runSolo(
			[]string{"sh", "-c", `printf 'api-1   | trailing, no newline'; exit 3`},
			config.LogSplitConfig{Mode: "compose"},
		)
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestRunSoloFlushesOnNonZeroExit$")
	cmd.Env = append(os.Environ(), "MDP_TEST_RUNSOLO_NONZERO=1")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	ee, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected ExitError from subprocess, got %v", err)
	}
	if ee.ExitCode() != 3 {
		t.Errorf("subprocess exit code: got %d, want 3", ee.ExitCode())
	}
	if !strings.Contains(stripANSI(out.String()), "trailing, no newline") {
		t.Errorf("trailing unterminated line was dropped; subprocess stdout:\n%s", out.String())
	}
}

// TestSplitWriterComposeFixtures feeds the split writer realistic docker-compose
// output samples (plain, name-wrapped ANSI, name-and-pipe-wrapped ANSI) and
// asserts each sub-service's messages land under its own lane while compose's
// own status lines fall through to the outer prefix.
func TestSplitWriterComposeFixtures(t *testing.T) {
	// Escapes kept as raw \x1b bytes so these stay faithful to real compose output.
	const cyan = "\x1b[36m"
	const yellow = "\x1b[33m"
	const reset = "\x1b[0m"

	cases := []struct {
		name  string
		input string
	}{
		{
			name: "ansi_never",
			input: "" +
				" Network app_default  Creating\n" +
				" Container app-api-1  Started\n" +
				"Attaching to api-1, auth-1\n" +
				"api-1   | listening on port 8080\n" +
				"auth-1  | auth ready on :8081\n" +
				"api-1   | GET /health 200\n",
		},
		{
			name: "ansi_colored_name_only",
			input: "" +
				"Attaching to api-1, auth-1\n" +
				cyan + "api-1   " + reset + " | listening on port 8080\n" +
				yellow + "auth-1  " + reset + " | auth ready on :8081\n" +
				cyan + "api-1   " + reset + " | GET /health 200\n",
		},
		{
			name: "ansi_colored_name_and_pipe",
			input: "" +
				"Attaching to api-1, auth-1\n" +
				cyan + "api-1   |" + reset + " listening on port 8080\n" +
				yellow + "auth-1  |" + reset + " auth ready on :8081\n" +
				cyan + "api-1   |" + reset + " GET /health 200\n",
		},
		{
			name: "crlf_line_endings",
			input: "" +
				"api-1   | listening on port 8080\r\n" +
				"auth-1  | auth ready on :8081\r\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var outBuf, fallbackBuf bytes.Buffer
			splitter := newLogSplitter(parseComposePrefix, "")
			sw := newSplitWriter(&fallbackBuf, &outBuf, splitter)
			sw.Write([]byte(tc.input))
			sw.Flush()

			out := stripANSI(outBuf.String())
			fb := stripANSI(fallbackBuf.String())

			// api-1 and auth-1 messages must land in the split output, not fallback.
			for _, want := range []string{
				"listening on port 8080",
				"auth ready on :8081",
				"GET /health 200",
			} {
				if strings.Contains(tc.input, want) && !strings.Contains(out, want) {
					t.Errorf("split output missing %q\n--- out ---\n%s\n--- fallback ---\n%s",
						want, out, fb)
				}
				if strings.Contains(fb, want) {
					t.Errorf("message %q leaked to fallback\n--- fallback ---\n%s", want, fb)
				}
			}

			// Container-name prefixes must appear in the colored output.
			if strings.Contains(tc.input, "api-1") && !strings.Contains(out, "api-1") {
				t.Errorf("split output missing api-1 lane:\n%s", out)
			}
			if strings.Contains(tc.input, "auth-1") && !strings.Contains(out, "auth-1") {
				t.Errorf("split output missing auth-1 lane:\n%s", out)
			}

			// Compose status lines (no pipe) must fall through to fallback.
			for _, want := range []string{
				"Attaching to api-1, auth-1",
				"Network app_default  Creating",
				"Container app-api-1  Started",
			} {
				if strings.Contains(tc.input, want) && !strings.Contains(fb, want) {
					t.Errorf("status line %q should fall through to fallback\n--- fallback ---\n%s",
						want, fb)
				}
			}

			// No stray CRs in output.
			if strings.Contains(out, "\r") || strings.Contains(fb, "\r") {
				t.Errorf("carriage return leaked into output/fallback")
			}
		})
	}
}

// TestSplitWriterRegexMode exercises the user-supplied-regex splitter with a
// variety of prefix shapes: bracket-style (`[name] msg`), space-separated
// (kubectl-ish `pod/name msg`), and colon-separated honcho/foreman-style.
// Non-matching lines must fall through to the outer prefix.
func TestSplitWriterRegexMode(t *testing.T) {
	cases := []struct {
		name       string
		pattern    string
		input      string
		wantSplit  []string // messages that must appear in split output
		wantLanes  []string // sub-names that must appear
		wantFallbk []string // lines that must fall through to outer prefix
	}{
		{
			name:    "bracket_style",
			pattern: `^\[(?P<name>[^\]]+)\]\s*(?P<msg>.*)$`,
			input: "" +
				"[api] listening on 8080\n" +
				"[auth] ready\n" +
				"orphan line without brackets\n" +
				"[api] GET /health\n",
			wantSplit:  []string{"listening on 8080", "ready", "GET /health"},
			wantLanes:  []string{"api", "auth"},
			wantFallbk: []string{"orphan line without brackets"},
		},
		{
			name: "kubectl_all_containers",
			// kubectl logs --all-containers --prefix emits: `[pod/<name>/<container>] msg`.
			// Capture only the pod name so the lane label stays under
			// prefixWriter's 12-char display cap.
			pattern: `^\[pod/(?P<name>[^/]+)/[^\]]+\]\s*(?P<msg>.*)$`,
			input: "" +
				"[pod/api-abc/api] starting\n" +
				"[pod/auth-xyz/auth] ready\n" +
				"informational: cluster message\n",
			wantSplit:  []string{"starting", "ready"},
			wantLanes:  []string{"api-abc", "auth-xyz"},
			wantFallbk: []string{"informational: cluster message"},
		},
		{
			name:    "honcho_foreman_colon",
			pattern: `^(?P<name>[a-z]+)\s*\|\s(?P<msg>.*)$`,
			input: "" +
				"web      | starting server\n" +
				"worker   | processing job\n" +
				"(not a process line)\n",
			wantSplit:  []string{"starting server", "processing job"},
			wantLanes:  []string{"web", "worker"},
			wantFallbk: []string{"(not a process line)"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			splitter, err := newLogSplitterFromConfig(config.LogSplitConfig{
				Mode:  "regex",
				Regex: tc.pattern,
			}, "")
			if err != nil {
				t.Fatalf("build splitter: %v", err)
			}
			var outBuf, fbBuf bytes.Buffer
			sw := newSplitWriter(&fbBuf, &outBuf, splitter)
			sw.Write([]byte(tc.input))
			sw.Flush()

			out := stripANSI(outBuf.String())
			fb := stripANSI(fbBuf.String())

			for _, want := range tc.wantSplit {
				if !strings.Contains(out, want) {
					t.Errorf("split output missing %q\n--- out ---\n%s\n--- fb ---\n%s", want, out, fb)
				}
				if strings.Contains(fb, want) {
					t.Errorf("message %q leaked to fallback", want)
				}
			}
			for _, want := range tc.wantLanes {
				if !strings.Contains(out, want) {
					t.Errorf("missing lane %q in split output:\n%s", want, out)
				}
			}
			for _, want := range tc.wantFallbk {
				if !strings.Contains(fb, want) {
					t.Errorf("fallback missing non-matching line %q:\n%s", want, fb)
				}
				if strings.Contains(out, want) {
					t.Errorf("non-matching line %q leaked into split output:\n%s", want, out)
				}
			}
		})
	}
}

// TestSplitWriterRegexDisabled ensures a zero-value LogSplitConfig produces
// no splitter (the caller just uses the outer prefix writer directly).
func TestSplitWriterRegexDisabled(t *testing.T) {
	splitter, err := newLogSplitterFromConfig(config.LogSplitConfig{}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if splitter != nil {
		t.Errorf("expected nil splitter for empty config, got %+v", splitter)
	}
}

// TestSplitWriterRegexInvalid ensures the factory surfaces compile errors
// rather than silently producing a broken splitter.
func TestSplitWriterRegexInvalid(t *testing.T) {
	if _, err := newLogSplitterFromConfig(config.LogSplitConfig{
		Mode: "regex", Regex: "[unclosed",
	}, ""); err == nil {
		t.Error("expected compile error for malformed regex")
	}
	if _, err := newLogSplitterFromConfig(config.LogSplitConfig{
		Mode: "regex", Regex: "^(.+)$",
	}, ""); err == nil {
		t.Error("expected error when regex is missing `name`/`msg` captures")
	}
}

// TestSplitWriterOuterLabel verifies sub-lane prefixes include the outer
// service name as "<outer>/<sub>" when the splitter is configured with one,
// so it's obvious which service an inner lane belongs to.
func TestSplitWriterOuterLabel(t *testing.T) {
	splitter := newLogSplitter(parseComposePrefix, "api-main")
	var out, fb bytes.Buffer
	sw := newSplitWriter(&fb, &out, splitter)
	sw.Write([]byte("api-1   | hello\n"))
	sw.Flush()

	got := stripANSI(out.String())
	if !strings.Contains(got, "api-main/api-1") {
		t.Errorf("expected lane label 'api-main/api-1' in output, got:\n%s", got)
	}
}

// TestPrefixWriterNoTruncation ensures service names longer than the minimum
// pad width render in full rather than being cut off at 12 chars.
func TestPrefixWriterNoTruncation(t *testing.T) {
	var buf bytes.Buffer
	pw := newPrefixWriter("api-feature-a", "1;34", &buf)
	pw.Write([]byte("hello\n"))

	got := stripANSI(buf.String())
	if !strings.Contains(got, "api-feature-a") {
		t.Errorf("full label missing; long names must not be truncated:\n%s", got)
	}
	if strings.Contains(got, "api-feature- ") || strings.Contains(got, "api-feature-\n") {
		t.Errorf("label appears truncated:\n%s", got)
	}
}

func TestSplitWriterStripsCR(t *testing.T) {
	var out, fallback bytes.Buffer
	splitter := newLogSplitter(parseComposePrefix, "")
	sw := newSplitWriter(&fallback, &out, splitter)

	sw.Write([]byte("api-1   | hello\r\n"))
	sw.Flush()

	outStr := stripANSI(out.String())
	if strings.Contains(outStr, "\r") {
		t.Errorf("expected CR stripped, got %q", outStr)
	}
	if !strings.Contains(outStr, "hello\n") {
		t.Errorf("expected 'hello' line, got %q", outStr)
	}
}
