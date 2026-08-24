package app

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/Carlboms-Data-AB/myai/internal/backend"
	"github.com/Carlboms-Data-AB/myai/internal/opencode"
	"github.com/Carlboms-Data-AB/myai/internal/service"
)

// Apply writes every generated file from the current configuration and
// restarts the services so the change takes effect. It is idempotent: running
// it twice leaves the machine in the same state.
func (a *App) Apply(ctx context.Context) error {
	if err := a.env.EnsureDirs(); err != nil {
		return err
	}

	openCodeChanged, err := a.writeOpenCodeConfig(ctx)
	if err != nil {
		return err
	}
	a.warnIfModelMissing(ctx)

	changed, err := a.InstallServices(ctx)
	if err != nil {
		return err
	}

	// Restart only what the change actually affects. Bouncing OpenCode Web
	// because the context size moved would interrupt a session, and OpenCode
	// announces itself again every time it starts.
	restartInference := changed[service.RoleInference]
	restartWeb := changed[service.RoleWeb] || openCodeChanged

	return a.restartRoles(ctx, restartInference, restartWeb)
}

// warnIfModelMissing points out that the backend is about to be started
// against a model that is not on disk, which otherwise shows up only as a
// service that will not stay up.
func (a *App) warnIfModelMissing(ctx context.Context) {
	model, err := a.ActiveModel()
	if err != nil {
		return
	}
	have, err := a.Backend().Store().Has(ctx, model)
	if err != nil || have {
		return
	}
	a.reporter.Warn(model.Label() + " is not downloaded; run Install / update or Models to fetch it")
}

// WriteOpenCodeConfig regenerates the managed OpenCode configuration.
func (a *App) WriteOpenCodeConfig(ctx context.Context) error {
	_, err := a.writeOpenCodeConfig(ctx)
	return err
}

// writeOpenCodeConfig regenerates the managed OpenCode configuration and
// reports whether the file changed.
func (a *App) writeOpenCodeConfig(ctx context.Context) (bool, error) {
	model, err := a.ActiveModel()
	if err != nil {
		return false, err
	}
	b := a.Backend()

	previous, _ := os.ReadFile(a.env.OpenCodeConfigFile())

	context := a.servedContext(ctx, b.ModelName(model))
	output := a.cfg.Inference.Output
	if output >= context {
		output = context / 2
	}

	if err := opencode.WriteConfig(a.env.OpenCodeConfigFile(), opencode.ConfigInput{
		BaseURL:   b.BaseURL(a.cfg),
		ModelID:   b.ModelName(model),
		ModelName: model.Label(),
		Limits: opencode.ModelLimits{
			Context: context,
			Output:  output,
		},
		WebTools: a.cfg.Tools.WebSearch,
	}); err != nil {
		return false, err
	}

	current, err := os.ReadFile(a.env.OpenCodeConfigFile())
	if err != nil {
		return false, err
	}
	return string(previous) != string(current), nil
}

// servedContext returns the context window to advertise to OpenCode. The
// configured value is used unless the server is running and says it serves
// less, because telling OpenCode it has more context than it does means
// sessions fail once they grow.
func (a *App) servedContext(ctx context.Context, modelID string) int {
	configured := a.cfg.Inference.Context

	served, ok := a.Inference().ContextLength(ctx, modelID)
	if !ok || served <= 0 || served >= configured {
		return configured
	}
	a.reporter.Warn(fmt.Sprintf(
		"the server serves %d tokens of context, not the configured %d; telling OpenCode the smaller figure",
		served, configured))
	return served
}

// ServiceSpecs builds the definitions for every service MyAI manages. The web
// service is omitted when the Web UI is disabled.
func (a *App) ServiceSpecs(ctx context.Context) ([]service.Spec, error) {
	model, err := a.ActiveModel()
	if err != nil {
		return nil, err
	}
	account, err := a.serviceAccount(ctx)
	if err != nil {
		return nil, err
	}

	inferenceSpec, err := a.Backend().ServiceSpec(ctx, backend.SpecParams{
		Config:    a.cfg,
		Model:     model,
		Name:      a.ServiceName(service.RoleInference),
		StdoutLog: a.env.LogFile("inference"),
		StderrLog: a.env.LogFile("inference-error"),
		LogDir:    a.env.LogDir(),
		WorkDir:   a.env.State,
		Account:   account,
	})
	if err != nil {
		return nil, err
	}
	specs := []service.Spec{inferenceSpec}

	if a.cfg.Web.Enabled {
		webSpec, err := opencode.WebServiceSpec(opencode.WebParams{
			Config:     a.cfg,
			Name:       a.ServiceName(service.RoleWeb),
			Supervisor: a.Executable(),
			WorkDir:    a.env.Home,
			StdoutLog:  a.env.LogFile("opencode-web"),
			StderrLog:  a.env.LogFile("opencode-web-error"),
			Account:    account,
		})
		if err != nil {
			return nil, err
		}
		specs = append(specs, webSpec)
	}
	return specs, nil
}

// InstallServices registers the background services and reports which roles
// were actually changed.
func (a *App) InstallServices(ctx context.Context) (map[string]bool, error) {
	a.StopLegacyServices(ctx)

	changed := map[string]bool{}
	specs, err := a.ServiceSpecs(ctx)
	if err != nil {
		return changed, err
	}
	for _, spec := range specs {
		didChange, err := a.services.Install(ctx, spec)
		if err != nil {
			return changed, err
		}
		changed[spec.Role] = didChange
		if didChange {
			a.reporter.Info("installed service " + spec.Name)
		}
	}

	// Without the Web UI, any previously installed web service should go.
	if !a.cfg.Web.Enabled {
		if err := a.services.Remove(ctx, a.ServiceName(service.RoleWeb)); err != nil {
			a.reporter.Warn("could not remove the OpenCode Web service: " + err.Error())
		}
	}
	return changed, nil
}

// StopLegacyServices shuts down services from the Bash prototype so they do
// not hold the ports MyAI is about to bind. Their definitions are left on
// disk for the user to remove once the new stack is proven.
func (a *App) StopLegacyServices(ctx context.Context) {
	for _, name := range service.LegacyNames(a.host.OS) {
		state, err := a.services.Status(ctx, name)
		if err != nil || !state.Installed {
			continue
		}
		a.reporter.Info("stopping the earlier local-ai service " + name)
		if err := a.services.Stop(ctx, name); err != nil {
			a.reporter.Warn("could not stop " + name + ": " + err.Error())
		}
	}
}

// Restart restarts the services and waits for the inference API. When the
// configuration asks for the model to stay in RAM, it is warmed here so the
// first real request is fast.
func (a *App) Restart(ctx context.Context) error {
	return a.restartRoles(ctx, true, true)
}

// restartRoles restarts the services asked for, waiting for each to answer.
func (a *App) restartRoles(ctx context.Context, inference, web bool) error {
	if !inference && !web {
		a.reporter.Info("nothing needed restarting")
		return nil
	}
	if !inference {
		return a.restartWeb(ctx)
	}
	return a.restartAll(ctx, web)
}

func (a *App) restartWeb(ctx context.Context) error {
	if !a.cfg.Web.Enabled {
		return nil
	}
	a.reporter.Step("Restarting OpenCode Web")
	if err := a.services.Restart(ctx, a.ServiceName(service.RoleWeb)); err != nil {
		return fmt.Errorf("restart the OpenCode Web service: %w", err)
	}
	if a.WaitWebReady(ctx, a.readyTimeout) {
		a.reporter.Info("OpenCode Web ready at " + opencode.LocalWebURL(a.cfg))
	} else {
		a.reporter.Warn("OpenCode Web has not started answering yet")
	}
	return nil
}

func (a *App) restartAll(ctx context.Context, web bool) error {
	name := a.ServiceName(service.RoleInference)
	if state, err := a.services.Status(ctx, name); err == nil && !state.Installed {
		return fmt.Errorf("the %s service is not installed; run Install / update first", name)
	}
	a.reporter.Step("Restarting services")

	if err := a.services.Restart(ctx, name); err != nil {
		return fmt.Errorf("restart the inference service: %w", err)
	}

	client := a.Inference()
	if err := client.WaitReady(ctx, a.readyTimeout); err != nil {
		a.reporter.Warn(err.Error())
	} else {
		a.reporter.Info("inference API ready at " + client.BaseURL)

		// The context a backend serves depends on the model being loaded:
		// mlx-serve reports one figure while idle and a smaller, memory-sized
		// one once weights are in memory. Load the model, then read it, then
		// write the OpenCode configuration, so OpenCode is never told more
		// context than it can actually use.
		if err := a.WarmModel(ctx); err != nil {
			a.reporter.Warn("could not load the model to measure its context: " + err.Error())
		}
		if err := a.WriteOpenCodeConfig(ctx); err != nil {
			a.reporter.Warn("could not refresh the OpenCode configuration: " + err.Error())
		}
	}

	if !web {
		return nil
	}
	return a.restartWeb(ctx)
}

// WaitWebReady polls the Web UI until it answers or the deadline passes.
func (a *App) WaitWebReady(ctx context.Context, total time.Duration) bool {
	deadline := time.Now().Add(total)
	for {
		if a.WebReachable(ctx) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(time.Second):
		}
	}
}

// Start starts both services without reinstalling them.
func (a *App) Start(ctx context.Context) error {
	if err := a.services.Start(ctx, a.ServiceName(service.RoleInference)); err != nil {
		return err
	}
	if !a.cfg.Web.Enabled {
		return nil
	}
	return a.services.Start(ctx, a.ServiceName(service.RoleWeb))
}

// Stop stops both services.
func (a *App) Stop(ctx context.Context) error {
	var firstErr error
	for _, role := range []string{service.RoleWeb, service.RoleInference} {
		if err := a.services.Stop(ctx, a.ServiceName(role)); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// WarmModel loads the active model into memory.
func (a *App) WarmModel(ctx context.Context) error {
	model, err := a.ActiveModel()
	if err != nil {
		return err
	}
	a.reporter.Info("warming " + model.Label())

	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	return a.Inference().Warm(ctx, a.Backend().ModelName(model))
}

// ServeWeb runs the OpenCode Web UI in the foreground. The background service
// invokes this so the Web UI password never has to be stored in a service
// definition.
func (a *App) ServeWeb(ctx context.Context) error {
	if !a.cfg.Web.Enabled {
		return fmt.Errorf("the OpenCode Web UI is disabled in the configuration")
	}
	creds, err := a.Credentials()
	if err != nil {
		return err
	}
	return a.oc.ServeWeb(ctx, a.env.OpenCodeConfigFile(), a.cfg, creds)
}

// LaunchOpenCode starts the OpenCode terminal interface in a directory.
func (a *App) LaunchOpenCode(ctx context.Context, dir string, args []string) error {
	model, err := a.ActiveModel()
	if err != nil {
		return err
	}
	b := a.Backend()
	if err := opencode.ValidateConfig(a.env.OpenCodeConfigFile(), b.ModelName(model), b.BaseURL(a.cfg)); err != nil {
		return fmt.Errorf("%w; run Install / update first", err)
	}
	return a.oc.Launch(ctx, dir, a.env.OpenCodeConfigFile(), a.cfg.Tools.WebSearch, args)
}
