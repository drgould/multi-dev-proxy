package hookpty

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestDetectorPromptStall(t *testing.T) {
	var d detector
	t0 := time.Now()
	d.observe([]byte("installing...\nPassword: "), t0)
	if d.waiting(t0.Add(time.Second), 5*time.Second) {
		t.Fatal("waiting before stall window elapsed")
	}
	if !d.waiting(t0.Add(6*time.Second), 5*time.Second) {
		t.Fatal("not waiting after stall on unterminated prompt")
	}
}

func TestDetectorNewlineTerminatedPrompt(t *testing.T) {
	var d detector
	t0 := time.Now()
	// Prompt printed on its own line before a read — no partial remains, but
	// the last complete line ends prompt-like.
	d.observe([]byte("Enter API key:\n"), t0)
	if d.waiting(t0.Add(time.Second), 5*time.Second) {
		t.Fatal("waiting before stall window elapsed")
	}
	if !d.waiting(t0.Add(6*time.Second), 5*time.Second) {
		t.Fatal("newline-terminated prompt not flagged as waiting")
	}
	if got := string(d.pending()); got != "Enter API key:\r\n" {
		t.Fatalf("pending = %q, want prompt line replay", got)
	}
}

func TestDetectorPromptLineSpansChunks(t *testing.T) {
	var d detector
	t0 := time.Now()
	d.observe([]byte("Are you"), t0)
	d.observe([]byte(" sure?\n"), t0)
	if !d.waiting(t0.Add(6*time.Second), 5*time.Second) {
		t.Fatal("chunk-split prompt line not flagged as waiting")
	}
}

func TestDetectorNewlineTerminatedSilence(t *testing.T) {
	var d detector
	t0 := time.Now()
	d.observe([]byte("compiling step 3 of 9\n"), t0)
	if d.waiting(t0.Add(time.Minute), 5*time.Second) {
		t.Fatal("newline-terminated silence flagged as waiting")
	}
}

func TestDetectorSpinnerCarriageReturn(t *testing.T) {
	var d detector
	t0 := time.Now()
	// A stalled spinner's last write typically ends with \r (rewinding for
	// the next frame), leaving an empty partial line.
	d.observe([]byte("downloading 50%\rdownloading 51%\r"), t0)
	if d.waiting(t0.Add(time.Minute), 5*time.Second) {
		t.Fatal("spinner output flagged as waiting")
	}
}

func TestDetectorANSIOnlyPartial(t *testing.T) {
	var d detector
	t0 := time.Now()
	d.observe([]byte("done\n\x1b[2K\x1b[0m"), t0)
	if d.waiting(t0.Add(time.Minute), 5*time.Second) {
		t.Fatal("ANSI-only partial flagged as waiting")
	}
}

func TestDetectorOutputResumeClears(t *testing.T) {
	var d detector
	t0 := time.Now()
	d.observe([]byte("Continue? (y/n) "), t0)
	if !d.waiting(t0.Add(6*time.Second), 5*time.Second) {
		t.Fatal("expected waiting")
	}
	d.observe([]byte("y\nproceeding\n"), t0.Add(7*time.Second))
	if d.waiting(t0.Add(8*time.Second), 5*time.Second) {
		t.Fatal("waiting after output resumed")
	}
}

func TestDetectorNoOutputAtAll(t *testing.T) {
	var d detector
	if d.waiting(time.Now(), 5*time.Second) {
		t.Fatal("zero-output session flagged as waiting")
	}
}

func TestDetectorPartialAccumulatesAcrossChunks(t *testing.T) {
	var d detector
	t0 := time.Now()
	d.observe([]byte("Pass"), t0)
	d.observe([]byte("word: "), t0)
	if got := string(d.pending()); got != "Password: " {
		t.Fatalf("pending = %q, want %q", got, "Password: ")
	}
}

func TestDetectorPartialCap(t *testing.T) {
	var d detector
	d.observe(bytes.Repeat([]byte("x"), partialCap*3), time.Now())
	if n := len(d.pending()); n != partialCap {
		t.Fatalf("partial len = %d, want cap %d", n, partialCap)
	}
}

func TestCRToLF(t *testing.T) {
	cases := []struct{ in, want string }{
		{"a\r\nb\r\n", "a\nb\n"},
		{"spin\rspin2\rdone\n", "spin\nspin2\ndone\n"},
		{"plain\n", "plain\n"},
	}
	for _, c := range cases {
		var buf bytes.Buffer
		w := &crToLF{w: &buf}
		w.Write([]byte(c.in))
		if buf.String() != c.want {
			t.Errorf("crToLF(%q) = %q, want %q", c.in, buf.String(), c.want)
		}
	}
}

func TestCRToLFSplitAcrossWrites(t *testing.T) {
	var buf bytes.Buffer
	w := &crToLF{w: &buf}
	w.Write([]byte("a\r"))
	w.Write([]byte("\nb"))
	if got := buf.String(); got != "a\nb" {
		t.Fatalf("got %q, want %q", got, "a\nb")
	}
	if strings.Contains(buf.String(), "\r") {
		t.Fatal("carriage return leaked through")
	}
}
