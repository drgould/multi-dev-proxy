package proxy

import (
	"crypto/tls"
	"net"
	"sync"
	"time"
)

// SmartListener wraps a net.Listener and transparently handles both plain HTTP
// and TLS connections on the same port. It peeks the first byte of each
// connection: if it is 0x16 (TLS ClientHello) and a TLS config is available,
// the connection is wrapped with tls.Server; otherwise it is passed through
// as plain TCP.
type SmartListener struct {
	inner     net.Listener
	mu        sync.RWMutex
	tlsConfig *tls.Config // nil means TLS is not available

	// peekTimeout bounds how long Accept waits for a connection's first
	// byte. The peek runs serially in the accept loop, so an open-but-silent
	// socket (e.g. a browser preconnect that Chrome holds idle for ~10s)
	// would otherwise stall every other new connection until it closes.
	peekTimeout time.Duration
}

// NewSmartListener creates a SmartListener around an existing net.Listener.
// tlsConfig may be nil (TLS disabled initially).
func NewSmartListener(ln net.Listener, tlsConfig *tls.Config) *SmartListener {
	return &SmartListener{inner: ln, tlsConfig: tlsConfig, peekTimeout: 5 * time.Second}
}

// SetTLSConfig replaces the TLS configuration. Pass nil to disable TLS.
// New connections will use the updated config; existing connections are not
// affected.
func (sl *SmartListener) SetTLSConfig(cfg *tls.Config) {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	sl.tlsConfig = cfg
}

// Accept waits for the next connection, peeks the first byte, and returns
// either a plain or TLS-unwrapped net.Conn.
func (sl *SmartListener) Accept() (net.Conn, error) {
	for {
		conn, err := sl.inner.Accept()
		if err != nil {
			return nil, err
		}

		// Read and buffer the first byte to detect protocol, bounded by
		// peekTimeout. The deadline is cleared immediately after so it never
		// leaks into the returned connection's reads.
		conn.SetReadDeadline(time.Now().Add(sl.peekTimeout))
		buf := make([]byte, 1)
		n, err := conn.Read(buf)
		conn.SetReadDeadline(time.Time{})
		if err != nil || n == 0 {
			if err == nil {
				// n=0 with no error shouldn't happen per io.Reader, pass through.
				return conn, nil
			}
			// Per-connection peek error: EOF from a preconnect socket closed
			// before sending data, or a timeout from one still sitting idle.
			// Surfacing it here would make http.Server.Serve exit — it treats
			// Accept errors as fatal to the listener — so drop the connection
			// and keep accepting.
			conn.Close()
			continue
		}
		peeked := &peekedConn{Conn: conn, buf: buf[:n]}

		sl.mu.RLock()
		cfg := sl.tlsConfig
		sl.mu.RUnlock()

		if buf[0] == 0x16 && cfg != nil {
			// TLS ClientHello — wrap with TLS.
			return tls.Server(peeked, cfg), nil
		}
		return peeked, nil
	}
}

// Close closes the underlying listener.
func (sl *SmartListener) Close() error {
	return sl.inner.Close()
}

// Addr returns the listener's network address.
func (sl *SmartListener) Addr() net.Addr {
	return sl.inner.Addr()
}

// peekedConn is a net.Conn that replays buffered bytes before reading from
// the underlying connection.
type peekedConn struct {
	net.Conn
	buf []byte
	off int
}

func (c *peekedConn) Read(p []byte) (int, error) {
	if c.off < len(c.buf) {
		n := copy(p, c.buf[c.off:])
		c.off += n
		if c.off >= len(c.buf) {
			c.buf = nil
		}
		if n == len(p) {
			return n, nil
		}
		// Fill the rest from the underlying conn.
		m, err := c.Conn.Read(p[n:])
		return n + m, err
	}
	return c.Conn.Read(p)
}

