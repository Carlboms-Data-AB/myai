package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Carlboms-Data-AB/myai/internal/config"
	"github.com/Carlboms-Data-AB/myai/internal/run"
	"github.com/Carlboms-Data-AB/myai/internal/secrets"
	"github.com/Carlboms-Data-AB/myai/internal/service"
)

// InstallOptions control what an install or upgrade does. The three parts are
// separate so an upgrade never has to touch downloaded models.
type InstallOptions struct {
	// Dependencies installs or updates the inference backend and OpenCode.
	Dependencies bool
	// Model downloads the active model if it is missing. An already
	// downloaded model is never fetched again.
	Model bool
	// Services installs and starts the background services.
	Services bool
	// Command installs the myai executable and puts it on PATH.
	Command bool
	// SelfUpdate fetches a newer MyAI release before doing anything else.
	SelfUpdate bool
}

// FullInstall is what "Install / update" does: everything, but nothing that is
// already in place is redone.
func FullInstall() InstallOptions {
	return InstallOptions{Dependencies: true, Model: true, Services: true, Command: true}
}

// UpgradeOnly refreshes MyAI and its services without considering models.
func UpgradeOnly() InstallOptions {
	return InstallOptions{Dependencies: true, Services: true, Command: true, SelfUpdate: true}
}

// Install sets MyAI up, or brings an existing installation up to date. It is
// idempotent, and it reuses whatever is already present.
func (a *App) Install(ctx context.Context, opts InstallOptions) error {
	if !a.host.Supported() {
		return fmt.Errorf("MyAI does not support %s", a.host.Label())
	}
	if err := a.env.EnsureDirs(); err != nil {
		return err
	}

	if opts.SelfUpdate {
		if _, err := a.SelfUpdate(ctx); err != nil {
			a.reporter.Warn("could not check for a newer MyAI: " + err.Error())
		}
	}

	if err := a.MigrateFromPrototype(ctx); err != nil {
		a.reporter.Warn("could not migrate the earlier local-ai settings: " + err.Error())
	}

	// Persist the configuration early so a later step failing still leaves a
	// usable configuration behind.
	if err := a.SetConfig(a.cfg); err != nil {
		return err
	}
	if _, err := secrets.Ensure(a.env.CredentialsFile(), a.cfg.Web.Username); err != nil {
		return err
	}

	if opts.Dependencies {
		// Record what MyAI installs, so uninstall can remove exactly that and
		// leave alone anything that was already here.
		b := a.Backend()
		backendWasPresent := b.Detect(ctx).Installed
		openCodeWasPresent := a.oc.Detect(ctx).Installed

		if err := b.Install(ctx, a.cfg, a.reporter); err != nil {
			return err
		}
		if err := a.oc.Install(ctx, a.reporter); err != nil {
			return err
		}

		if err := a.recordInstalled(func(m *Manifest) {
			if !backendWasPresent && b.Detect(ctx).Installed {
				m.Backend = true
				m.BackendName = b.Name()
			}
			if !openCodeWasPresent && a.oc.Detect(ctx).Installed {
				m.OpenCode = true
			}
		}); err != nil {
			a.reporter.Warn("could not record what was installed: " + err.Error())
		}
	}

	if opts.Model {
		if err := a.installChosenModels(ctx); err != nil {
			return err
		}
	}

	if opts.Command && !opts.SelfUpdate {
		// A self-update has already put the newest binary in place; copying
		// the running one over it would undo that.
		if err := a.InstallCommand(); err != nil {
			return err
		}
	}

	if err := a.WriteOpenCodeConfig(ctx); err != nil {
		return err
	}

	if opts.Services {
		changed, err := a.InstallServices(ctx)
		if err != nil {
			return err
		}
		// A fresh install has to start them; an upgrade only restarts what
		// the new definitions actually changed.
		if err := a.restartRoles(ctx,
			changed[service.RoleInference] || !a.serviceRunning(ctx, service.RoleInference),
			changed[service.RoleWeb] || !a.serviceRunning(ctx, service.RoleWeb),
		); err != nil {
			return err
		}
	}
	return nil
}

// ChooseModels is how a caller offers the model choice during install. It
// receives what MyAI can install here and returns the ids to download, with
// the first becoming the active model. Returning nothing means "keep whatever
// is configured".
type ChooseModels func(offered []ModelEntry) ([]string, error)

// ModelChooser is consulted during install when it is set. Without one,
// install downloads the configured model and asks nothing, which is what a
// non-interactive run needs.
func (a *App) SetModelChooser(choose ChooseModels) { a.chooseModels = choose }

// installChosenModels downloads the models the operator picked, or the
// configured one when there is nobody to ask.
func (a *App) installChosenModels(ctx context.Context) error {
	store := a.Backend().Store()

	if a.chooseModels != nil {
		offered := a.Offered(ctx)
		chosen, err := a.chooseModels(offered)
		if err != nil {
			return err
		}
		if len(chosen) > 0 {
			for _, id := range chosen {
				resolved, err := a.resolveForStore(ctx, store, id)
				if err != nil {
					return err
				}
				if err := store.Install(ctx, resolved, a.reporter); err != nil {
					return err
				}
			}
			// The first pick becomes the active model.
			return a.Update(func(c *config.Config) { c.ActiveModel = chosen[0] })
		}
	}

	model, err := a.ActiveModel()
	if err != nil {
		return err
	}
	return store.Install(ctx, model, a.reporter)
}

// serviceRunning reports whether a role's service is up.
func (a *App) serviceRunning(ctx context.Context, role string) bool {
	state, err := a.services.Status(ctx, a.ServiceName(role))
	return err == nil && state.Running
}

// InstallCommand copies the running binary to the MyAI bin directory and makes
// sure that directory is on PATH.
func (a *App) InstallCommand() error {
	source := a.exe
	if source == "" {
		return errors.New("cannot locate the running myai executable")
	}
	target := a.env.Executable()

	sourceInfo, err := os.Stat(source)
	if err != nil {
		return err
	}
	if targetInfo, err := os.Stat(target); err == nil && os.SameFile(sourceInfo, targetInfo) {
		return a.ensurePath()
	}

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	if err := copyExecutable(source, target); err != nil {
		return err
	}
	a.reporter.Info("installed the myai command at " + target)
	return a.ensurePath()
}

func copyExecutable(source, target string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()

	// Writing to a temporary name first means a running myai is never
	// replaced underneath itself.
	tmp := target + ".new"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	os.Remove(target)
	return os.Rename(tmp, target)
}

// pathMarkers delimit the block MyAI manages in a shell profile.
const (
	pathBegin = "# >>> myai >>>"
	pathEnd   = "# <<< myai <<<"
)

// ensurePath makes the MyAI bin directory reachable from a new shell.
func (a *App) ensurePath() error {
	if onPath(a.env.Bin) {
		return nil
	}
	if a.host.OS == "windows" {
		// Changing the user PATH on Windows is the operator's call, so MyAI
		// says what to do rather than editing it.
		a.reporter.Warn("add " + a.env.Bin + " to your PATH to run myai from any prompt")
		return nil
	}

	profile := a.shellProfile()
	if profile == "" {
		a.reporter.Warn("add " + a.env.Bin + " to your PATH to run myai from any shell")
		return nil
	}

	existing, err := os.ReadFile(profile)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	body := removeBlock(string(existing))
	body += fmt.Sprintf("\n%s\nexport PATH=\"%s:$PATH\"\n%s\n", pathBegin, a.env.Bin, pathEnd)

	if err := os.WriteFile(profile, []byte(body), 0o644); err != nil {
		return err
	}
	a.reporter.Info("added " + a.env.Bin + " to PATH in " + profile)
	return nil
}

// shellProfile picks the profile file to edit for the current shell.
func (a *App) shellProfile() string {
	shell := filepath.Base(os.Getenv("SHELL"))
	switch shell {
	case "zsh":
		return filepath.Join(a.env.Home, ".zshrc")
	case "bash":
		return filepath.Join(a.env.Home, ".bashrc")
	}
	// Fall back to whichever profile already exists.
	for _, name := range []string{".zshrc", ".bashrc", ".profile"} {
		path := filepath.Join(a.env.Home, name)
		if fileExists(path) {
			return path
		}
	}
	return ""
}

// removeBlock strips a previously written MyAI PATH block.
func removeBlock(body string) string {
	var out []string
	skipping := false
	for _, line := range strings.Split(body, "\n") {
		switch strings.TrimSpace(line) {
		case pathBegin:
			skipping = true
			continue
		case pathEnd:
			skipping = false
			continue
		}
		if !skipping {
			out = append(out, line)
		}
	}
	return strings.TrimRight(strings.Join(out, "\n"), "\n")
}

func onPath(dir string) bool {
	for _, entry := range filepath.SplitList(os.Getenv("PATH")) {
		if entry == dir {
			return true
		}
	}
	return false
}

// MigrateFromPrototype imports settings from the Bash local-ai installation,
// once. It reads only the configuration: downloaded models are left exactly
// where they are.
func (a *App) MigrateFromPrototype(ctx context.Context) error {
	legacy := filepath.Join(a.env.LegacyConfigDir(), "config")
	if fileExists(a.env.ConfigFile()) || !fileExists(legacy) {
		return nil
	}

	body, err := os.ReadFile(legacy)
	if err != nil {
		return err
	}
	a.reporter.Step("Importing settings from the earlier local-ai installation")

	cfg := a.cfg
	if err := applyLegacyConfig(&cfg, string(body)); err != nil {
		return err
	}
	if err := a.SetConfig(cfg); err != nil {
		return err
	}

	// The generated Web UI password is worth keeping so existing bookmarks
	// and saved logins still work.
	if creds, err := readLegacyCredentials(filepath.Join(a.env.LegacyConfigDir(), "web-auth")); err == nil && creds.Complete() {
		if err := secrets.Save(a.env.CredentialsFile(), creds); err != nil {
			a.reporter.Warn("could not import the Web UI password: " + err.Error())
		} else {
			a.reporter.Info("kept the existing Web UI password")
		}
	}

	a.StopLegacyServices(ctx)
	a.reporter.Info("downloaded models were left untouched")
	return nil
}

// applyLegacyConfig maps the prototype's KEY=value settings onto the current
// configuration.
func applyLegacyConfig(cfg *config.Config, body string) error {
	for _, line := range strings.Split(body, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch key {
		case "MODEL":
			// The prototype stored an MLX repository id directly. Prefer the
			// catalog entry when it describes the same artifact, so the model
			// gains a proper name without being re-downloaded.
			cfg.ActiveModel = value
			if id, ok := catalogIDForRef(value); ok {
				cfg.ActiveModel = id
			}
		case "CONTEXT":
			cfg.Inference.Context = atoiOr(value, cfg.Inference.Context)
		case "OUTPUT":
			cfg.Inference.Output = atoiOr(value, cfg.Inference.Output)
		case "PORT":
			cfg.Inference.Port = atoiOr(value, cfg.Inference.Port)
		case "WEB_PORT":
			cfg.Web.Port = atoiOr(value, cfg.Web.Port)
		case "WEB":
			cfg.Tools.WebSearch = value == "true"
		case "EGO":
			cfg.Tools.BrowserAutomation = value == "true"
		case "IDLE":
			if seconds := atoiOr(value, 0); seconds > 0 {
				cfg.Inference.KeepInRAM = false
				cfg.Inference.IdleUnloadMinutes = seconds / 60
			}
		}
	}
	cfg.Normalize()
	return cfg.Validate()
}

// readLegacyCredentials parses the prototype's web-auth file.
func readLegacyCredentials(path string) (secrets.Credentials, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return secrets.Credentials{}, err
	}
	var creds secrets.Credentials
	for _, line := range strings.Split(string(body), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch key {
		case "WEB_USERNAME":
			creds.Username = value
		case "WEB_PASSWORD":
			creds.Password = value
		}
	}
	return creds, nil
}

func atoiOr(s string, fallback int) int {
	n := 0
	if s == "" {
		return fallback
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return fallback
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// RotateWebPassword generates a new Web UI password. The Web service has to
// restart before it takes effect.
func (a *App) RotateWebPassword() (secrets.Credentials, error) {
	return secrets.Rotate(a.env.CredentialsFile(), a.cfg.Web.Username)
}

// InstallBrowserSkill installs the optional ego-browser skill. It is separate
// from the main install because browser automation reaches real, signed-in
// browser sessions and is therefore opt-in.
func (a *App) InstallBrowserSkill(ctx context.Context) error {
	if !run.Available(a.runner, "npx") {
		return errors.New("Node.js is required for browser automation: install Node, then try again")
	}
	a.reporter.Step("Installing the ego-browser skill")
	_, err := a.runner.Run(ctx, run.Spec{
		Name:   "npx",
		Args:   []string{"skills", "add", "citrolabs/ego-lite"},
		OnLine: a.reporter.Info,
	})
	if err != nil {
		return err
	}
	a.reporter.Info("ego lite also needs its macOS application and a one-time setup in its own interface")

	if err := a.recordInstalled(func(m *Manifest) { m.BrowserSkill = true }); err != nil {
		a.reporter.Warn("could not record the browser skill: " + err.Error())
	}
	return nil
}
