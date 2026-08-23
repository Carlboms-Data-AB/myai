//go:build !windows

package secrets

import "os"

// Restrict limits a file to its owner.
func Restrict(path string) error {
	return os.Chmod(path, 0o600)
}
