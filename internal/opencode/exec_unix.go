//go:build !windows

package opencode

import (
	"context"
	"os"
	"syscall"
)

// execReplace replaces the current process with the command, so the service
// manager supervises OpenCode itself and signals reach it directly.
func execReplace(_ context.Context, path string, args []string, env map[string]string) error {
	environ := os.Environ()
	for k, v := range env {
		environ = append(environ, k+"="+v)
	}
	return syscall.Exec(path, append([]string{path}, args...), environ)
}
