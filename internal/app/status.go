package app

import (
	"context"
	"strconv"
	"time"

	"github.com/Carlboms-Data-AB/myai/internal/backend"
	"github.com/Carlboms-Data-AB/myai/internal/opencode"
	"github.com/Carlboms-Data-AB/myai/internal/platform"
	"github.com/Carlboms-Data-AB/myai/internal/run"
	"github.com/Carlboms-Data-AB/myai/internal/secrets"
	"github.com/Carlboms-Data-AB/myai/internal/service"
)

// Status is everything MyAI can report about the current machine. It is data
// only, so any interface can render it.
type Status struct {
	// Version is the MyAI build.
	Version string
	// Platform is the operating system and architecture.
	Platform string
	// Installed reports whether MyAI has been set up on this machine.
	Installed bool

	// Backend describes the inference backend.
	Backend backend.Info
	// OpenCode describes the agent frontend.
	OpenCode opencode.Info

	// Model is the active model's display name.
	Model string
	// ModelRef is the artifact reference on disk.
	ModelRef string
	// ModelInstalled reports whether the artifact is downloaded.
	ModelInstalled bool
	// ModelLoaded reports whether it currently occupies memory.
	ModelLoaded bool
	// KeepInRAM is the configured residency setting.
	KeepInRAM bool
	// IdleUnload describes the configured idle behaviour.
	IdleUnload string

	// API is the inference endpoint.
	API string
	// APIReachable reports whether it answers.
	APIReachable bool

	// InferenceService and WebService are the background service states.
	InferenceService service.State
	WebService       service.State

	// WebEnabled reports whether the Web UI is configured to run.
	WebEnabled bool
	// WebURL is the address to use from another machine.
	WebURL string
	// WebReachable reports whether the Web UI answers locally.
	WebReachable bool
	// WebExposed reports whether the Web UI is bound beyond loopback.
	WebExposed bool

	// WebSearch and BrowserAutomation are the optional tool settings.
	WebSearch         bool
	BrowserAutomation bool
	// BrowserReady reports whether the browser skill is actually available.
	BrowserReady bool
}

// Version is the MyAI build identifier. It is set at build time.
var Version = "dev"

// Status gathers the current state of the machine.
func (a *App) Status(ctx context.Context) Status {
	b := a.Backend()

	s := Status{
		Version:           Version,
		Platform:          a.host.Label(),
		Backend:           b.Detect(ctx),
		OpenCode:          a.oc.Detect(ctx),
		KeepInRAM:         a.cfg.Inference.KeepInRAM,
		API:               b.BaseURL(a.cfg),
		WebEnabled:        a.cfg.Web.Enabled,
		WebExposed:        a.cfg.WebExposedBeyondLoopback(),
		WebSearch:         a.cfg.Tools.WebSearch,
		BrowserAutomation: a.cfg.Tools.BrowserAutomation,
	}
	s.Installed = fileExists(a.env.ConfigFile())
	s.IdleUnload = a.idleUnloadSummary(ctx, b)

	if model, err := a.ActiveModel(); err == nil {
		s.Model = model.Label()
		s.ModelRef = model.Ref()
		if have, err := b.Store().Has(ctx, model); err == nil {
			s.ModelInstalled = have
		}
	} else {
		s.Model = a.cfg.ActiveModel + " (unavailable: " + err.Error() + ")"
	}

	client := a.Inference()
	s.APIReachable = client.Ready(ctx, 2*time.Second)
	if s.APIReachable && s.ModelInstalled {
		if model, err := a.ActiveModel(); err == nil {
			// A resident model answers a one-token request immediately; a model
			// that has to be read back from disk does not.
			s.ModelLoaded = client.Loaded(ctx, b.ModelName(model), 3*time.Second)
		}
	}

	s.InferenceService, _ = a.services.Status(ctx, a.ServiceName(service.RoleInference))
	if a.cfg.Web.Enabled {
		s.WebService, _ = a.services.Status(ctx, a.ServiceName(service.RoleWeb))
		s.WebURL = opencode.WebURL(platform.ReachableAddress(), a.cfg)
		s.WebReachable = a.WebReachable(ctx)
	}
	s.BrowserReady = a.browserReady()
	return s
}

// idleUnloadSummary describes what will happen to an idle model, using the
// backend's real capability rather than the configured wish.
func (a *App) idleUnloadSummary(ctx context.Context, b backend.Backend) string {
	if a.cfg.Inference.KeepInRAM {
		return "off, model kept in RAM"
	}
	minutes := a.cfg.Inference.IdleUnloadMinutes
	if minutes == 0 {
		return "off"
	}

	support := b.IdleUnload(ctx)
	if !support.Supported {
		return "requested " + strconv.Itoa(minutes) + " min, unavailable: " + support.Reason
	}
	return strconv.Itoa(minutes) + " min (" + support.Mechanism + ")"
}

// WebReachable checks the Web UI over loopback using the stored credentials.
// It only reads them: reporting status must never create files on a machine
// where MyAI has not been installed.
func (a *App) WebReachable(ctx context.Context) bool {
	creds, err := secrets.Load(a.env.CredentialsFile())
	if err != nil || !creds.Complete() {
		return false
	}
	return probeWeb(ctx, opencode.LocalWebURL(a.cfg), creds.Username, creds.Password)
}

// WebAccess is what someone needs to reach the Web UI from another machine.
type WebAccess struct {
	// Enabled reports whether the Web UI is configured to run.
	Enabled bool
	// URL is the address to open.
	URL string
	// Username and Password are the basic-auth credentials.
	Username string
	Password string
	// Reachable reports whether the service answers.
	Reachable bool
	// Exposed reports whether the UI is bound beyond loopback.
	Exposed bool
}

// WebAccess returns the current Web UI access details.
func (a *App) WebAccess(ctx context.Context) (WebAccess, error) {
	out := WebAccess{
		Enabled: a.cfg.Web.Enabled,
		URL:     opencode.WebURL(platform.ReachableAddress(), a.cfg),
		Exposed: a.cfg.WebExposedBeyondLoopback(),
	}
	if !a.cfg.Web.Enabled {
		return out, nil
	}

	// Generate credentials only for a machine MyAI is actually installed on;
	// asking for access details elsewhere should not leave a file behind.
	creds, err := secrets.Load(a.env.CredentialsFile())
	if err != nil {
		return out, err
	}
	if !creds.Complete() && fileExists(a.env.ConfigFile()) {
		if creds, err = a.Credentials(); err != nil {
			return out, err
		}
	}
	out.Username = creds.Username
	out.Password = creds.Password
	out.Reachable = probeWeb(ctx, opencode.LocalWebURL(a.cfg), creds.Username, creds.Password)
	return out, nil
}

// browserReady reports whether the optional browser automation tool is
// actually present, rather than merely switched on.
func (a *App) browserReady() bool {
	if !a.cfg.Tools.BrowserAutomation {
		return false
	}
	return run.Available(a.runner, "ego-browser")
}
