//go:build windows

package secrets

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Restrict removes inherited access and grants the current user full control,
// which is the closest Windows equivalent of mode 600.
func Restrict(path string) error {
	user := strings.TrimSpace(os.Getenv("USERNAME"))
	if user == "" {
		// Without a username there is nothing to grant to; leaving inheritance
		// intact is safer than stripping all access from the file.
		return nil
	}
	domain := strings.TrimSpace(os.Getenv("USERDOMAIN"))
	principal := user
	if domain != "" {
		principal = domain + `\` + user
	}

	cmd := exec.Command("icacls", path, "/inheritance:r", "/grant:r", principal+":(F)")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("icacls %s: %w: %s", path, err, strings.TrimSpace(string(out)))
	}
	return nil
}
