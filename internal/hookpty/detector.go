// Package hookpty runs service setup/shutdown hooks on a pseudo-terminal so
// hooks that prompt for input can be detected and forwarded to the user's
// terminal instead of hanging silently.
package hookpty

import (
	"bytes"
	"regexp"
	"sync"
	"time"
)

// ansiSeqRe matches ANSI CSI escape sequences (SGR colors, cursor codes,
// erase-line, etc.) so a stalled line consisting only of terminal control
// traffic is not mistaken for a prompt. Intentionally broader than the
// similar regex in cmd/mdp/run.go: PTY output includes private-mode
// sequences like \x1b[?25l (cursor hide), which the `?` in the class covers;
// run.go's variant only needs to strip compose log-prefix coloring.
var ansiSeqRe = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)

// partialCap bounds the retained partial line so a pathological
// no-newline stream cannot grow memory without bound.
const partialCap = 4096

// detector tracks a hook's output stream and decides whether the hook looks
// like it is waiting for input: no output for a stall window AND either the
// last output line is unterminated (no trailing \n or \r) and non-blank once
// ANSI sequences are stripped, OR the last complete line looks like a prompt
// (ends with ':', '?', or '>') — catching e.g. `echo "Enter API key:"` before
// a `read`. Newline-terminated silence (slow compiles) and \r-rewriting
// spinners leave an empty partial and a non-promptish last line, so neither
// fires.
type detector struct {
	mu        sync.Mutex
	partial   []byte // bytes after the last \n or \r
	lastLine  []byte // most recent complete line (between the last two \n/\r)
	lastWrite time.Time
}

// observe records an output chunk read from the PTY at time now.
func (d *detector) observe(p []byte, now time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.lastWrite = now
	if idx := bytes.LastIndexAny(p, "\n\r"); idx >= 0 {
		head := p[:idx]
		if j := bytes.LastIndexAny(head, "\n\r"); j >= 0 {
			d.lastLine = append(d.lastLine[:0], head[j+1:]...)
		} else {
			d.lastLine = append(append(d.lastLine[:0], d.partial...), head...)
		}
		d.partial = append(d.partial[:0], p[idx+1:]...)
	} else {
		d.partial = append(d.partial, p...)
	}
	if len(d.partial) > partialCap {
		d.partial = d.partial[len(d.partial)-partialCap:]
	}
	if len(d.lastLine) > partialCap {
		d.lastLine = d.lastLine[len(d.lastLine)-partialCap:]
	}
}

// pending returns the bytes to replay on attach so the user sees what the
// hook is asking: the unterminated partial line, or — when the prompt ended
// with a newline — the last complete line.
func (d *detector) pending() []byte {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.partial) > 0 {
		return append([]byte(nil), d.partial...)
	}
	if len(d.lastLine) > 0 {
		return append(append([]byte(nil), d.lastLine...), '\r', '\n')
	}
	return nil
}

// waiting reports whether the stream looks input-starved at time now: at
// least stallAfter has elapsed since the last output, and the pending partial
// line has visible text or the last complete line ends prompt-like.
func (d *detector) waiting(now time.Time, stallAfter time.Duration) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.lastWrite.IsZero() || now.Sub(d.lastWrite) < stallAfter {
		return false
	}
	return d.promptishLocked()
}

// promptish reports whether the stream's tail currently looks like a prompt
// (the content half of waiting, without the stall-time gate). Used by the
// auto-release check: a hook that moved past its prompt no longer looks
// promptish even though it produced output recently.
func (d *detector) promptish() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.promptishLocked()
}

// partialPending reports whether an unterminated line with visible text is
// pending — the strong prompt signal (e.g. `Overwrite? (y/n) ` before a
// read), as opposed to the weaker complete-line-ends-in-:/?/> heuristic.
func (d *detector) partialPending() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(bytes.TrimSpace(ansiSeqRe.ReplaceAll(d.partial, nil))) > 0
}

func (d *detector) promptishLocked() bool {
	if visible := bytes.TrimSpace(ansiSeqRe.ReplaceAll(d.partial, nil)); len(visible) > 0 {
		return true
	}
	last := bytes.TrimSpace(ansiSeqRe.ReplaceAll(d.lastLine, nil))
	if len(last) == 0 {
		return false
	}
	switch last[len(last)-1] {
	case ':', '?', '>':
		return true
	}
	return false
}

// lastOutput returns the time of the most recent observed output chunk.
func (d *detector) lastOutput() time.Time {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.lastWrite
}
