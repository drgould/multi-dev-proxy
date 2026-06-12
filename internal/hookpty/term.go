package hookpty

import "golang.org/x/term"

// termController abstracts raw-mode control of the user's terminal so the
// attach state machine can be tested with a fake.
type termController interface {
	IsTerminal(fd int) bool
	// MakeRaw switches the terminal to raw mode and returns a restore func.
	MakeRaw(fd int) (restore func() error, err error)
}

// RealTerm implements termController with golang.org/x/term.
type RealTerm struct{}

func (RealTerm) IsTerminal(fd int) bool { return term.IsTerminal(fd) }

func (RealTerm) MakeRaw(fd int) (func() error, error) {
	st, err := term.MakeRaw(fd)
	if err != nil {
		return nil, err
	}
	return func() error { return term.Restore(fd, st) }, nil
}
