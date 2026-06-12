package hookpty

import (
	"io"
	"os"
	"sync"
	"time"
)

// Session is one hook process running on a PTY. Its pump goroutine copies
// master output to the current sink: the service's prefixed log writer while
// detached, or the raw terminal while the user is attached.
type Session struct {
	label  string
	master *os.File
	det    detector

	mu       sync.Mutex
	sink     io.Writer
	prefixed io.Writer // crToLF-wrapped service log writer (detached sink)
}

func (s *Session) setSink(w io.Writer) {
	s.mu.Lock()
	s.sink = w
	s.mu.Unlock()
}

func (s *Session) writeSink(p []byte) {
	s.mu.Lock()
	w := s.sink
	s.mu.Unlock()
	w.Write(p)
}

// pump copies PTY master output to the session sink, feeding the stall
// detector. Returns when the master reaches EOF (child exited). On Linux a
// master read after child exit fails with EIO — treated as EOF.
func (s *Session) pump() {
	buf := make([]byte, 32*1024)
	for {
		n, err := s.master.Read(buf)
		if n > 0 {
			s.det.observe(buf[:n], time.Now())
			s.writeSink(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

// crToLF rewrites \r\n and bare \r to \n so PTY output (where the line
// discipline emits \r\n, and spinners rewrite lines with \r) does not
// accumulate into one giant buffered line inside a prefixWriter that only
// splits on \n.
type crToLF struct {
	w         io.Writer
	pendingCR bool
}

func (c *crToLF) Write(p []byte) (int, error) {
	out := make([]byte, 0, len(p))
	for _, b := range p {
		switch b {
		case '\r':
			if c.pendingCR {
				out = append(out, '\n')
			}
			c.pendingCR = true
		case '\n':
			out = append(out, '\n')
			c.pendingCR = false
		default:
			if c.pendingCR {
				out = append(out, '\n')
				c.pendingCR = false
			}
			out = append(out, b)
		}
	}
	if _, err := c.w.Write(out); err != nil {
		return 0, err
	}
	return len(p), nil
}
