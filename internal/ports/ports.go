package ports

import (
	"fmt"
	"math/rand"
	"net"
	"strconv"
	"strings"
)

// PortRange defines an inclusive range of ports.
type PortRange struct {
	Start int
	End   int
}

// DefaultRange is the default port range for spawned dev servers.
var DefaultRange = PortRange{Start: 10000, End: 60000}

// ParseRange parses a "start-end" string into a PortRange.
// Returns error if format is invalid, reversed, or out of valid range.
func ParseRange(s string) (PortRange, error) {
	parts := strings.SplitN(s, "-", 2)
	if len(parts) != 2 {
		return PortRange{}, fmt.Errorf("invalid port range %q: expected format start-end", s)
	}
	start, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return PortRange{}, fmt.Errorf("invalid port range %q: start is not a number", s)
	}
	end, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return PortRange{}, fmt.Errorf("invalid port range %q: end is not a number", s)
	}
	if start >= end {
		return PortRange{}, fmt.Errorf("invalid port range %q: start must be less than end", s)
	}
	if start < 1024 || end > 65535 {
		return PortRange{}, fmt.Errorf("invalid port range %q: ports must be in range 1024-65535", s)
	}
	if end-start < 9 {
		return PortRange{}, fmt.Errorf("invalid port range %q: range must span at least 10 ports", s)
	}
	return PortRange{Start: start, End: end}, nil
}

// IsPortFree checks whether a port is available by attempting to bind to it.
func IsPortFree(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return false
	}
	ln.Close()
	return true
}

// FindFreePort picks a random free port in r that is not in the exclude list.
// Returns an error if no free port is found after 100 attempts.
func FindFreePort(r PortRange, exclude []int) (int, error) {
	excluded := make(map[int]bool, len(exclude))
	for _, p := range exclude {
		excluded[p] = true
	}
	span := r.End - r.Start + 1
	for i := 0; i < 100; i++ {
		port := r.Start + rand.Intn(span)
		if excluded[port] {
			continue
		}
		if IsPortFree(port) {
			return port, nil
		}
	}
	return 0, fmt.Errorf("no free port found in range %d-%d after 100 attempts", r.Start, r.End)
}
