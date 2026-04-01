package proxy

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
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
