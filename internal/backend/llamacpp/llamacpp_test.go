package llamacpp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Carlboms-Data-AB/myai/internal/backend"
	"github.com/Carlboms-Data-AB/myai/internal/catalog"
	"github.com/Carlboms-Data-AB/myai/internal/config"
	"github.com/Carlboms-Data-AB/myai/internal/run"
	"github.com/Carlboms-Data-AB/myai/internal/service"
)

// helpWithSleep is the part of llama-server --help that matters here.
const helpWithSleep = `  --sleep-idle-seconds SECONDS   number of seconds of idleness after which the server will sleep`

func newBackend(t *testing.T, fake *run.Fake) (*Backend, string) {
	t.Helper()
	dir := t.TempDir()
	b := New(filepath.Join(dir, "models"), filepath.Join(dir, "tools"), fake, "linux", "amd64")
	return b, dir
}

func specArgs(t *testing.T, fake *run.Fake, mutate func(*config.Config)) []string {
	t.Helper()
	b, _ := newBackend(t, fake)

	cfg := config.Default()
	if mutate != nil {
		mutate(&cfg)
	}
	cfg.Normalize()

	model, err := catalog.Resolve(cfg.ActiveModel, catalog.HostTarget("linux", "amd64"))
	if err != nil {
		t.Fatal(err)
	}
	spec, err := b.ServiceSpec(context.Background(), backend.SpecParams{
		Config:    cfg,
		Model:     model,
		Name:      service.Name("linux", service.RoleInference),
		StdoutLog: filepath.Join(t.TempDir(), "inference.log"),
	})
	if err != nil {
		t.Fatal(err)
	}
	return spec.Args
}

func joined(args []string) string { return strings.Join(args, " ") }

func TestServiceSpecCarriesModelAndLimits(t *testing.T) {
	fake := run.NewFake().Respond("--help", helpWithSleep)
	got := joined(specArgs(t, fake, nil))

	for _, want := range []string{
		"--alias qwen3.5-9b",
		"--host 127.0.0.1",
		"--port 11234",
		"--ctx-size 131072",
		"--n-predict 16384",
		"--jinja",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("args %q missing %q", got, want)
		}
	}
	if !strings.Contains(got, "Qwen3.5-9B-Q6_K.gguf") {
		t.Errorf("args %q should point at the resolved GGUF file", got)
	}
}

func TestKeepInRAMLocksTheModel(t *testing.T) {
	fake := run.NewFake().Respond("--help", helpWithSleep)
	got := joined(specArgs(t, fake, func(c *config.Config) { c.Inference.KeepInRAM = true }))

	if !strings.Contains(got, "--load-mode mlock") {
		t.Errorf("args %q should lock the model in memory", got)
	}
	if strings.Contains(got, "--sleep-idle-seconds") {
		t.Errorf("a resident model must never be put to sleep: %q", got)
	}
}

func TestIdleUnloadUsesSleepIdleSeconds(t *testing.T) {
	fake := run.NewFake().Respond("--help", helpWithSleep)
	got := joined(specArgs(t, fake, func(c *config.Config) {
		c.Inference.KeepInRAM = false
		c.Inference.IdleUnloadMinutes = 20
	}))

	if !strings.Contains(got, "--sleep-idle-seconds 1200") {
		t.Errorf("args %q should sleep after 1200 idle seconds", got)
	}
	if strings.Contains(got, "--load-mode mlock") {
		t.Errorf("a model that may be unloaded must not be locked: %q", got)
	}
}

func TestIdleUnloadOmittedWhenBuildLacksTheOption(t *testing.T) {
	// An older llama.cpp has no such flag. Passing it anyway would stop the
	// server from starting at all.
	fake := run.NewFake().Respond("--help", "  --no-warmup   skip warming up the model")
	got := joined(specArgs(t, fake, func(c *config.Config) {
		c.Inference.KeepInRAM = false
		c.Inference.IdleUnloadMinutes = 20
	}))

	if strings.Contains(got, "--sleep-idle-seconds") {
		t.Errorf("must not pass an option this build does not have: %q", got)
	}
}

func TestIdleUnloadReportsWhyItIsUnavailable(t *testing.T) {
	fake := run.NewFake().Respond("--help", "  --no-warmup")
	b, _ := newBackend(t, fake)

	got := b.IdleUnload(context.Background())
	if got.Supported {
		t.Fatal("this build cannot unload idle models")
	}
	if !strings.Contains(got.Reason, "--sleep-idle-seconds") {
		t.Errorf("Reason = %q, want an explanation naming the missing option", got.Reason)
	}
}

func TestIdleUnloadDetectedFromHelp(t *testing.T) {
	fake := run.NewFake().Respond("--help", helpWithSleep)
	b, _ := newBackend(t, fake)

	got := b.IdleUnload(context.Background())
	if !got.Supported || got.Mechanism != "--sleep-idle-seconds" {
		t.Errorf("IdleUnload = %+v", got)
	}
}

func TestIdleUnloadWithoutBackendInstalled(t *testing.T) {
	fake := run.NewFake().Absent(Executable)
	b, _ := newBackend(t, fake)

	got := b.IdleUnload(context.Background())
	if got.Supported {
		t.Error("a missing backend cannot support anything")
	}
	if !strings.Contains(got.Reason, "not installed") {
		t.Errorf("Reason = %q", got.Reason)
	}
}

func TestDetectPrefersTheCopyMyAIInstalled(t *testing.T) {
	fake := run.NewFake().Respond("--version", "version: 10589 (abc1234)\n")
	b, dir := newBackend(t, fake)

	installed := filepath.Join(dir, "tools", "llama.cpp", "build", "bin", "llama-server")
	if err := os.MkdirAll(filepath.Dir(installed), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installed, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	got := b.Detect(context.Background())
	if !got.Installed {
		t.Fatal("llama-server should be detected")
	}
	if got.Path != installed {
		t.Errorf("Path = %q, want the MyAI-installed copy at %q", got.Path, installed)
	}
	if got.Version != "version: 10589 (abc1234)" {
		t.Errorf("Version = %q", got.Version)
	}
}

func TestDetectFallsBackToPath(t *testing.T) {
	fake := run.NewFake().Respond("--version", "version: 1 (x)\n")
	b, _ := newBackend(t, fake)

	got := b.Detect(context.Background())
	if !got.Installed {
		t.Fatal("a llama-server on PATH should be detected")
	}
	if !strings.HasSuffix(got.Path, Executable) {
		t.Errorf("Path = %q", got.Path)
	}
}

func TestDetectReportsMissing(t *testing.T) {
	fake := run.NewFake().Absent(Executable)
	b, _ := newBackend(t, fake)

	if b.Detect(context.Background()).Installed {
		t.Error("llama-server should not be detected")
	}
}

func TestServiceSpecFailsWithoutBackend(t *testing.T) {
	fake := run.NewFake().Absent(Executable)
	b, _ := newBackend(t, fake)

	model, err := catalog.Resolve("qwen3.5-9b", catalog.HostTarget("linux", "amd64"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = b.ServiceSpec(context.Background(), backend.SpecParams{Config: config.Default(), Model: model, Name: "myai"})
	if err == nil {
		t.Error("expected an error when llama.cpp is not installed")
	}
}

func TestModelNameUsesTheLogicalID(t *testing.T) {
	b, _ := newBackend(t, run.NewFake())

	model, err := catalog.Resolve("qwen3.5-9b", catalog.HostTarget("linux", "amd64"))
	if err != nil {
		t.Fatal(err)
	}
	if got := b.ModelName(model); got != "qwen3.5-9b" {
		t.Errorf("ModelName = %q", got)
	}

	custom, err := catalog.Resolve("someone/Some-GGUF/file.gguf", catalog.HostTarget("linux", "amd64"))
	if err != nil {
		t.Fatal(err)
	}
	if got := b.ModelName(custom); got != "someone/Some-GGUF/file.gguf" {
		t.Errorf("custom ModelName = %q", got)
	}
}

func TestExecutableNameIsPlatformCorrect(t *testing.T) {
	win := New("/m", "/t", run.NewFake(), "windows", "amd64")
	if win.executableName() != "llama-server.exe" {
		t.Errorf("windows executable = %q", win.executableName())
	}
	lin := New("/m", "/t", run.NewFake(), "linux", "amd64")
	if lin.executableName() != "llama-server" {
		t.Errorf("linux executable = %q", lin.executableName())
	}
}

func TestVersionLine(t *testing.T) {
	tests := map[string]string{
		"version: 10589 (abc1234)\nbuilt with gcc\n":                 "version: 10589 (abc1234)",
		"load_backend: loaded CPU backend\nversion: 900 (deadbee)\n": "version: 900 (deadbee)",
		"something else\n": "something else",
	}
	for in, want := range tests {
		if got := versionLine(in); got != want {
			t.Errorf("versionLine(%q) = %q, want %q", in, got, want)
		}
	}
}
