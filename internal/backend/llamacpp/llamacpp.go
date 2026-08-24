// Package llamacpp runs models on Windows and Linux through llama.cpp's
// llama-server, which serves GGUF files over an OpenAI-compatible API.
//
// MyAI installs the official prebuilt binaries from the llama.cpp releases.
// That works natively on Windows, including on ARM64, and never involves WSL.
package llamacpp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/Carlboms-Data-AB/myai/internal/archive"
	"github.com/Carlboms-Data-AB/myai/internal/backend"
	"github.com/Carlboms-Data-AB/myai/internal/catalog"
	"github.com/Carlboms-Data-AB/myai/internal/config"
	"github.com/Carlboms-Data-AB/myai/internal/fetch"
	"github.com/Carlboms-Data-AB/myai/internal/models"
	"github.com/Carlboms-Data-AB/myai/internal/progress"
	"github.com/Carlboms-Data-AB/myai/internal/run"
	"github.com/Carlboms-Data-AB/myai/internal/service"
)

// Executable is the llama.cpp server command name.
const Executable = "llama-server"

// releasesURL lists llama.cpp releases newest first.
const releasesURL = "https://api.github.com/repos/ggml-org/llama.cpp/releases?per_page=15"

// Backend runs llama-server.
type Backend struct {
	// ModelDir holds the GGUF files MyAI downloads.
	ModelDir string
	// ToolDir is where MyAI installs llama.cpp itself.
	ToolDir string
	// Runner executes llama-server.
	Runner run.Runner
	// HTTP fetches release metadata and archives.
	HTTP *http.Client
	// GOOS and GOARCH identify the platform.
	GOOS, GOARCH string
}

// New returns a llama.cpp backend.
func New(modelDir, toolDir string, runner run.Runner, goos, goarch string) *Backend {
	return &Backend{
		ModelDir: modelDir,
		ToolDir:  filepath.Join(toolDir, "llama.cpp"),
		Runner:   runner,
		HTTP:     http.DefaultClient,
		GOOS:     goos,
		GOARCH:   goarch,
	}
}

// ID returns the backend identifier.
func (b *Backend) ID() string { return config.BackendLlamaCPP }

// Name returns the display name.
func (b *Backend) Name() string { return "llama.cpp" }

// executableName is the platform-specific file name.
func (b *Backend) executableName() string {
	if b.GOOS == "windows" {
		return Executable + ".exe"
	}
	return Executable
}

// Detect finds llama-server, preferring a copy MyAI installed over one that
// happens to be on PATH, so upgrades are predictable.
func (b *Backend) Detect(ctx context.Context) backend.Info {
	info := backend.Info{ID: b.ID(), Name: b.Name()}

	if path, err := archive.FindExecutable(b.ToolDir, Executable); err == nil {
		info.Installed, info.Path = true, path
	} else if path, err := b.Runner.Look(Executable); err == nil {
		info.Installed, info.Path = true, path
	} else {
		return info
	}

	if res, err := b.Runner.Run(ctx, run.Spec{Name: info.Path, Args: []string{"--version"}}); err == nil {
		info.Version = versionLine(res.Output)
	}
	return info
}

// Install downloads and unpacks the official llama.cpp build for this machine.
// When an accelerated build will not run, MyAI falls back to the portable CPU
// build rather than leaving a backend that cannot start.
func (b *Backend) Install(ctx context.Context, cfg config.Config, rep progress.Reporter) error {
	reporter := progress.Or(rep)

	if info := b.Detect(ctx); info.Installed && info.Version != "" {
		reporter.Info("llama.cpp is already installed: " + info.Version)
		return nil
	}

	tag, names, err := b.latestRelease(ctx)
	if err != nil {
		return err
	}

	acceleration := cfg.Runtime.Acceleration
	for attempt := 0; attempt < 2; attempt++ {
		sel, err := SelectAsset(names, b.GOOS, b.GOARCH, acceleration)
		if err != nil {
			return err
		}

		reporter.Step(fmt.Sprintf("Installing llama.cpp %s (%s)", tag, sel.Acceleration))
		if err := b.installSelection(ctx, tag, sel, reporter); err != nil {
			return err
		}

		info := b.Detect(ctx)
		if info.Installed && info.Version != "" {
			return nil
		}
		if sel.Fallback == "" {
			return fmt.Errorf("installed llama.cpp %s but %s would not run", tag, b.executableName())
		}
		reporter.Warn(fmt.Sprintf("the %s build does not run on this machine, falling back to %s", sel.Acceleration, sel.Fallback))
		if err := os.RemoveAll(b.ToolDir); err != nil {
			return err
		}
		acceleration = sel.Fallback
	}
	return fmt.Errorf("could not install a working llama.cpp build")
}

func (b *Backend) installSelection(ctx context.Context, tag string, sel Selection, rep progress.Reporter) error {
	if err := os.MkdirAll(b.ToolDir, 0o755); err != nil {
		return err
	}
	for _, name := range append([]string{sel.Primary}, sel.Extra...) {
		archivePath := filepath.Join(b.ToolDir, name)
		url := fmt.Sprintf("https://github.com/ggml-org/llama.cpp/releases/download/%s/%s", tag, name)

		if err := fetch.Download(ctx, b.http(), url, archivePath, name, rep); err != nil {
			return err
		}
		if err := archive.Extract(archivePath, b.ToolDir); err != nil {
			return fmt.Errorf("unpack %s: %w", name, err)
		}
		os.Remove(archivePath)
	}
	return nil
}

type releaseInfo struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name string `json:"name"`
	} `json:"assets"`
}

var buildTag = regexp.MustCompile(`^b\d+$`)

// latestRelease returns the newest numbered llama.cpp build and its assets.
// The project also publishes other tags, which carry no binaries.
func (b *Backend) latestRelease(ctx context.Context) (string, []string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releasesURL, nil)
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := b.http().Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("list llama.cpp releases: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", nil, fmt.Errorf("list llama.cpp releases: %s", resp.Status)
	}

	var releases []releaseInfo
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return "", nil, err
	}
	for _, r := range releases {
		if !buildTag.MatchString(r.TagName) || len(r.Assets) == 0 {
			continue
		}
		names := make([]string, 0, len(r.Assets))
		for _, a := range r.Assets {
			names = append(names, a.Name)
		}
		return r.TagName, names, nil
	}
	return "", nil, fmt.Errorf("no llama.cpp build release found")
}

// Uninstall removes the llama.cpp build MyAI unpacked. Models are stored
// elsewhere and are untouched.
func (b *Backend) Uninstall(_ context.Context, rep progress.Reporter) error {
	progress.Or(rep).Step("Removing llama.cpp")
	return os.RemoveAll(b.ToolDir)
}

// Store returns the GGUF model store.
func (b *Backend) Store() models.Store {
	return models.NewGGUFStore(b.ModelDir, b.GOOS, b.GOARCH)
}

// BaseURL returns the inference API root.
func (b *Backend) BaseURL(cfg config.Config) string { return backend.BaseURL(cfg) }

// ModelName returns the identifier llama-server is told to advertise. MyAI
// sets it explicitly so the name does not change with the file on disk.
func (b *Backend) ModelName(r catalog.Resolved) string {
	if r.Artifact.Custom || r.Model.ID == "" {
		return r.Ref()
	}
	return r.Model.ID
}

// ServiceSpec describes the llama-server background service.
//
// llama.cpp loads the model when the server starts, so keeping it in RAM means
// locking it there and never letting the server sleep. Idle unloading uses
// llama-server's own --sleep-idle-seconds, which frees the memory while the
// endpoint stays up and reloads on the next request. MyAI does not stop the
// service on idle, because that would take the API away from OpenCode.
func (b *Backend) ServiceSpec(ctx context.Context, p backend.SpecParams) (service.Spec, error) {
	info := b.Detect(ctx)
	if !info.Installed {
		return service.Spec{}, fmt.Errorf("llama.cpp is not installed")
	}

	modelPath := b.Store().PathFor(p.Model)
	args := []string{
		"--model", modelPath,
		"--alias", b.ModelName(p.Model),
		"--host", p.Config.Inference.Host,
		"--port", strconv.Itoa(p.Config.Inference.Port),
		"--ctx-size", strconv.Itoa(p.Config.Inference.Context),
		"--n-predict", strconv.Itoa(p.Config.Inference.Output),
		"--jinja",
	}

	if p.Config.Inference.KeepInRAM {
		args = append(args, "--load-mode", "mlock")
	} else if seconds := p.Config.IdleUnloadSeconds(); seconds > 0 {
		if b.IdleUnload(ctx).Supported {
			args = append(args, "--sleep-idle-seconds", strconv.Itoa(seconds))
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

// IdleUnload reports whether this llama-server build can unload an idle model.
// The capability is read from the binary's own help output rather than
// assumed, because older builds do not have it.
func (b *Backend) IdleUnload(ctx context.Context) backend.IdleUnload {
	info := b.Detect(ctx)
	if !info.Installed {
		return backend.IdleUnload{Reason: "llama.cpp is not installed"}
	}

	res, err := b.Runner.Run(ctx, run.Spec{Name: info.Path, Args: []string{"--help"}})
	if err == nil && strings.Contains(res.Output, "--sleep-idle-seconds") {
		return backend.IdleUnload{Supported: true, Mechanism: "--sleep-idle-seconds"}
	}
	return backend.IdleUnload{
		Reason: "this llama.cpp build has no --sleep-idle-seconds option; update llama.cpp to unload idle models",
	}
}

func (b *Backend) http() *http.Client {
	if b.HTTP != nil {
		return b.HTTP
	}
	return http.DefaultClient
}

// versionLine extracts the build line from llama-server --version output.
func versionLine(out string) string {
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "version:") || strings.Contains(line, "build:") {
			return line
		}
	}
	return strings.TrimSpace(firstLine(out))
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}
