package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/carlbomsdata/myai/internal/config"
	"github.com/carlbomsdata/myai/internal/secrets"
)

// writeLegacyInstall recreates what the Bash prototype leaves on a Mac.
func writeLegacyInstall(t *testing.T, home, configBody, authBody string) {
	t.Helper()
	dir := filepath.Join(home, ".config", "local-ai-mac-mini")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config"), []byte(configBody), 0o600); err != nil {
		t.Fatal(err)
	}
	if authBody != "" {
		if err := os.WriteFile(filepath.Join(dir, "web-auth"), []byte(authBody), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

const legacyConfig = `MODEL=mlx-community/Qwen3.5-9B-6bit
CONTEXT=131072
OUTPUT=16384
PORT=11234
WEB=true
EGO=false
IDLE=0
WEB_PORT=4096
`

func TestMigrationImportsPrototypeSettings(t *testing.T) {
	a, _, env := newTestApp(t, "darwin", "arm64")
	writeLegacyInstall(t, env.Home, legacyConfig, "WEB_USERNAME=opencode\nWEB_PASSWORD=abc123def456\n")

	if err := a.MigrateFromPrototype(context.Background()); err != nil {
		t.Fatalf("MigrateFromPrototype: %v", err)
	}

	cfg := a.Config()
	// The prototype stored the raw repository id; MyAI should recognise it as
	// the catalog model so it is never re-downloaded under a new name.
	if cfg.ActiveModel != "qwen3.5-9b" {
		t.Errorf("ActiveModel = %q, want the catalog id for the installed artifact", cfg.ActiveModel)
	}
	if cfg.Inference.Port != 11234 || cfg.Web.Port != 4096 {
		t.Errorf("ports not imported: %+v", cfg)
	}
	if cfg.Inference.Context != 131072 || cfg.Inference.Output != 16384 {
		t.Errorf("limits not imported: %+v", cfg.Inference)
	}
	if !cfg.Tools.WebSearch || cfg.Tools.BrowserAutomation {
		t.Errorf("tool settings not imported: %+v", cfg.Tools)
	}
}

func TestMigrationKeepsTheExistingWebPassword(t *testing.T) {
	a, _, env := newTestApp(t, "darwin", "arm64")
	writeLegacyInstall(t, env.Home, legacyConfig, "WEB_USERNAME=opencode\nWEB_PASSWORD=keepthisexactpassword\n")

	if err := a.MigrateFromPrototype(context.Background()); err != nil {
		t.Fatal(err)
	}
	creds, err := secrets.Load(env.CredentialsFile())
	if err != nil {
		t.Fatal(err)
	}
	if creds.Password != "keepthisexactpassword" {
		t.Errorf("password = %q; an upgrade must not invalidate saved logins", creds.Password)
	}
}

func TestMigrationNeverTouchesDownloadedModels(t *testing.T) {
	a, _, env := newTestApp(t, "darwin", "arm64")
	writeLegacyInstall(t, env.Home, legacyConfig, "")

	// Stand in for the real multi-gigabyte download.
	modelFile := filepath.Join(env.MLXModelDir(), "mlx-community", "Qwen3.5-9B-6bit", "model.safetensors")
	if err := os.MkdirAll(filepath.Dir(modelFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(modelFile, []byte("weights"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := a.MigrateFromPrototype(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(modelFile); err != nil {
		t.Fatalf("migration removed a downloaded model: %v", err)
	}
}

func TestMigrationRunsOnlyOnce(t *testing.T) {
	a, _, env := newTestApp(t, "darwin", "arm64")
	writeLegacyInstall(t, env.Home, legacyConfig, "")

	if err := a.MigrateFromPrototype(context.Background()); err != nil {
		t.Fatal(err)
	}
	// A later change must not be reverted by a second migration.
	if err := a.Update(func(c *config.Config) { c.Inference.Port = 12000 }); err != nil {
		t.Fatal(err)
	}
	if err := a.MigrateFromPrototype(context.Background()); err != nil {
		t.Fatal(err)
	}
	if a.Config().Inference.Port != 12000 {
		t.Error("migration overwrote a configuration that already existed")
	}
}

func TestMigrationWithNoPrototypeIsHarmless(t *testing.T) {
	a, _, _ := newTestApp(t, "linux", "amd64")
	if err := a.MigrateFromPrototype(context.Background()); err != nil {
		t.Errorf("MigrateFromPrototype: %v", err)
	}
}

func TestMigrationImportsIdleEvictionAsMinutes(t *testing.T) {
	a, _, env := newTestApp(t, "darwin", "arm64")
	writeLegacyInstall(t, env.Home, strings.Replace(legacyConfig, "IDLE=0", "IDLE=900", 1), "")

	if err := a.MigrateFromPrototype(context.Background()); err != nil {
		t.Fatal(err)
	}
	cfg := a.Config()
	if cfg.Inference.KeepInRAM {
		t.Error("a configured idle eviction means the model was not being kept in RAM")
	}
	if cfg.Inference.IdleUnloadMinutes != 15 {
		t.Errorf("IdleUnloadMinutes = %d, want 15", cfg.Inference.IdleUnloadMinutes)
	}
}

func TestInstallCommandCopiesBinaryAndSetsPath(t *testing.T) {
	a, _, env := newTestApp(t, "linux", "amd64")
	if err := a.env.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", "/bin/zsh")
	t.Setenv("PATH", "/usr/bin")

	if _, err := a.InstallCommand(); err != nil {
		t.Fatalf("InstallCommand: %v", err)
	}
	if !fileExists(env.Executable()) {
		t.Fatal("the myai command was not installed")
	}
	profile, err := os.ReadFile(filepath.Join(env.Home, ".zshrc"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(profile), env.Bin) {
		t.Errorf("PATH entry not added:\n%s", profile)
	}
	if strings.Count(string(profile), pathBegin) != 1 {
		t.Errorf("PATH block should appear exactly once:\n%s", profile)
	}
}

func TestInstallCommandIsIdempotent(t *testing.T) {
	a, _, env := newTestApp(t, "linux", "amd64")
	if err := a.env.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", "/bin/zsh")
	t.Setenv("PATH", "/usr/bin")

	for i := 0; i < 3; i++ {
		if _, err := a.InstallCommand(); err != nil {
			t.Fatalf("InstallCommand %d: %v", i, err)
		}
	}
	profile, err := os.ReadFile(filepath.Join(env.Home, ".zshrc"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(profile), pathBegin) != 1 {
		t.Errorf("repeated installs duplicated the PATH block:\n%s", profile)
	}
}

func TestRemoveBlockLeavesOtherLinesAlone(t *testing.T) {
	body := "export EDITOR=vim\n" + pathBegin + "\nexport PATH=\"/x:$PATH\"\n" + pathEnd + "\nalias ll='ls -l'"
	got := removeBlock(body)
	if strings.Contains(got, "PATH=\"/x") {
		t.Errorf("block not removed:\n%s", got)
	}
	if !strings.Contains(got, "export EDITOR=vim") || !strings.Contains(got, "alias ll=") {
		t.Errorf("unrelated lines were lost:\n%s", got)
	}
}

func TestInstallOptionPresets(t *testing.T) {
	if !FullInstall().Model {
		t.Error("a full install should be willing to download the model")
	}
	if UpgradeOnly().Model {
		t.Error("an upgrade must never force a model download")
	}
	if !UpgradeOnly().Dependencies || !UpgradeOnly().Services || !UpgradeOnly().Command {
		t.Errorf("an upgrade should refresh everything else: %+v", UpgradeOnly())
	}
}

func TestApplyLegacyConfigRejectsNonsense(t *testing.T) {
	cfg := config.Default()
	if err := applyLegacyConfig(&cfg, "PORT=4096\nWEB_PORT=4096\n"); err == nil {
		t.Error("clashing ports should be rejected rather than imported")
	}
}

func TestAssetNamePerPlatform(t *testing.T) {
	tests := map[[2]string]string{
		{"darwin", "arm64"}:  "myai-darwin-arm64",
		{"linux", "amd64"}:   "myai-linux-amd64",
		{"linux", "arm64"}:   "myai-linux-arm64",
		{"windows", "amd64"}: "myai-windows-amd64.exe",
		{"windows", "arm64"}: "myai-windows-arm64.exe",
	}
	for p, want := range tests {
		got, err := AssetName(p[0], p[1])
		if err != nil {
			t.Fatalf("%s/%s: %v", p[0], p[1], err)
		}
		if got != want {
			t.Errorf("%s/%s = %q, want %q", p[0], p[1], got, want)
		}
	}
	if _, err := AssetName("darwin", "386"); err == nil {
		t.Error("expected an error for an unbuilt architecture")
	}
}

func TestExpectedSum(t *testing.T) {
	sums := `abc123  myai-darwin-arm64
def456  myai-linux-amd64
`
	got, err := ExpectedSum(sums, "myai-linux-amd64")
	if err != nil {
		t.Fatal(err)
	}
	if got != "def456" {
		t.Errorf("ExpectedSum = %q", got)
	}
	if _, err := ExpectedSum(sums, "myai-windows-arm64.exe"); err == nil {
		t.Error("expected an error when no checksum is published")
	}
}

func TestReplaceExecutableKeepsTheTargetOnFailureFree(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "myai")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dir, "downloaded")
	if err := os.WriteFile(source, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := replaceExecutable(source, target); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "new" {
		t.Errorf("target = %q, want the new binary", body)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Error("the replacement must be executable")
	}
	if _, err := os.Stat(target + ".old"); err == nil {
		t.Error("the superseded binary should be cleaned up")
	}
}

func TestIsNewerRefusesToDowngrade(t *testing.T) {
	tests := []struct {
		release, running string
		want             bool
		why              string
	}{
		{"v0.2.0", "v0.1.7", true, "a newer release should be taken"},
		{"v0.1.7", "v0.1.7", false, "the same version is not newer"},
		{"v0.1.6", "v0.1.7", false, "an older release must not be installed"},
		{"v0.1.7", "v0.1.7-3-g8d58e97-dirty", false, "a working-tree build must not be replaced by a release"},
		{"v0.1.7", "dev", false, "a dev build must not be replaced"},
		{"v1.0.0", "v0.9.9", true, "a major bump is newer"},
		{"v0.10.0", "v0.9.0", true, "versions compare numerically, not as text"},
	}
	for _, tt := range tests {
		if got := IsNewer(tt.release, tt.running); got != tt.want {
			t.Errorf("IsNewer(%q, %q) = %v: %s", tt.release, tt.running, got, tt.why)
		}
	}
}

func TestInstallCommandReportsWhetherTheBinaryChanged(t *testing.T) {
	// The services run this binary, so a caller has to know when it moved.
	a, _, env := newTestApp(t, "linux", "amd64")
	if err := a.env.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", "/bin/zsh")
	t.Setenv("PATH", env.Bin)

	changed, err := a.InstallCommand()
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Error("the first install puts a new binary in place")
	}

	changed, err = a.InstallCommand()
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("installing the same binary twice is not a change")
	}

	if err := os.WriteFile(a.exe, []byte("a different build"), 0o755); err != nil {
		t.Fatal(err)
	}
	changed, err = a.InstallCommand()
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Error("a different binary is a change")
	}
}
