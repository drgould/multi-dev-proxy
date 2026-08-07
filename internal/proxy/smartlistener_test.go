package proxy

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"io"
	"math/big"
	"net"
	"testing"
	"time"
)

func generateTestCert(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
	}
	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{
		Certificate: [][]byte{certDER},
		PrivateKey:  key,
	}
}

func TestSmartListenerPlainHTTP(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	sl := NewSmartListener(ln, nil)
	defer sl.Close()

	go func() {
		conn, err := sl.Accept()
		if err != nil {
			return
		}
		// Plain connection — echo back what we receive.
		io.Copy(conn, conn)
		conn.Close()
	}()

	conn, err := net.Dial("tcp", sl.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	msg := "hello plain"
	conn.Write([]byte(msg))
	conn.(*net.TCPConn).CloseWrite()

	buf, err := io.ReadAll(conn)
	if err != nil {
		t.Fatal(err)
	}
	if string(buf) != msg {
		t.Errorf("got %q, want %q", buf, msg)
	}
}

func TestSmartListenerTLS(t *testing.T) {
	cert := generateTestCert(t)
	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	sl := NewSmartListener(ln, tlsCfg)
	defer sl.Close()

	go func() {
		conn, err := sl.Accept()
		if err != nil {
			return
		}
		// The connection should already be TLS-unwrapped.
		io.Copy(conn, conn)
		conn.Close()
	}()

	// Connect with TLS.
	conn, err := tls.Dial("tcp", sl.Addr().String(), &tls.Config{
		InsecureSkipVerify: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	msg := "hello tls"
	conn.Write([]byte(msg))
	conn.CloseWrite()

	buf, err := io.ReadAll(conn)
	if err != nil {
		t.Fatal(err)
	}
	if string(buf) != msg {
		t.Errorf("got %q, want %q", buf, msg)
	}
}

func TestSmartListenerNoTLSConfigRejectsNothing(t *testing.T) {
	// When no TLS config is set, a TLS ClientHello (0x16) is passed through
	// as plain data (the server side will see garbage, but it shouldn't crash).
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	sl := NewSmartListener(ln, nil)
	defer sl.Close()

	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := sl.Accept()
		if err != nil {
			return
		}
		accepted <- conn
	}()

	conn, err := net.Dial("tcp", sl.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	// Send a byte that looks like TLS ClientHello prefix.
	conn.Write([]byte{0x16, 0x03, 0x01})
	conn.Close()

	select {
	case srvConn := <-accepted:
		srvConn.Close()
	case <-time.After(2 * time.Second):
		t.Fatal("expected connection to be accepted")
	}
}

func TestSmartListenerSetTLSConfig(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	// Start with no TLS.
	sl := NewSmartListener(ln, nil)
	defer sl.Close()

	// Update to have TLS.
	cert := generateTestCert(t)
	sl.SetTLSConfig(&tls.Config{Certificates: []tls.Certificate{cert}})

	go func() {
		conn, err := sl.Accept()
		if err != nil {
			return
		}
		io.Copy(conn, conn)
		conn.Close()
	}()

	// Should now accept TLS connections.
	conn, err := tls.Dial("tcp", sl.Addr().String(), &tls.Config{
		InsecureSkipVerify: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	msg := "upgraded"
	conn.Write([]byte(msg))
	conn.CloseWrite()

	buf, err := io.ReadAll(conn)
	if err != nil {
		t.Fatal(err)
	}
	if string(buf) != msg {
		t.Errorf("got %q, want %q", buf, msg)
	}
}

func TestPeekedConnRead(t *testing.T) {
	// Verify that peekedConn replays buffered bytes then reads from underlying.
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	go func() {
		client.Write([]byte("world"))
		client.Close()
	}()

	pc := &peekedConn{Conn: server, buf: []byte("hello ")}

	buf, err := io.ReadAll(pc)
	if err != nil {
		t.Fatal(err)
	}
	if string(buf) != "hello world" {
		t.Errorf("got %q, want %q", buf, "hello world")
	}
}

// A connection closed before sending any data (e.g. a browser preconnect
// socket) must not surface its EOF as an Accept error — http.Server.Serve
// treats Accept errors as fatal and would tear down the whole listener.
func TestSmartListenerSkipsConnClosedBeforeData(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	sl := NewSmartListener(ln, nil)
	defer sl.Close()

	// Connect and close immediately without writing — peek sees EOF.
	dead, err := net.Dial("tcp", sl.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	dead.Close()

	// A real client right behind it.
	live, err := net.Dial("tcp", sl.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	if _, err := live.Write([]byte("GET")); err != nil {
		t.Fatal(err)
	}

	// Accept must skip the dead connection and return the live one.
	got := make(chan error, 1)
	go func() {
		conn, err := sl.Accept()
		if err != nil {
			got <- err
			return
		}
		buf := make([]byte, 3)
		if _, err := io.ReadFull(conn, buf); err != nil {
			got <- err
			return
		}
		if string(buf) != "GET" {
			got <- fmt.Errorf("peeked replay got %q, want %q", buf, "GET")
			return
		}
		conn.Close()
		got <- nil
	}()

	select {
	case err := <-got:
		if err != nil {
			t.Fatalf("Accept should skip the closed connection, got error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Accept did not return within 2s")
	}
}

// An open-but-silent connection (browser preconnect) must not stall Accept
// beyond peekTimeout, and the peek deadline must not leak into the returned
// connection's subsequent reads.
func TestSmartListenerIdleConnDoesNotBlockOthers(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	sl := NewSmartListener(ln, nil)
	sl.peekTimeout = 100 * time.Millisecond
	defer sl.Close()

	// Idle socket: connects but never writes.
	idle, err := net.Dial("tcp", sl.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer idle.Close()

	// Live client right behind it.
	live, err := net.Dial("tcp", sl.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	if _, err := live.Write([]byte("G")); err != nil {
		t.Fatal(err)
	}

	// Accept must drop the idle socket after the peek timeout and return the
	// live connection, instead of blocking until the idle socket closes.
	start := time.Now()
	conn, err := sl.Accept()
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	defer conn.Close()
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Accept blocked %v on an idle connection", elapsed)
	}

	// Deadline must be cleared: bytes arriving after the peek-timeout window
	// still read fine on the accepted connection.
	go func() {
		time.Sleep(3 * sl.peekTimeout) // comfortably clears the peek deadline
		live.Write([]byte("ET"))
	}()
	buf := make([]byte, 3)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("read after peek-timeout window: %v", err)
	}
	if string(buf) != "GET" {
		t.Errorf("got %q, want %q", buf, "GET")
	}
}
