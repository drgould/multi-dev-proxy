package hookpty

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// detachByte is the attach-mode escape key (Ctrl-]).
const detachByte = 0x1d

// drainGrace bounds how long RunHook waits for the PTY master to reach EOF
// after the hook exits before force-closing it (a grandchild may still hold
// the slave side open).
const drainGrace = 2 * time.Second

// Manager runs hooks on PTYs, watches them for input-starved stalls, and
// lets the user attach their terminal to a waiting hook. One Manager is
// created per `mdp run` batch invocation; a nil Manager means the feature is
// off and callers should run hooks on plain pipes.
type Manager struct {
	stdin, stdout *os.File
	notify        io.Writer
	tc            termController
	gates         []*Gate // held while a session has focus; nil-safe (empty)
	stallAfter    time.Duration
	pollEvery     time.Duration
	releaseGrace  time.Duration

	mu               sync.Mutex
	waiting          []*Session // flagged input-starved, in flag order
	attached         *Session
	lastInput        time.Time // attach time, then time of last forwarded keystroke
	inputSinceAttach bool      // a keystroke was forwarded to the attached session
	strongPrompt     bool      // attach trigger was an unterminated partial line
	restoreTerm      func() error
	resizeStop       chan struct{}
	lineBuf          []byte
	listenerOn       bool
	closed           bool
	done             chan struct{}
}

// NewManager returns a Manager wired to the user's terminal, or nil when
// interactive hook forwarding is unavailable: stdin/stdout is not a TTY
// (e.g. CI — a PTY would make tools prompt with nobody able to attach), or
// MDP_NO_HOOK_PTY is set. gates are held (buffering all other terminal
// output) while a session has focus and released — flushing the buffer —
// when it detaches.
func NewManager(stdin, stdout *os.File, notify io.Writer, tc termController, gates ...*Gate) *Manager {
	if os.Getenv("MDP_NO_HOOK_PTY") != "" {
		return nil
	}
	if !tc.IsTerminal(int(stdin.Fd())) || !tc.IsTerminal(int(stdout.Fd())) {
		return nil
	}
	return &Manager{
		stdin:        stdin,
		stdout:       stdout,
		notify:       notify,
		tc:           tc,
		gates:        gates,
		stallAfter:   5 * time.Second,
		pollEvery:    time.Second,
		releaseGrace: 3 * time.Second,
		done:         make(chan struct{}),
	}
}

// RunHook starts cmd on a PTY, streams its output to prefixed, and waits for
// it to exit, flagging the session as attachable whenever it looks like it
// is waiting for input. The returned bool is false when the PTY could not be
// started (cmd has not run) — the caller should fall back to plain pipes.
func (m *Manager) RunHook(ctx context.Context, cmd *exec.Cmd, label string, prefixed io.Writer) (bool, error) {
	master, err := startPTY(cmd, m.stdin)
	if err != nil {
		return false, err
	}
	s := &Session{label: label, master: master, prefixed: &crToLF{w: prefixed}}
	s.sink = s.prefixed
	m.register(s)

	pumpDone := make(chan struct{})
	go func() { s.pump(); close(pumpDone) }()

	watchDone := make(chan struct{})
	go func() {
		t := time.NewTicker(m.pollEvery)
		defer t.Stop()
		for {
			select {
			case <-watchDone:
				return
			case <-t.C:
				now := time.Now()
				m.setWaiting(s, s.det.waiting(now, m.stallAfter))
				m.maybeAutoRelease(s, now)
			}
		}
	}()

	err = cmd.Wait()
	close(watchDone)
	// Drain remaining output: the master reads EOF (EIO on Linux) once the
	// child exits, unless a grandchild still holds the slave open.
	select {
	case <-pumpDone:
	case <-time.After(drainGrace):
	}
	// Unregister (detaching and stopping the resize watcher) before closing
	// the master so nothing touches a closed file.
	m.unregister(s)
	master.Close()
	<-pumpDone
	return true, err
}

// Close stops the stdin listener and restores the terminal if a session is
// still attached. Idempotent.
func (m *Manager) Close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	m.detachLocked()
	close(m.done)
	m.mu.Unlock()
	// Unblock a pending listener read; best-effort (not all stdins support
	// deadlines, in which case the goroutine exits with the process).
	m.stdin.SetReadDeadline(time.Now())
}

func (m *Manager) register(s *Session) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.listenerOn && !m.closed {
		m.listenerOn = true
		go m.listen()
	}
}

func (m *Manager) unregister(s *Session) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.attached == s {
		m.detachLocked()
	}
	if slices.Contains(m.waiting, s) {
		m.dropWaitingLocked(s)
	}
}

// setWaiting flags or clears a session as input-starved. A newly flagged
// session takes focus immediately when nothing else holds it; otherwise it
// queues until the attached session detaches. When taking focus fails (raw
// mode unavailable), it falls back to the notice + picker flow, in which any
// change to the waiting list prints a fresh notice (so picker numbers always
// match a printed listing) and discards keystrokes typed against the old
// listing.
func (m *Manager) setWaiting(s *Session, w bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	flagged := slices.Contains(m.waiting, s)
	if !w {
		if flagged {
			m.dropWaitingLocked(s)
		}
		return
	}
	if flagged || m.attached == s {
		return
	}
	if m.attached == nil && m.attachLocked(s) {
		return
	}
	// Discard any bytes typed before this notice (stray keystrokes, escape
	// sequences) so the next Enter attaches instead of failing the pick parse.
	m.lineBuf = nil
	m.waiting = append(m.waiting, s)
	if m.attached == nil {
		m.printNoticeLocked()
	}
}

// dropWaitingLocked removes s from the waiting list, invalidates any
// half-typed pick (the numbering it referred to is gone), and reprints the
// notice for the sessions still waiting (picker-fallback mode only — while a
// session is attached the queue is silent).
func (m *Manager) dropWaitingLocked(s *Session) {
	m.waiting = removeSession(m.waiting, s)
	m.lineBuf = nil
	if len(m.waiting) > 0 && m.attached == nil {
		m.printNoticeLocked()
	}
}

func (m *Manager) printNoticeLocked() {
	if len(m.waiting) == 1 {
		fmt.Fprintf(m.notify, "mdp: [%s] looks like it is waiting for input — press Enter to attach\n", m.waiting[0].label)
		return
	}
	labels := make([]string, len(m.waiting))
	for i, w := range m.waiting {
		labels[i] = fmt.Sprintf("[%d] %s", i+1, w.label)
	}
	fmt.Fprintf(m.notify, "mdp: hooks waiting for input: %s — type a number then Enter to attach\n", strings.Join(labels, ", "))
}

// listen reads the user's terminal for the life of the manager. Stdin stays
// in cooked mode while no session is attached (so Ctrl-C still interrupts
// mdp normally); raw mode is entered only for the attached state.
func (m *Manager) listen() {
	buf := make([]byte, 256)
	for {
		n, err := m.stdin.Read(buf)
		select {
		case <-m.done:
			return
		default:
		}
		if n > 0 {
			m.handleInput(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

func (m *Manager) handleInput(p []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if s := m.attached; s != nil {
		// Raw mode: forward bytes to the hook's PTY; Ctrl-] detaches. Ctrl-C
		// arrives as a plain 0x03 byte here and goes to the hook, not mdp.
		// Writing under m.mu means RunHook's unregister/Close (which take the
		// lock) cannot close the master mid-write; if the hook stops reading,
		// the write unblocks with EIO once the child exits and the slave
		// closes.
		m.lastInput = time.Now()
		m.inputSinceAttach = true
		if i := bytes.IndexByte(p, detachByte); i >= 0 {
			if i > 0 {
				s.master.Write(p[:i])
			}
			m.detachLocked()
			return
		}
		s.master.Write(p)
		return
	}

	// Cooked mode: buffer until Enter, then interpret the line as a pick.
	m.lineBuf = append(m.lineBuf, p...)
	idx := bytes.IndexByte(m.lineBuf, '\n')
	if idx < 0 {
		return
	}
	line := strings.TrimSpace(string(m.lineBuf[:idx]))
	m.lineBuf = m.lineBuf[idx+1:]
	if len(m.waiting) == 0 {
		return
	}
	pick := 0 // bare Enter attaches to the longest-waiting session
	if line != "" {
		n, err := strconv.Atoi(line)
		if err != nil || n < 1 || n > len(m.waiting) {
			return
		}
		pick = n - 1
	}
	m.attachLocked(m.waiting[pick])
}

// attachLocked gives s focus: terminal goes raw, all gated output is held in
// memory, and the pending prompt is replayed. Returns false when raw mode is
// unavailable (caller falls back to the notice flow).
func (m *Manager) attachLocked(s *Session) bool {
	restore, err := m.tc.MakeRaw(int(m.stdin.Fd()))
	if err != nil {
		fmt.Fprintf(m.notify, "mdp: cannot attach to [%s]: %v\n", s.label, err)
		return false
	}
	m.restoreTerm = restore
	m.attached = s
	m.lastInput = time.Now()
	m.inputSinceAttach = false
	m.strongPrompt = s.det.partialPending()
	// No notice reprint here (it would garble the raw session) — just drop
	// the picked session and any leftover typed bytes.
	m.waiting = removeSession(m.waiting, s)
	m.lineBuf = nil
	// Hold the gates before the banner so nothing interleaves with the
	// interactive session; everything buffered flushes on detach.
	for _, g := range m.gates {
		g.Hold()
	}
	fmt.Fprintf(m.stdout, "\r\n--- attached to [%s] — Ctrl-] to detach, Ctrl-C is sent to the hook ---\r\n", s.label)
	// Replay the pending prompt so the user sees what the hook is asking.
	m.stdout.Write(s.det.pending())
	s.setSink(m.stdout)
	m.resizeStop = make(chan struct{})
	watchResize(m.stdin, s.master, m.resizeStop)
	return true
}

func (m *Manager) detachLocked() {
	s := m.attached
	if s == nil {
		return
	}
	m.attached = nil
	if m.resizeStop != nil {
		close(m.resizeStop)
		m.resizeStop = nil
	}
	s.setSink(s.prefixed)
	if m.restoreTerm != nil {
		m.restoreTerm()
		m.restoreTerm = nil
	}
	fmt.Fprintf(m.notify, "\r\n--- detached from [%s] ---\n", s.label)
	// Flush everything buffered while s had focus, then hand focus to the
	// next queued session (not on Close — the terminal is going away).
	for _, g := range m.gates {
		g.Release()
	}
	if !m.closed && len(m.waiting) > 0 && !m.attachLocked(m.waiting[0]) {
		m.printNoticeLocked()
	}
}

// maybeAutoRelease detaches s when the user looks done answering: no
// keystrokes for releaseGrace, the hook has produced output since the last
// keystroke (so a silent password prompt stays attached), and its tail no
// longer looks like a prompt (so a half-typed answer or an immediate second
// prompt keeps focus).
//
// Before the first keystroke, hook output is ambiguous: a false-positive
// attach whose hook moved on by itself, or noise (countdown, periodic log)
// from a hook still blocked on its prompt. Releasing on the latter would
// orphan the prompt — its non-promptish tail means the stall detector never
// re-fires. The attach trigger disambiguates: an unterminated partial line
// (strongPrompt) is almost certainly a real prompt, so focus is held until
// the user types, Ctrl-], or hook exit; a complete line ending in `:`/`?`/`>`
// is the false-positive-prone form, so resumed output releases (self-heal).
func (m *Manager) maybeAutoRelease(s *Session, now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.attached != s || m.closed {
		return
	}
	if !m.inputSinceAttach && m.strongPrompt {
		return
	}
	if now.Sub(m.lastInput) < m.releaseGrace {
		return
	}
	if !s.det.lastOutput().After(m.lastInput) {
		return
	}
	if s.det.promptish() {
		return
	}
	m.detachLocked()
}

func removeSession(list []*Session, s *Session) []*Session {
	if i := slices.Index(list, s); i >= 0 {
		return slices.Delete(list, i, i+1)
	}
	return list
}
