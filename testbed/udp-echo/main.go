package main

import (
	"fmt"
	"net"
	"os"
	"time"
)

// A tiny UDP echo server used by the testbed to demonstrate the
// `protocol: udp` PortMapping field: mdp allocates the host port via a
// UDP-aware free-port check and skips it in the depends_on readiness probe.
//
// Reads UDP_PORT (set by mdp via the `ports:` mapping), binds, echoes each
// incoming datagram back to the sender, and prints one line per packet.
//
// Try it:
//   printf 'hello' | socat -t1 - UDP:127.0.0.1:$UDP_PORT
//   printf 'hello' | ncat -u --send-only 127.0.0.1 $UDP_PORT
func main() {
	port := os.Getenv("UDP_PORT")
	if port == "" {
		fmt.Fprintln(os.Stderr, "UDP_PORT not set")
		os.Exit(1)
	}
	name := os.Getenv("NAME")
	if name == "" {
		name = "udp-echo"
	}
	addr := ":" + port
	pc, err := net.ListenPacket("udp", addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listen udp %s: %v\n", addr, err)
		os.Exit(1)
	}
	defer pc.Close()
	fmt.Printf("[%s] listening on udp %s\n", name, addr)

	buf := make([]byte, 2048)
	for {
		n, src, err := pc.ReadFrom(buf)
		if err != nil {
			fmt.Fprintf(os.Stderr, "read: %v\n", err)
			return
		}
		ts := time.Now().Format("15:04:05.000")
		fmt.Printf("[%s %s] %d bytes from %s: %q\n", name, ts, n, src, buf[:n])
		if _, err := pc.WriteTo(buf[:n], src); err != nil {
			fmt.Fprintf(os.Stderr, "write: %v\n", err)
		}
	}
}
