package run

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// Call records one invocation made against a Fake runner.
type Call struct {
	Name string
	Args []string
	Env  []string
	Dir  string
}

// String renders the call as a command line, for readable assertions.
func (c Call) String() string {
	return strings.TrimSpace(c.Name + " " + strings.Join(c.Args, " "))
}

// Fake is a Runner for tests. Responses are matched against the command line
// by substring, so a test only has to describe the part it cares about.
type Fake struct {
	mu sync.Mutex
	// Calls records every invocation in order.
	Calls []Call
	// Responses maps a substring of the command line to a canned result.
	Responses map[string]Result
	// Errors maps a substring of the command line to an error.
	Errors map[string]error
	// Missing lists executable names Look should fail to resolve.
	Missing map[string]bool
	// DefaultErr is returned for commands with no configured response.
	DefaultErr error
}

// NewFake returns an empty Fake.
func NewFake() *Fake {
	return &Fake{Responses: map[string]Result{}, Errors: map[string]error{}, Missing: map[string]bool{}}
}

// Respond configures the result for command lines containing match.
func (f *Fake) Respond(match, output string) *Fake {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Responses[match] = Result{Output: output}
	return f
}

// Fail configures an error for command lines containing match.
func (f *Fake) Fail(match string, err error) *Fake {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Errors[match] = err
	return f
}

// Absent makes Look fail for the named executable.
func (f *Fake) Absent(name string) *Fake {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Missing[name] = true
	return f
}

// Look resolves a name unless it was marked absent.
func (f *Fake) Look(name string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Missing[name] {
		return "", fmt.Errorf("executable file not found: %s", name)
	}
	return "/usr/local/bin/" + name, nil
}

// Run records the call and returns the configured response.
func (f *Fake) Run(_ context.Context, spec Spec) (Result, error) {
	f.mu.Lock()
	call := Call{Name: spec.Name, Args: append([]string(nil), spec.Args...), Env: append([]string(nil), spec.Env...), Dir: spec.Dir}
	f.Calls = append(f.Calls, call)
	line := call.String()

	for match, err := range f.Errors {
		if strings.Contains(line, match) {
			f.mu.Unlock()
			return Result{ExitCode: 1}, err
		}
	}
	var out Result
	found := false
	for match, res := range f.Responses {
		if strings.Contains(line, match) {
			out, found = res, true
			break
		}
	}
	defaultErr := f.DefaultErr
	f.mu.Unlock()

	if spec.OnLine != nil && out.Output != "" {
		for _, l := range strings.Split(strings.TrimRight(out.Output, "\n"), "\n") {
			spec.OnLine(l)
		}
	}
	if !found && defaultErr != nil {
		return Result{ExitCode: 1}, defaultErr
	}
	return out, nil
}

// CommandLines returns every recorded call as a command line.
func (f *Fake) CommandLines() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.Calls))
	for _, c := range f.Calls {
		out = append(out, c.String())
	}
	return out
}

// Ran reports whether any recorded call contains match.
func (f *Fake) Ran(match string) bool {
	for _, line := range f.CommandLines() {
		if strings.Contains(line, match) {
			return true
		}
	}
	return false
}
