// Package prompt defines how MyAI asks the operator a question. Core packages
// depend on the interface rather than on standard input, so the same code path
// works from a terminal, a graphical frontend or a non-interactive run.
package prompt

import "errors"

// ErrNotInteractive is returned when an answer is required but no operator is
// available to give one.
var ErrNotInteractive = errors.New("input required but the session is not interactive")

// Asker collects answers from the operator.
type Asker interface {
	// Confirm asks a yes or no question.
	Confirm(question string, defaultYes bool) (bool, error)
	// Text asks for a line of input. An empty answer means the default.
	Text(question, defaultValue string) (string, error)
	// Secret asks for input that must not be echoed, such as a password.
	Secret(question string) (string, error)
	// Choose asks the operator to pick one of several options and returns its
	// index.
	Choose(question string, options []string, defaultIndex int) (int, error)
	// Interactive reports whether answers can actually be collected.
	Interactive() bool
}

// NonInteractive refuses every question. Operations that need an answer fail
// cleanly instead of hanging or guessing.
type NonInteractive struct{}

func (NonInteractive) Confirm(string, bool) (bool, error)  { return false, ErrNotInteractive }
func (NonInteractive) Text(string, string) (string, error) { return "", ErrNotInteractive }
func (NonInteractive) Secret(string) (string, error)       { return "", ErrNotInteractive }
func (NonInteractive) Choose(string, []string, int) (int, error) {
	return 0, ErrNotInteractive
}
func (NonInteractive) Interactive() bool { return false }

// Or returns a, or a NonInteractive asker when a is nil.
func Or(a Asker) Asker {
	if a == nil {
		return NonInteractive{}
	}
	return a
}
