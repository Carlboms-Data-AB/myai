// Package app is MyAI's core. Every operation the product performs lives
// here, expressed as methods that take structured input and return structured
// results. It never writes to a terminal and never reads standard input:
// narration goes through a progress.Reporter and questions through a
// prompt.Asker. The terminal interface is one caller of this package, and a
// desktop frontend can be another without any of this changing.
package app

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"time"

	"github.com/Carlboms-Data-AB/myai/internal/backend"
	"github.com/Carlboms-Data-AB/myai/internal/backend/llamacpp"
	"github.com/Carlboms-Data-AB/myai/internal/backend/mlxserve"
	"github.com/Carlboms-Data-AB/myai/internal/catalog"
	"github.com/Carlboms-Data-AB/myai/internal/config"
	"github.com/Carlboms-Data-AB/myai/internal/inference"
	"github.com/Carlboms-Data-AB/myai/internal/opencode"
	"github.com/Carlboms-Data-AB/myai/internal/paths"
	"github.com/Carlboms-Data-AB/myai/internal/platform"
	"github.com/Carlboms-Data-AB/myai/internal/progress"
	"github.com/Carlboms-Data-AB/myai/internal/prompt"
	"github.com/Carlboms-Data-AB/myai/internal/run"
	"github.com/Carlboms-Data-AB/myai/internal/secrets"
	"github.com/Carlboms-Data-AB/myai/internal/service"
	"github.com/Carlboms-Data-AB/myai/internal/service/launchd"
	"github.com/Carlboms-Data-AB/myai/internal/service/nssm"
	"github.com/Carlboms-Data-AB/myai/internal/service/systemd"
)

// Options configure a new App.
type Options struct {
	// Env is the directory layout. The zero value resolves the running
	// machine's layout.
	Env *paths.Env
	// Host describes the platform. The zero value describes this machine.
	Host *platform.Host
	// Runner executes external commands. A nil runner runs them for real.
	Runner run.Runner
	// Reporter receives narration. A nil reporter discards it.
	Reporter progress.Reporter
	// Asker answers questions. A nil asker refuses them.
	Asker prompt.Asker
	// Executable is the path to the running myai binary. When empty it is
	// discovered from the process.
	Executable string
	// ReadyTimeout is how long to wait for the inference API after a
	// restart. Zero uses the default.
	ReadyTimeout time.Duration
}

// App is the MyAI core.
type App struct {
	env      paths.Env
	host     platform.Host
	runner   run.Runner
	reporter progress.Reporter
	asker    prompt.Asker
	exe      string

	cfg          config.Config
	services     service.Manager
	oc           *opencode.OpenCode
	readyTimeout time.Duration
}

// DefaultReadyTimeout is how long MyAI waits for the inference API to answer
// after starting it. Loading a multi-gigabyte model takes a while.
const DefaultReadyTimeout = 90 * time.Second

// New builds an App and loads the current configuration.
func New(opts Options) (*App, error) {
	env := paths.Env{}
	if opts.Env != nil {
		env = *opts.Env
	} else {
		resolved, err := paths.Current()
		if err != nil {
			return nil, err
		}
		env = resolved
	}

	host := platform.Current()
	if opts.Host != nil {
		host = *opts.Host
	}

	runner := opts.Runner
	if runner == nil {
		runner = run.Exec{}
	}

	exe := opts.Executable
	if exe == "" {
		if found, err := os.Executable(); err == nil {
			exe = found
		}
	}

	cfg, err := config.Load(env.ConfigFile())
	if err != nil {
		return nil, err
	}

	readyTimeout := opts.ReadyTimeout
	if readyTimeout <= 0 {
		readyTimeout = DefaultReadyTimeout
	}

	a := &App{
		env:          env,
		host:         host,
		runner:       runner,
		reporter:     progress.Or(opts.Reporter),
		asker:        prompt.Or(opts.Asker),
		exe:          exe,
		cfg:          cfg,
		readyTimeout: readyTimeout,
	}
	a.services = a.newServiceManager()
	a.oc = opencode.New(env.ToolsDir(), runner, host.OS, host.Arch)
	return a, nil
}

// Env returns the directory layout.
func (a *App) Env() paths.Env { return a.env }

// Host returns the platform description.
func (a *App) Host() platform.Host { return a.host }

// Config returns the current configuration.
func (a *App) Config() config.Config { return a.cfg }

// Reporter returns the progress reporter, for callers that run their own
// long operations against the same output.
func (a *App) Reporter() progress.Reporter { return a.reporter }

// OpenCode returns the OpenCode integration.
func (a *App) OpenCode() *opencode.OpenCode { return a.oc }

// Services returns the platform's service manager.
func (a *App) Services() service.Manager { return a.services }

// Executable returns the path to the installed or running myai binary.
func (a *App) Executable() string {
	if installed := a.env.Executable(); fileExists(installed) {
		return installed
	}
	return a.exe
}

// BackendID reports which backend the current configuration selects, resolving
// "auto" to the right one for this machine.
func (a *App) BackendID() string {
	if a.cfg.Backend == config.BackendAuto || a.cfg.Backend == "" {
		return catalog.DefaultBackend(a.host.OS, a.host.Arch)
	}
	return a.cfg.Backend
}

// Target describes what models have to be resolved for: this platform, and the
// backend that will actually load them.
func (a *App) Target() catalog.Target {
	return catalog.HostTarget(a.host.OS, a.host.Arch).WithBackend(a.BackendID())
}

// Backend returns the inference backend for the current configuration.
func (a *App) Backend() backend.Backend {
	id := a.BackendID()
	if id == config.BackendMLXServe {
		return mlxserve.New(a.env.MLXModelDir(), a.runner, a.host.OS, a.host.Arch)
	}
	return llamacpp.New(a.env.GGUFModelDir(), a.env.ToolsDir(), a.runner, a.host.OS, a.host.Arch)
}

// ActiveModel resolves the configured model for this platform.
func (a *App) ActiveModel() (catalog.Resolved, error) {
	return catalog.Resolve(a.cfg.ActiveModel, a.Target())
}

// Inference returns a client for the local inference API.
func (a *App) Inference() *inference.Client {
	return inference.New(a.Backend().BaseURL(a.cfg))
}

// ServiceName returns the platform service name for a role.
func (a *App) ServiceName(role string) string { return service.Name(a.host.OS, role) }

// Credentials returns the stored Web UI credentials, generating them the
// first time they are needed.
func (a *App) Credentials() (secrets.Credentials, error) {
	return secrets.Ensure(a.env.CredentialsFile(), a.cfg.Web.Username)
}

// SetConfig validates and persists a configuration.
func (a *App) SetConfig(cfg config.Config) error {
	cfg.Normalize()
	if err := cfg.Validate(); err != nil {
		return err
	}
	if err := a.env.EnsureDirs(); err != nil {
		return err
	}
	if err := config.Save(a.env.ConfigFile(), cfg); err != nil {
		return err
	}
	a.cfg = cfg
	return nil
}

// Update applies a change to the configuration and persists it, without
// touching services. Callers that need the change to take effect follow with
// Apply.
func (a *App) Update(mutate func(*config.Config)) error {
	cfg := a.cfg
	mutate(&cfg)
	return a.SetConfig(cfg)
}

// newServiceManager returns the manager for this operating system.
func (a *App) newServiceManager() service.Manager {
	switch a.host.OS {
	case "darwin":
		return launchd.New(filepath.Join(a.env.Home, "Library", "LaunchAgents"), os.Getuid(), a.runner)
	case "windows":
		return nssm.New("nssm", a.runner)
	default:
		return systemd.New(a.env.SystemdUserDir(), currentUsername(), a.runner)
	}
}

// serviceAccount returns the identity services should run as. Only Windows
// needs one, and MyAI asks for it rather than defaulting to LocalSystem.
func (a *App) serviceAccount() (service.Account, error) {
	if !a.services.NeedsAccount() {
		return service.Account{}, nil
	}

	name, err := platform.QualifiedUser()
	if err != nil {
		return service.Account{}, err
	}
	if !a.asker.Interactive() {
		return service.Account{}, fmt.Errorf("installing Windows services needs the password for %s; run MyAI from an elevated prompt", name)
	}
	password, err := a.asker.Secret(fmt.Sprintf("Windows password for %s (used only to register the service, never stored by MyAI)", name))
	if err != nil {
		return service.Account{}, err
	}
	if password == "" {
		return service.Account{}, fmt.Errorf("a password is required to run the services as %s", name)
	}
	return service.Account{User: name, Password: password}, nil
}

func currentUsername() string {
	if u, err := user.Current(); err == nil {
		return u.Username
	}
	return ""
}

func fileExists(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
