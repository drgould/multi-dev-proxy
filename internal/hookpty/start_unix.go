//go:build unix

package hookpty

import (
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/creack/pty"
)

// startPTY starts cmd with its stdio attached to a new pseudo-terminal and
// returns the master side, sized to match the user's terminal.
func startPTY(cmd *exec.Cmd, tty *os.File) (*os.File, error) {
	// pty.Start puts the hook in its own session (Setsid), so mdp's Ctrl-C no
	// longer reaches it at kernel level — and exec.CommandContext's default
	// Cancel is SIGKILL. Restore graceful shutdown: SIGINT the hook's process
	// group on ctx cancel, escalating to SIGKILL after WaitDelay.
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGINT)
	}
	cmd.WaitDelay = 5 * time.Second
	master, err := pty.Start(cmd)
	if err != nil {
		return nil, err
	}
	pty.InheritSize(tty, master)
	return master, nil
}

// watchResize forwards terminal size changes to the PTY master until stop is
// closed. Used only while a session is attached.
func watchResize(tty, master *os.File, stop <-chan struct{}) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGWINCH)
	go func() {
		defer signal.Stop(ch)
		for {
			select {
			case <-stop:
				return
			case <-ch:
				pty.InheritSize(tty, master)
			}
		}
	}()
}
