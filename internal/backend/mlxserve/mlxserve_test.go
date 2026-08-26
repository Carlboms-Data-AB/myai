package mlxserve

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/carlbomsdata/myai/internal/backend"
	"github.com/carlbomsdata/myai/internal/catalog"
	"github.com/carlbomsdata/myai/internal/config"
	"github.com/carlbomsdata/myai/internal/run"
	"github.com/carlbomsdata/myai/internal/service"
)

func params(t *testing.T, mutate func(*config.Config)) backend.SpecParams {
	t.Helper()
	cfg := config.Default()
	if mutate != nil {
		mutate(&cfg)
		cfg.Normalize()
	}
	model, err := catalog.Resolve(cfg.ActiveModel, catalog.HostTarget("darwin", "arm64"))
	if err != nil {
		t.Fatal(err)
	}
	return backend.SpecParams{
		Config:    cfg,
		Model:     model,
		Name:      service.Name("darwin", service.RoleInference),
		StdoutLog: "/tmp/logs/inference.log",
		StderrLog: "/tmp/logs/inference-error.log",
		LogDir:    "/tmp/logs",
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
	// The prototype ran the defaults and kept its model resident, so MyAI
	// adds nothing here.
	if strings.Contains(got, "--max-resident") {
		t.Errorf("keeping the model in RAM should not change the resident-set defaults: %q", got)
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
	model, err := catalog.Resolve("qwen3.5-9b", catalog.HostTarget("darwin", "arm64"))
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

// realVersionOutput is what mlx-serve 26.8.9 actually prints. The memory
// diagnostic comes first, so taking the first line reports nonsense.
const realVersionOutput = `[mem] MLX buffer-pool cap 2048 MB (was 23347 MB)
mlx-serve 26.8.9
mlx 0.32.0
mlx-c fba4470b8907
nax off (requires M5-class GPU)
ggml 0.16.0 (505b1ed15)
llama.cpp b10034
gguf 3
ds4 unknown
`

func TestVersionSkipsTheMemoryDiagnostic(t *testing.T) {
	fake := run.NewFake().Respond("mlx-serve --version", realVersionOutput)

	got := New("/models", fake, "darwin", "arm64").Detect(context.Background())
	if got.Version != "mlx-serve 26.8.9" {
		t.Errorf("Version = %q, want %q", got.Version, "mlx-serve 26.8.9")
	}
	if strings.Contains(got.Version, "[mem]") {
		t.Error("a diagnostic line was reported as the version")
	}
}

func TestVersionLine(t *testing.T) {
	tests := map[string]string{
		realVersionOutput:                          "mlx-serve 26.8.9",
		"mlx-serve 1.0.0\n":                        "mlx-serve 1.0.0",
		"[mem] noise\n[mem] more\nsomething 2.0\n": "something 2.0",
		"[mem] only noise\n":                       "",
		"":                                         "",
	}
	for in, want := range tests {
		if got := versionLine(in); got != want {
			t.Errorf("versionLine(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestKeepInRAMRunsTheSameCommandThePrototypeDid(t *testing.T) {
	// The Bash prototype ran this and kept a 9B model resident on a 24 GB
	// Mac. MyAI must not quietly differ from it.
	got := argsFor(t, func(c *config.Config) { c.Inference.KeepInRAM = true })

	want := []string{
		"--serve",
		"--model-dir", "/Users/t/.mlx-serve/models",
		"--host", "127.0.0.1",
		"--port", "11234",
		"--log-file", "/tmp/logs",
		"--skip-mem-preflight",
	}
	if len(got) != len(want) {
		t.Fatalf("args = %v, want exactly %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("arg %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestMemoryCheckIsSkippedByDefaultAndCanBeRestored(t *testing.T) {
	on := joined(argsFor(t, nil))
	if !strings.Contains(on, "--skip-mem-preflight") {
		t.Errorf("args %q should skip the pre-flight by default", on)
	}

	off := joined(argsFor(t, func(c *config.Config) { c.Runtime.SkipMemoryCheck = false }))
	if strings.Contains(off, "--skip-mem-preflight") {
		t.Errorf("turning the override off must restore the check: %q", off)
	}
}
