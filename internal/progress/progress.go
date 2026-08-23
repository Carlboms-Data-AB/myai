// Package progress defines how long-running MyAI operations report what they
// are doing. Core packages depend only on these interfaces, never on the
// terminal, so the same operations can drive a graphical frontend later.
package progress

// Reporter receives narration from a running operation.
type Reporter interface {
	// Step announces a new stage of work.
	Step(message string)
	// Info reports a detail within the current stage.
	Info(message string)
	// Warn reports something that did not stop the operation.
	Warn(message string)
	// Check reports the outcome of a named verification.
	Check(name string, ok bool, detail string)
	// Download reports byte progress for a named artifact. Total is zero when
	// the size is unknown. Done equal to Total marks completion.
	Download(name string, done, total int64)
}

// Discard is a Reporter that ignores everything. It keeps core code free of
// nil checks and makes non-interactive use straightforward.
type Discard struct{}

func (Discard) Step(string)                   {}
func (Discard) Info(string)                   {}
func (Discard) Warn(string)                   {}
func (Discard) Check(string, bool, string)    {}
func (Discard) Download(string, int64, int64) {}

// Or returns r, or a Discard reporter when r is nil.
func Or(r Reporter) Reporter {
	if r == nil {
		return Discard{}
	}
	return r
}
