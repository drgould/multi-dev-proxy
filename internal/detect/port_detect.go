package detect

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"time"
)

// portPattern matches a URL with a port in stdout from a dev server.
// Intentionally URL-based to avoid false positives from bare port numbers.
// Matches: http://localhost:3000, https://127.0.0.1:4001, http://0.0.0.0:8080
var portPattern = regexp.MustCompile(`(https?)://(?:localhost|127\.0\.0\.1|0\.0\.0\.0):(\d+)`)

// ErrPortNotDetected is returned when no port is found within the timeout.
var ErrPortNotDetected = errors.New("port not detected within timeout")

// DetectPort scans lines from reader looking for a URL with a port number.
// Returns the first detected port. Returns ErrPortNotDetected on timeout.
type PortResult struct {
	Port   int
	Scheme string // "http" or "https"
}

func DetectPort(reader io.Reader, timeout time.Duration) (PortResult, error) {
	result := make(chan PortResult, 1)
	done := make(chan struct{})

	go func() {
		defer close(done)
		scanner := bufio.NewScanner(reader)
		for scanner.Scan() {
			line := scanner.Text()
			if m := portPattern.FindStringSubmatch(line); m != nil {
				port, err := strconv.Atoi(m[2])
				if err == nil && port > 0 && port <= 65535 {
					select {
					case result <- PortResult{Port: port, Scheme: m[1]}:
					default:
					}
					return
				}
			}
		}
	}()

	select {
	case pr := <-result:
		return pr, nil
	case <-time.After(timeout):
		return PortResult{}, fmt.Errorf("%w after %s", ErrPortNotDetected, timeout)
	case <-done:
		select {
		case pr := <-result:
			return pr, nil
		default:
			return PortResult{}, ErrPortNotDetected
		}
	}
}

// TeeAndDetect tees stdout to output while detecting the port.
// Returns the detected port (or 0 and ErrPortNotDetected on timeout).
// The tee continues even after port detection.
func TeeAndDetect(stdout io.ReadCloser, output io.Writer, timeout time.Duration) (PortResult, error) {
	pr, pw := io.Pipe()
	tr := io.TeeReader(stdout, pw)

	go func() {
		defer pw.Close()
		io.Copy(output, tr)
	}()

	return DetectPort(pr, timeout)
}
