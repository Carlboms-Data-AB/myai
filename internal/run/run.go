// Package run wraps external command execution behind an interface. Every
// MyAI component that shells out to launchctl, systemctl, nssm, mlx-serve,
// llama-server or opencode goes through this package, which keeps those
// components testable on a machine where none of those tools exist.
package run

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// Spec describes one command invocation.
type Spec struct {
	// Name is the executable, either an absolute path or a name on PATH.
	Name string
	// Args are the arguments, passed without shell interpretation.
	Args []string
	// Env holds extra environment entries as "KEY=value". They are added to
	// the current environment rather than replacing it.
	Env []string
	// Dir is the working directory. Empty means the current one.
	Dir string
	// Stdin, when set, is written to the command.
	Stdin string
	// OnLine, when set, receives each line of combined output as it appears.
	OnLine func(string)
	// Interactive connects the command to the terminal, for OpenCode's TUI.
	Interactive bool
}

// Result is the outcome of a command.
type Result struct {
	Output   string
	ExitCode int
}

// Runner executes commands.
type Runner interface {
	// Run executes a command and returns its combined output.
	Run(ctx context.Context, spec Spec) (Result, error)
	// Look resolves an executable name to a path, as exec.LookPath does.
	Look(name string) (string, error)
}

// Exec is the real Runner.
type Exec struct{}

// Look resolves an executable on PATH.
func (Exec) Look(name string) (string, error) { return exec.LookPath(name) }

// Run executes the command described by spec.
func (Exec) Run(ctx context.Context, spec Spec) (Result, error) {
	cmd := exec.CommandContext(ctx, spec.Name, spec.Args...)
	cmd.Dir = spec.Dir
	if len(spec.Env) > 0 {
		cmd.Env = append(os.Environ(), spec.Env...)
	}

	if spec.Interactive {
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		err := cmd.Run()
		return Result{ExitCode: exitCode(cmd)}, err
	}

	if spec.Stdin != "" {
		cmd.Stdin = strings.NewReader(spec.Stdin)
	}

	var buf bytes.Buffer
	if spec.OnLine != nil {
		pr, pw := io.Pipe()
		cmd.Stdout = pw
		cmd.Stderr = pw
		done := make(chan struct{})
		go func() {
			defer close(done)
			scanner := bufio.NewScanner(pr)
			scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
			for scanner.Scan() {
				line := scanner.Text()
				buf.WriteString(line)
				buf.WriteByte('\n')
				spec.OnLine(line)
			}
		}()
		err := cmd.Run()
		pw.Close()
		<-done
		return result(&buf, cmd, err)
	}

	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return result(&buf, cmd, err)
}

// exitCode reports a command's exit status. A command that never started has
// no ProcessState, which ExitCode reports as -1.
func exitCode(cmd *exec.Cmd) int {
	return cmd.ProcessState.ExitCode()
}

func result(buf *bytes.Buffer, cmd *exec.Cmd, err error) (Result, error) {
	res := Result{Output: buf.String(), ExitCode: exitCode(cmd)}
	if err != nil {
		return res, fmt.Errorf("%s: %w: %s", cmd.Path, err, strings.TrimSpace(trim(res.Output)))
	}
	return res, nil
}

func trim(s string) string {
	const max = 2000
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// Quiet runs a command and reports only whether it succeeded, which suits
// probes such as "is this service loaded".
func Quiet(ctx context.Context, r Runner, name string, args ...string) bool {
	_, err := r.Run(ctx, Spec{Name: name, Args: args})
	return err == nil
}

// Available reports whether an executable can be found on PATH.
func Available(r Runner, name string) bool {
	_, err := r.Look(name)
	return err == nil
}
