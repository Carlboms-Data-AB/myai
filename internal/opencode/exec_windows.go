//go:build windows

package opencode

import (
	"context"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
)

// execReplace runs the command as a child process and waits for it. Windows
// has no exec that replaces the running process, so MyAI stays in place as a
// thin parent and passes on a stop request.
func execReplace(ctx context.Context, path string, args []string, env map[string]string) error {
	cmd := exec.Command(path, args...)
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return err
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(stop)

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	for {
		select {
		case err := <-done:
			return err
		case <-stop:
			if cmd.Process != nil {
				cmd.Process.Kill()
			}
		case <-ctx.Done():
			if cmd.Process != nil {
				cmd.Process.Kill()
			}
			return ctx.Err()
		}
	}
}
