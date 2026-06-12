package hookpty

import (
	"bytes"
	"io"
	"os"
	"sync"
	"testing"
	"time"
)

// fakeTerm records raw-mode transitions without touching a real terminal.
type fakeTerm struct {
	mu       sync.Mutex
	isTTY    bool
	raws     int
	restores int
}

func (f *fakeTerm) IsTerminal(fd int) bool { return f.isTTY }

func (f *fakeTerm) MakeRaw(fd int) (func() error, error) {
	f.mu.Lock()
	f.raws++
	f.mu.Unlock()
	return func() error {
		f.mu.Lock()
		f.restores++
		f.mu.Unlock()
		return nil
	}, nil
}

func (f *fakeTerm) counts() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.raws, f.restores
}

// syncBuffer is a goroutine-safe bytes.Buffer for capturing notices.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// testManager builds a Manager over OS pipes (stdin writable by the test,
// stdout readable by the test) plus a fake terminal controller.
func testManager(t *testing.T) (m *Manager, stdinW *os.File, stdoutR *os.File, notify *syncBuffer, tc *fakeTerm) {
	t.Helper()
	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stdinR.Close()
		stdinW.Close()
		stdoutR.Close()
		stdoutW.Close()
	})
	notify = &syncBuffer{}
	tc = &fakeTerm{isTTY: true}
	m = &Manager{
		stdin:      stdinR,
		stdout:     stdoutW,
		notify:     notify,
		tc:         tc,
		stallAfter: 50 * time.Millisecond,
		pollEvery:  10 * time.Millisecond,
		done:       make(chan struct{}),
	}
	t.Cleanup(m.Close)
	return m, stdinW, stdoutR, notify, tc
}

// testSession builds a registered session whose master is one end of an OS
// pipe; the test reads forwarded input from the other end.
func testSession(t *testing.T, m *Manager, label string) (*Session, *os.File, *bytes.Buffer) {
	t.Helper()
	masterR, masterW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		masterR.Close()
		masterW.Close()
	})
	var logBuf bytes.Buffer
	s := &Session{label: label, master: masterW, prefixed: &crToLF{w: &logBuf}}
	s.sink = s.prefixed
	m.register(s)
	return s, masterR, &logBuf
}

func TestNewManagerNonTTY(t *testing.T) {
	if m := NewManager(os.Stdin, os.Stdout, io.Discard, &fakeTerm{isTTY: false}); m != nil {
		t.Fatal("expected nil manager when stdin/stdout is not a TTY")
	}
}

func TestNewManagerEnvDisable(t *testing.T) {
	t.Setenv("MDP_NO_HOOK_PTY", "1")
	if m := NewManager(os.Stdin, os.Stdout, io.Discard, &fakeTerm{isTTY: true}); m != nil {
		t.Fatal("expected nil manager when MDP_NO_HOOK_PTY is set")
	}
}

func TestWaitingNoticePrinted(t *testing.T) {
	m, _, _, notify, _ := testManager(t)
	s, _, _ := testSession(t, m, "api setup")
	m.setWaiting(s, true)
	if got := notify.String(); !bytes.Contains([]byte(got), []byte("[api setup] looks like it is waiting for input")) {
		t.Fatalf("notice not printed, got %q", got)
	}
	// Re-flagging is a no-op.
	m.setWaiting(s, true)
	if n := bytes.Count([]byte(notify.String()), []byte("waiting for input")); n != 1 {
		t.Fatalf("notice printed %d times, want 1", n)
	}
}

func TestAttachForwardDetach(t *testing.T) {
	m, stdinW, stdoutR, notify, tc := testManager(t)
	s, masterR, _ := testSession(t, m, "api setup")
	m.setWaiting(s, true)

	// Bare Enter attaches to the (only) waiting session.
	stdinW.Write([]byte("\n"))
	waitFor(t, "raw mode", func() bool { raws, _ := tc.counts(); return raws == 1 })

	// Separator + replayed prompt appear on the raw terminal.
	sepBuf := make([]byte, 256)
	n, _ := stdoutR.Read(sepBuf)
	if !bytes.Contains(sepBuf[:n], []byte("attached to [api setup]")) {
		t.Fatalf("separator missing, got %q", sepBuf[:n])
	}

	// Typed bytes are forwarded to the hook's PTY master.
	stdinW.Write([]byte("yes\n"))
	fwd := make([]byte, 16)
	n, _ = masterR.Read(fwd)
	if string(fwd[:n]) != "yes\n" {
		t.Fatalf("forwarded %q, want %q", fwd[:n], "yes\n")
	}

	// Ctrl-] detaches and restores the terminal.
	stdinW.Write([]byte{detachByte})
	waitFor(t, "restore", func() bool { _, r := tc.counts(); return r == 1 })
	waitFor(t, "detach notice", func() bool {
		return bytes.Contains([]byte(notify.String()), []byte("detached from [api setup]"))
	})
}

func TestPickerSelectsNumberedSession(t *testing.T) {
	m, stdinW, stdoutR, notify, tc := testManager(t)
	s1, _, _ := testSession(t, m, "api setup")
	s2, master2R, _ := testSession(t, m, "web setup")
	m.setWaiting(s1, true)
	m.setWaiting(s2, true)

	if got := notify.String(); !bytes.Contains([]byte(got), []byte("[1] api setup")) || !bytes.Contains([]byte(got), []byte("[2] web setup")) {
		t.Fatalf("numbered picker notice missing, got %q", got)
	}

	stdinW.Write([]byte("2\n"))
	waitFor(t, "raw mode", func() bool { raws, _ := tc.counts(); return raws == 1 })
	m.mu.Lock()
	attached := m.attached
	m.mu.Unlock()
	if attached != s2 {
		t.Fatalf("attached to %v, want web setup", attached)
	}

	// Drain stdout so the writer side never blocks, then verify forwarding
	// reaches the picked session's master.
	go io.Copy(io.Discard, stdoutR)
	stdinW.Write([]byte("ok"))
	fwd := make([]byte, 8)
	n, _ := master2R.Read(fwd)
	if string(fwd[:n]) != "ok" {
		t.Fatalf("forwarded %q to picked session, want %q", fwd[:n], "ok")
	}
}

func TestWaitingListShrinkInvalidatesTypedPick(t *testing.T) {
	m, stdinW, stdoutR, notify, tc := testManager(t)
	s1, _, _ := testSession(t, m, "api setup")
	s2, _, _ := testSession(t, m, "web setup")
	m.setWaiting(s1, true)
	m.setWaiting(s2, true)
	go io.Copy(io.Discard, stdoutR)

	// User types "2" (meaning web setup) but doesn't press Enter yet.
	stdinW.Write([]byte("2"))
	waitFor(t, "pick buffered", func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		return len(m.lineBuf) > 0
	})

	// api setup resumes output and leaves the list — the old numbering is
	// stale, so the half-typed pick must be discarded and the notice reprinted.
	m.setWaiting(s1, false)
	if got := notify.String(); !bytes.Contains([]byte(got), []byte("[web setup] looks like it is waiting")) {
		t.Fatalf("notice not reprinted after list shrink, got %q", got)
	}

	// Enter now attaches to the only waiting session instead of being
	// swallowed by an out-of-range "2".
	stdinW.Write([]byte("\n"))
	waitFor(t, "raw mode", func() bool { raws, _ := tc.counts(); return raws == 1 })
	m.mu.Lock()
	attached := m.attached
	m.mu.Unlock()
	if attached != s2 {
		t.Fatalf("attached to %v, want web setup", attached)
	}
}

func TestSessionExitWhileAttachedDetaches(t *testing.T) {
	m, stdinW, stdoutR, _, tc := testManager(t)
	s, _, _ := testSession(t, m, "api setup")
	m.setWaiting(s, true)
	go io.Copy(io.Discard, stdoutR)

	stdinW.Write([]byte("\n"))
	waitFor(t, "raw mode", func() bool { raws, _ := tc.counts(); return raws == 1 })

	m.unregister(s) // RunHook cleanup path when the hook exits mid-attach
	if _, restores := tc.counts(); restores != 1 {
		t.Fatalf("restores = %d, want 1", restores)
	}
}

func TestCloseRestoresWhileAttached(t *testing.T) {
	m, stdinW, stdoutR, _, tc := testManager(t)
	s, _, _ := testSession(t, m, "api setup")
	m.setWaiting(s, true)
	go io.Copy(io.Discard, stdoutR)

	stdinW.Write([]byte("\n"))
	waitFor(t, "raw mode", func() bool { raws, _ := tc.counts(); return raws == 1 })

	m.Close()
	if _, restores := tc.counts(); restores != 1 {
		t.Fatalf("restores = %d, want 1", restores)
	}
	m.Close() // idempotent
}

func TestStrayInputBeforeNoticeDoesNotBlockAttach(t *testing.T) {
	m, stdinW, stdoutR, _, tc := testManager(t)
	s, _, _ := testSession(t, m, "api setup")
	go io.Copy(io.Discard, stdoutR)

	// Stray keystrokes (no newline) typed while no hook is waiting.
	stdinW.Write([]byte("ab\x1b[A"))
	waitFor(t, "stray bytes buffered", func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		return len(m.lineBuf) > 0
	})

	// Flagging a session discards the stale buffer, so bare Enter attaches.
	m.setWaiting(s, true)
	stdinW.Write([]byte("\n"))
	waitFor(t, "raw mode", func() bool { raws, _ := tc.counts(); return raws == 1 })
}

func TestEnterWithNothingWaitingIsIgnored(t *testing.T) {
	m, stdinW, _, _, tc := testManager(t)
	testSession(t, m, "api setup") // registered but not waiting
	stdinW.Write([]byte("\n\n3\n"))
	time.Sleep(50 * time.Millisecond)
	if raws, _ := tc.counts(); raws != 0 {
		t.Fatalf("attached with nothing waiting (raws=%d)", raws)
	}
}
