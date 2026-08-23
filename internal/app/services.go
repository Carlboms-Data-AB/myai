package app

import (
	"context"
	"fmt"
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
	if err := a.WriteOpenCodeConfig(ctx); err != nil {
		return err
	}
	if err := a.InstallServices(ctx); err != nil {
		return err
	}
	return a.Restart(ctx)
}

// WriteOpenCodeConfig regenerates the managed OpenCode configuration from the
// current settings and active model.
func (a *App) WriteOpenCodeConfig(ctx context.Context) error {
	model, err := a.ActiveModel()
	if err != nil {
		return err
	}
	b := a.Backend()

	return opencode.WriteConfig(a.env.OpenCodeConfigFile(), opencode.ConfigInput{
		BaseURL:   b.BaseURL(a.cfg),
		ModelID:   b.ModelName(model),
		ModelName: model.Label(),
		Limits: opencode.ModelLimits{
			Context: a.cfg.Inference.Context,
			Output:  a.cfg.Inference.Output,
		},
		WebTools: a.cfg.Tools.WebSearch,
	})
}

// ServiceSpecs builds the definitions for every service MyAI manages. The web
// service is omitted when the Web UI is disabled.
func (a *App) ServiceSpecs(ctx context.Context) ([]service.Spec, error) {
	model, err := a.ActiveModel()
	if err != nil {
		return nil, err
	}
	account, err := a.serviceAccount()
	if err != nil {
		return nil, err
	}

	inferenceSpec, err := a.Backend().ServiceSpec(ctx, backend.SpecParams{
		Config:    a.cfg,
		Model:     model,
		Name:      a.ServiceName(service.RoleInference),
		StdoutLog: a.env.LogFile("inference"),
		StderrLog: a.env.LogFile("inference-error"),
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

// InstallServices registers the background services.
func (a *App) InstallServices(ctx context.Context) error {
	a.StopLegacyServices(ctx)

	specs, err := a.ServiceSpecs(ctx)
	if err != nil {
		return err
	}
	for _, spec := range specs {
		a.reporter.Step("Installing service " + spec.Name)
		if err := a.services.Install(ctx, spec); err != nil {
			return err
		}
	}

	// Without the Web UI, any previously installed web service should go.
	if !a.cfg.Web.Enabled {
		if err := a.services.Remove(ctx, a.ServiceName(service.RoleWeb)); err != nil {
			a.reporter.Warn("could not remove the OpenCode Web service: " + err.Error())
		}
	}
	return nil
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
	a.reporter.Step("Restarting services")

	if err := a.services.Restart(ctx, a.ServiceName(service.RoleInference)); err != nil {
		return fmt.Errorf("restart the inference service: %w", err)
	}

	client := a.Inference()
	if err := client.WaitReady(ctx, a.readyTimeout); err != nil {
		a.reporter.Warn(err.Error())
	} else {
		a.reporter.Info("inference API ready at " + client.BaseURL)
		if a.cfg.Inference.KeepInRAM {
			if err := a.WarmModel(ctx); err != nil {
				a.reporter.Warn("could not warm the model: " + err.Error())
			}
		}
	}

	if !a.cfg.Web.Enabled {
		return nil
	}
	if err := a.services.Restart(ctx, a.ServiceName(service.RoleWeb)); err != nil {
		return fmt.Errorf("restart the OpenCode Web service: %w", err)
	}
	return nil
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
