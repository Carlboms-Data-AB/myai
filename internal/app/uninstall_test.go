package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// installFakeModel stands in for a real multi-gigabyte download.
func installFakeModel(t *testing.T, a *App) string {
	t.Helper()
	dir := filepath.Join(a.env.MLXModelDir(), "mlx-community", "Qwen3.5-9B-6bit")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "model.safetensors")
	if err := os.WriteFile(path, make([]byte, 4096), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestUninstallKeepsModelsByDefault(t *testing.T) {
	a, _, env := newTestApp(t, "darwin", "arm64")
	if err := a.env.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	if err := a.SetConfig(a.Config()); err != nil {
		t.Fatal(err)
	}
	model := installFakeModel(t, a)

	if err := a.Uninstall(context.Background(), UninstallKeepModels); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}

	if _, err := os.Stat(model); err != nil {
		t.Fatalf("the default uninstall deleted a downloaded model: %v", err)
	}
	if fileExists(env.ConfigFile()) {
		t.Error("the configuration should have been removed")
	}
	if _, err := os.Stat(env.State); !os.IsNotExist(err) {
		t.Error("state should have been removed")
	}
}

func TestUninstallWithModelsRemovesThem(t *testing.T) {
	a, _, _ := newTestApp(t, "darwin", "arm64")
	if err := a.env.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	model := installFakeModel(t, a)

	if err := a.Uninstall(context.Background(), UninstallWithModels); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(model); !os.IsNotExist(err) {
		t.Error("the model should have been removed when explicitly requested")
	}
}

func TestUninstallModelsOnlyLeavesMyAIInstalled(t *testing.T) {
	a, _, env := newTestApp(t, "darwin", "arm64")
	if err := a.env.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	if err := a.SetConfig(a.Config()); err != nil {
		t.Fatal(err)
	}
	model := installFakeModel(t, a)

	if err := a.Uninstall(context.Background(), UninstallModelsOnly); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(model); !os.IsNotExist(err) {
		t.Error("the model should have been removed")
	}
	if !fileExists(env.ConfigFile()) {
		t.Error("MyAI itself should still be installed")
	}
}

func TestUninstallRemovesBothServices(t *testing.T) {
	a, fake, _ := newTestApp(t, "darwin", "arm64")
	if err := a.env.EnsureDirs(); err != nil {
		t.Fatal(err)
	}

	if err := a.Uninstall(context.Background(), UninstallKeepModels); err != nil {
		t.Fatal(err)
	}
	lines := strings.Join(fake.CommandLines(), "\n")
	for _, name := range []string{"se.carlbomsdata.myai", "se.carlbomsdata.myai-opencode"} {
		if !strings.Contains(lines, name) {
			t.Errorf("service %q was not removed: %v", name, fake.CommandLines())
		}
	}
}

func TestPlanUninstallSaysWhatSurvives(t *testing.T) {
	a, _, _ := newTestApp(t, "darwin", "arm64")
	if err := a.env.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	installFakeModel(t, a)

	keep, err := a.PlanUninstall(context.Background(), UninstallKeepModels)
	if err != nil {
		t.Fatal(err)
	}
	if keep.ModelBytes != 4096 {
		t.Errorf("ModelBytes = %d, want 4096", keep.ModelBytes)
	}
	if !strings.Contains(strings.Join(keep.Keeps, " "), "downloaded models") {
		t.Errorf("the plan should say models are kept: %v", keep.Keeps)
	}
	for _, r := range keep.Removes {
		if strings.Contains(r, a.env.MLXModelDir()) {
			t.Errorf("the default plan must not list the model directory for removal: %v", keep.Removes)
		}
	}

	full, err := a.PlanUninstall(context.Background(), UninstallWithModels)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(full.Removes, " "), "downloaded models") {
		t.Errorf("the full plan should list the models: %v", full.Removes)
	}
}

func TestUninstallRemovesThePathBlockOnly(t *testing.T) {
	a, _, env := newTestApp(t, "linux", "amd64")
	if err := a.env.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", "/bin/zsh")
	t.Setenv("PATH", "/usr/bin")

	profile := filepath.Join(env.Home, ".zshrc")
	if err := os.WriteFile(profile, []byte("export EDITOR=vim\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := a.InstallCommand(); err != nil {
		t.Fatal(err)
	}
	if err := a.Uninstall(context.Background(), UninstallKeepModels); err != nil {
		t.Fatal(err)
	}

	body, err := os.ReadFile(profile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), env.Bin) {
		t.Errorf("PATH block not removed:\n%s", body)
	}
	if !strings.Contains(string(body), "export EDITOR=vim") {
		t.Errorf("unrelated profile lines were lost:\n%s", body)
	}
}
