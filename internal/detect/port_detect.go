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
var portPattern = regexp.MustCompile(`https?://(?:localhost|127\.0\.0\.1|0\.0\.0\.0):(\d+)`)

// ErrPortNotDetected is returned when no port is found within the timeout.
var ErrPortNotDetected = errors.New("port not detected within timeout")

// DetectPort scans lines from reader looking for a URL with a port number.
// Returns the first detected port. Returns ErrPortNotDetected on timeout.
func DetectPort(reader io.Reader, timeout time.Duration) (int, error) {
	result := make(chan int, 1)
	done := make(chan struct{})

	go func() {
		defer close(done)
		scanner := bufio.NewScanner(reader)
		for scanner.Scan() {
			line := scanner.Text()
			if m := portPattern.FindStringSubmatch(line); m != nil {
				port, err := strconv.Atoi(m[1])
				if err == nil && port > 0 && port <= 65535 {
					select {
					case result <- port:
					default:
					}
					return
				}
			}
		}
	}()

	select {
	case port := <-result:
		return port, nil
	case <-time.After(timeout):
		return 0, fmt.Errorf("%w after %s", ErrPortNotDetected, timeout)
	case <-done:
		return 0, ErrPortNotDetected
	}
}

// TeeAndDetect tees stdout to output while detecting the port.
// Returns the detected port (or 0 and ErrPortNotDetected on timeout).
// The tee continues even after port detection.
func TeeAndDetect(stdout io.ReadCloser, output io.Writer, timeout time.Duration) (int, error) {
	pr, pw := io.Pipe()
	tr := io.TeeReader(stdout, pw)

	// Copy from TeeReader to output in background (drains both pr and output)
	go func() {
		defer pw.Close()
		io.Copy(output, tr)
	}()

	return DetectPort(pr, timeout)
}
