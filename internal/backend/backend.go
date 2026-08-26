// Package backend defines the inference backends MyAI can run. A backend
// knows how to install itself, where its models live, how to describe itself
// as a background service and what it can do about keeping a model in memory.
// Nothing above this package needs to know whether that is MLX or llama.cpp.
package backend

import (
	"context"
	"strconv"

	"github.com/carlbomsdata/myai/internal/catalog"
	"github.com/carlbomsdata/myai/internal/config"
	"github.com/carlbomsdata/myai/internal/models"
	"github.com/carlbomsdata/myai/internal/progress"
	"github.com/carlbomsdata/myai/internal/service"
)

// Info describes an installed backend.
type Info struct {
	// ID is the backend identifier.
	ID string
	// Name is the display name.
	Name string
	// Installed reports whether the backend is present on this machine.
	Installed bool
	// Path is the executable location when installed.
	Path string
	// Version is what the backend reports about itself.
	Version string
}

// Summary renders the backend for status output.
func (i Info) Summary() string {
	if !i.Installed {
		return "not installed"
	}
	if i.Version != "" {
		return i.Version
	}
	return i.Path
}

// IdleUnload describes what a backend can do about unloading an idle model.
// When a backend cannot do it, MyAI says so rather than pretending.
type IdleUnload struct {
	// Supported reports whether the backend can unload an idle model.
	Supported bool
	// Mechanism names the flag or behaviour used, for status output.
	Mechanism string
	// Reason explains the limitation when Supported is false.
	Reason string
}

// SpecParams is everything a backend needs to describe itself as a service.
type SpecParams struct {
	// Config is the current configuration.
	Config config.Config
	// Model is the resolved active model.
	Model catalog.Resolved
	// Name is the platform-specific service name.
	Name string
	// StdoutLog and StderrLog are where the service writes output.
	StdoutLog string
	StderrLog string
	// LogDir is where a backend that keeps its own log should write it.
	LogDir string
	// WorkDir is the service working directory.
	WorkDir string
	// Account is the identity to run as, where the platform needs one.
	Account service.Account
}

// Backend is one way of running a local model.
type Backend interface {
	// ID is the backend identifier, matching the config values.
	ID() string
	// Name is the display name.
	Name() string
	// Detect reports whether the backend is installed and which version.
	Detect(ctx context.Context) Info
	// Install puts the backend on the machine, doing nothing if it is
	// already present and usable.
	Install(ctx context.Context, cfg config.Config, rep progress.Reporter) error
	// Uninstall removes the backend. It is only called for a backend MyAI
	// installed itself, so it need not worry about taking away something the
	// operator put there.
	Uninstall(ctx context.Context, rep progress.Reporter) error
	// Store manages this backend's model artifacts.
	Store() models.Store
	// BaseURL is the inference API root.
	BaseURL(cfg config.Config) string
	// ModelName is the identifier the server advertises for a model, which is
	// also what the OpenCode configuration must name.
	ModelName(r catalog.Resolved) string
	// ServiceSpec describes the background service that runs this backend.
	ServiceSpec(ctx context.Context, p SpecParams) (service.Spec, error)
	// IdleUnload reports what this backend can do about idle models.
	IdleUnload(ctx context.Context) IdleUnload
}

// BaseURL builds the inference API root from a configuration. Both backends
// serve the same OpenAI-compatible API, so the shape is identical.
func BaseURL(cfg config.Config) string {
	host := cfg.Inference.Host
	if host == "0.0.0.0" || host == "::" {
		// A wildcard bind is reachable on loopback, which is how MyAI itself
		// talks to the server.
		host = "127.0.0.1"
	}
	return "http://" + host + ":" + strconv.Itoa(cfg.Inference.Port)
}
