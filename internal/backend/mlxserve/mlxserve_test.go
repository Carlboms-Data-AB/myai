package mlxserve

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Carlboms-Data-AB/myai/internal/backend"
	"github.com/Carlboms-Data-AB/myai/internal/catalog"
	"github.com/Carlboms-Data-AB/myai/internal/config"
	"github.com/Carlboms-Data-AB/myai/internal/run"
	"github.com/Carlboms-Data-AB/myai/internal/service"
)

func params(t *testing.T, mutate func(*config.Config)) backend.SpecParams {
	t.Helper()
	cfg := config.Default()
	if mutate != nil {
		mutate(&cfg)
		cfg.Normalize()
	}
	model, err := catalog.Resolve(cfg.ActiveModel, "darwin", "arm64")
	if err != nil {
		t.Fatal(err)
	}
	return backend.SpecParams{
		Config:    cfg,
		Model:     model,
		Name:      service.Name("darwin", service.RoleInference),
		StdoutLog: "/tmp/logs/inference.log",
		StderrLog: "/tmp/logs/inference-error.log",
	}
}

func argsFor(t *testing.T, mutate func(*config.Config)) []string {
	t.Helper()
	b := New("/Users/t/.mlx-serve/models", run.NewFake(), "darwin", "arm64")
	spec, err := b.ServiceSpec(context.Background(), params(t, mutate))
	if err != nil {
		t.Fatal(err)
	}
	return spec.Args
}

func joined(args []string) string { return strings.Join(args, " ") }

func TestServiceSpecMatchesThePrototypeInvocation(t *testing.T) {
	got := joined(argsFor(t, nil))
	for _, want := range []string{
		"--serve",
		"--model-dir /Users/t/.mlx-serve/models",
		"--host 127.0.0.1",
		"--port 11234",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("args %q missing %q", got, want)
		}
	}
}

func TestKeepInRAMLeavesEagerWarmupAlone(t *testing.T) {
	// mlx-serve warms models at boot by default, so keeping a model resident
	// means not disabling that and never evicting.
	got := joined(argsFor(t, func(c *config.Config) { c.Inference.KeepInRAM = true }))
	if strings.Contains(got, "--no-warmup-eager") {
		t.Errorf("eager warmup must stay on when keeping the model in RAM: %q", got)
	}
	if strings.Contains(got, "--idle-evict-secs") {
		t.Errorf("a resident model must not be evicted: %q", got)
	}
	if !strings.Contains(got, "--max-resident-models 1") {
		t.Errorf("the model must be allowed to stay loaded: %q", got)
	}
}

func TestIdleUnloadUsesNativeEviction(t *testing.T) {
	got := joined(argsFor(t, func(c *config.Config) {
		c.Inference.KeepInRAM = false
		c.Inference.IdleUnloadMinutes = 15
	}))
	if !strings.Contains(got, "--idle-evict-secs 900") {
		t.Errorf("args %q should evict after 900 seconds", got)
	}
	if !strings.Contains(got, "--no-warmup-eager") {
		t.Errorf("args %q should not warm eagerly when the model need not stay resident", got)
	}
}

func TestNoIdleUnloadWhenDisabled(t *testing.T) {
	got := joined(argsFor(t, func(c *config.Config) {
		c.Inference.KeepInRAM = false
		c.Inference.IdleUnloadMinutes = 0
	}))
	if strings.Contains(got, "--idle-evict-secs") {
		t.Errorf("idle eviction is off, so the flag must be absent: %q", got)
	}
}

func TestCustomPortAndHostAreHonoured(t *testing.T) {
	got := joined(argsFor(t, func(c *config.Config) {
		c.Inference.Port = 12000
		c.Inference.Host = "127.0.0.1"
	}))
	if !strings.Contains(got, "--port 12000") {
		t.Errorf("args = %q", got)
	}
}

func TestServiceSpecFailsWhenBackendMissing(t *testing.T) {
	fake := run.NewFake().Absent(Executable)
	b := New("/models", fake, "darwin", "arm64")
	if _, err := b.ServiceSpec(context.Background(), params(t, nil)); err == nil {
		t.Error("expected an error when mlx-serve is not installed")
	}
}

func TestDetectReportsVersion(t *testing.T) {
	fake := run.NewFake().Respond("mlx-serve --version", "mlx-serve 0.9.1\n")
	got := New("/models", fake, "darwin", "arm64").Detect(context.Background())
	if !got.Installed {
		t.Fatal("mlx-serve should be detected")
	}
	if got.Version != "mlx-serve 0.9.1" {
		t.Errorf("Version = %q", got.Version)
	}
	if got.Summary() != "mlx-serve 0.9.1" {
		t.Errorf("Summary = %q", got.Summary())
	}
}

func TestDetectReportsMissingBackend(t *testing.T) {
	fake := run.NewFake().Absent(Executable)
	got := New("/models", fake, "darwin", "arm64").Detect(context.Background())
	if got.Installed {
		t.Error("mlx-serve should not be detected")
	}
	if got.Summary() != "not installed" {
		t.Errorf("Summary = %q", got.Summary())
	}
}

func TestInstallUsesHomebrewTap(t *testing.T) {
	fake := run.NewFake().Absent(Executable)
	err := New("/models", fake, "darwin", "arm64").Install(context.Background(), config.Default(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !fake.Ran("brew tap ddalcu/mlx-serve") {
		t.Errorf("tap not added: %v", fake.CommandLines())
	}
	if !fake.Ran("brew install ddalcu/mlx-serve/mlx-serve") {
		t.Errorf("formula not installed: %v", fake.CommandLines())
	}
}

func TestInstallSkipsWhenPresent(t *testing.T) {
	fake := run.NewFake()
	if err := New("/models", fake, "darwin", "arm64").Install(context.Background(), config.Default(), nil); err != nil {
		t.Fatal(err)
	}
	if fake.Ran("brew install") {
		t.Error("an installed backend must not be reinstalled")
	}
}

func TestInstallNeedsHomebrew(t *testing.T) {
	fake := run.NewFake().Absent(Executable).Absent("brew")
	fake.Fail("brew", errors.New("not found"))

	err := New("/models", fake, "darwin", "arm64").Install(context.Background(), config.Default(), nil)
	if err == nil || !strings.Contains(err.Error(), "Homebrew") {
		t.Errorf("err = %v, want a clear Homebrew message", err)
	}
}

func TestModelNameIsTheRepositoryID(t *testing.T) {
	model, err := catalog.Resolve("qwen3.5-9b", "darwin", "arm64")
	if err != nil {
		t.Fatal(err)
	}
	b := New("/models", run.NewFake(), "darwin", "arm64")
	if got := b.ModelName(model); got != "mlx-community/Qwen3.5-9B-6bit" {
		t.Errorf("ModelName = %q", got)
	}
}

func TestIdleUnloadIsNative(t *testing.T) {
	got := New("/models", run.NewFake(), "darwin", "arm64").IdleUnload(context.Background())
	if !got.Supported || got.Mechanism != "--idle-evict-secs" {
		t.Errorf("IdleUnload = %+v", got)
	}
}

func TestBaseURLKeepsLoopback(t *testing.T) {
	cfg := config.Default()
	b := New("/models", run.NewFake(), "darwin", "arm64")
	if got := b.BaseURL(cfg); got != "http://127.0.0.1:11234" {
		t.Errorf("BaseURL = %q", got)
	}
}
