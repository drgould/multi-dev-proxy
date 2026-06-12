//go:build windows

package hookpty

import (
	"errors"
	"os"
	"os/exec"
)

var errPTYUnsupported = errors.New("hookpty: PTY not supported on windows")

// startPTY is unsupported on Windows — callers fall back to plain pipes.
func startPTY(cmd *exec.Cmd, tty *os.File) (*os.File, error) {
	return nil, errPTYUnsupported
}

func watchResize(tty, master *os.File, stop <-chan struct{}) {}
