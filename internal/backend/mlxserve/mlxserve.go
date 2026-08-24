// Package mlxserve runs models on Apple Silicon through mlx-serve, which
// serves MLX checkpoints over an OpenAI-compatible API using Metal.
package mlxserve

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/Carlboms-Data-AB/myai/internal/backend"
	"github.com/Carlboms-Data-AB/myai/internal/catalog"
	"github.com/Carlboms-Data-AB/myai/internal/config"
	"github.com/Carlboms-Data-AB/myai/internal/models"
	"github.com/Carlboms-Data-AB/myai/internal/progress"
	"github.com/Carlboms-Data-AB/myai/internal/run"
	"github.com/Carlboms-Data-AB/myai/internal/service"
)

// Executable is the mlx-serve command name.
const Executable = "mlx-serve"

// Backend runs mlx-serve.
type Backend struct {
	// ModelDir is the shared mlx-serve model store.
	ModelDir string
	// Runner executes mlx-serve and Homebrew.
	Runner run.Runner
	// GOOS and GOARCH identify the platform.
	GOOS, GOARCH string
}

// New returns an mlx-serve backend.
func New(modelDir string, runner run.Runner, goos, goarch string) *Backend {
	return &Backend{ModelDir: modelDir, Runner: runner, GOOS: goos, GOARCH: goarch}
}

// ID returns the backend identifier.
func (b *Backend) ID() string { return config.BackendMLXServe }

// Name returns the display name.
func (b *Backend) Name() string { return "mlx-serve" }

// Detect reports whether mlx-serve is installed.
func (b *Backend) Detect(ctx context.Context) backend.Info {
	info := backend.Info{ID: b.ID(), Name: b.Name()}

	path, err := b.Runner.Look(Executable)
	if err != nil {
		return info
	}
	info.Installed = true
	info.Path = path

	if res, err := b.Runner.Run(ctx, run.Spec{Name: Executable, Args: []string{"--version"}}); err == nil {
		info.Version = versionLine(res.Output)
	}
	return info
}

// Install adds mlx-serve through Homebrew, which is how its author
// distributes it for macOS.
func (b *Backend) Install(ctx context.Context, _ config.Config, rep progress.Reporter) error {
	reporter := progress.Or(rep)

	if b.Detect(ctx).Installed {
		reporter.Info("mlx-serve is already installed")
		return nil
	}
	if !run.Available(b.Runner, "brew") {
		return fmt.Errorf("Homebrew is required to install mlx-serve: see https://brew.sh")
	}

	reporter.Step("Installing mlx-serve")
	if _, err := b.Runner.Run(ctx, run.Spec{
		Name:   "brew",
		Args:   []string{"tap", "ddalcu/mlx-serve", "https://github.com/ddalcu/mlx-serve"},
		OnLine: reporter.Info,
	}); err != nil {
		return fmt.Errorf("add the mlx-serve tap: %w", err)
	}

	// Newer Homebrew releases require third-party formulae to be trusted
	// before they will run. Older ones have no such subcommand, so a failure
	// here is not fatal.
	if run.Quiet(ctx, b.Runner, "brew", "help", "trust") {
		_, _ = b.Runner.Run(ctx, run.Spec{Name: "brew", Args: []string{"trust", "--formula", "ddalcu/mlx-serve/mlx-serve"}})
	}

	if _, err := b.Runner.Run(ctx, run.Spec{
		Name:   "brew",
		Args:   []string{"install", "ddalcu/mlx-serve/mlx-serve"},
		OnLine: reporter.Info,
	}); err != nil {
		return fmt.Errorf("install mlx-serve: %w", err)
	}
	return nil
}

// Uninstall removes the Homebrew formula MyAI installed. Models live outside
// the formula and are untouched.
func (b *Backend) Uninstall(ctx context.Context, rep progress.Reporter) error {
	reporter := progress.Or(rep)

	if !run.Available(b.Runner, "brew") {
		return fmt.Errorf("Homebrew is not available to remove mlx-serve")
	}
	reporter.Step("Removing mlx-serve")
	if _, err := b.Runner.Run(ctx, run.Spec{
		Name:   "brew",
		Args:   []string{"uninstall", "ddalcu/mlx-serve/mlx-serve"},
		OnLine: reporter.Info,
	}); err != nil {
		return fmt.Errorf("uninstall mlx-serve: %w", err)
	}
	return nil
}

// Store returns the shared mlx-serve model store.
func (b *Backend) Store() models.Store {
	return models.NewMLXStore(b.ModelDir, Executable, b.Runner, b.GOOS, b.GOARCH)
}

// BaseURL returns the inference API root.
func (b *Backend) BaseURL(cfg config.Config) string { return backend.BaseURL(cfg) }

// ModelName returns the identifier mlx-serve advertises, which is the
// repository id of the checkpoint.
func (b *Backend) ModelName(r catalog.Resolved) string { return r.Artifact.Repo }

// ServiceSpec describes the mlx-serve background service.
//
// Model lifecycle maps onto mlx-serve's own flags: it warms models eagerly at
// boot unless told otherwise, so keeping a model in RAM means leaving that
// alone and never passing an idle-eviction window.
func (b *Backend) ServiceSpec(ctx context.Context, p backend.SpecParams) (service.Spec, error) {
	info := b.Detect(ctx)
	if !info.Installed {
		return service.Spec{}, fmt.Errorf("mlx-serve is not installed")
	}

	args := []string{
		"--serve",
		"--model-dir", b.ModelDir,
		"--host", p.Config.Inference.Host,
		"--port", strconv.Itoa(p.Config.Inference.Port),
	}
	// mlx-serve writes to its own log directory rather than to stdout, so
	// point it at MyAI's. Without this the service log is empty and the
	// backend's own diagnostics are somewhere else entirely.
	if p.LogDir != "" {
		args = append(args, "--log-file", p.LogDir)
	}
	if p.Config.Runtime.SkipMemoryCheck {
		args = append(args, "--skip-mem-preflight")
	}
	if p.Config.Inference.KeepInRAM {
		// Nothing to add: mlx-serve warms eagerly and keeps models resident
		// by default. Capping the resident set here would only differ from
		// the defaults the prototype ran on, without keeping anything more.
	} else {
		args = append(args, "--no-warmup-eager")
		if seconds := p.Config.IdleUnloadSeconds(); seconds > 0 {
			args = append(args, "--idle-evict-secs", strconv.Itoa(seconds))
		}
	}

	return service.Spec{
		Role:        service.RoleInference,
		Name:        p.Name,
		DisplayName: service.DisplayName(service.RoleInference),
		Description: service.Description(service.RoleInference),
		Exec:        info.Path,
		Args:        args,
		Env:         map[string]string{"MYAI_ROLE": service.RoleInference},
		Dir:         p.WorkDir,
		StdoutLog:   p.StdoutLog,
		StderrLog:   p.StderrLog,
		Account:     p.Account,
	}, nil
}

// IdleUnload reports mlx-serve's native idle eviction.
func (b *Backend) IdleUnload(context.Context) backend.IdleUnload {
	return backend.IdleUnload{Supported: true, Mechanism: "--idle-evict-secs"}
}

// versionLine picks the version out of "mlx-serve --version" output. That
// output starts with bracketed memory diagnostics and then lists the versions
// of several components, so the first line is not the answer.
func versionLine(out string) string {
	var candidates []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "[") {
			continue
		}
		if strings.HasPrefix(line, Executable+" ") {
			return line
		}
		candidates = append(candidates, line)
	}
	if len(candidates) > 0 {
		return candidates[0]
	}
	return ""
}
