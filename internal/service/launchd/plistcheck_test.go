package launchd

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Carlboms-Data-AB/myai/internal/service"
)

// TestPlistPassesRealPlutil validates the generated agent with the same tool
// launchd's tooling uses, on a machine where it is available.
func TestPlistPassesRealPlutil(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("plutil is macOS only")
	}
	if _, err := exec.LookPath("plutil"); err != nil {
		t.Skip("plutil not available")
	}

	spec := service.Spec{
		Name:      "se.carlbomsdata.myai-opencode",
		Exec:      "/opt/homebrew/bin/opencode",
		Args:      []string{"web", "--hostname", "0.0.0.0", "--port", "4096"},
		Env:       map[string]string{"OPENCODE_SERVER_USERNAME": "opencode", "AMP": "a & b"},
		StdoutLog: "/tmp/out.log",
	}
	body, err := Plist(spec)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "agent.plist")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("plutil", "-lint", path).CombinedOutput(); err != nil {
		t.Fatalf("plutil rejected the generated plist: %v\n%s\n%s", err, out, body)
	}
}
