package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Carlboms-Data-AB/myai/internal/config"
	"github.com/Carlboms-Data-AB/myai/internal/secrets"
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

	if err := a.InstallCommand(); err != nil {
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
		if err := a.InstallCommand(); err != nil {
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
