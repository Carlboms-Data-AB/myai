package opencode

import (
	"context"
	"fmt"
	"strconv"

	"github.com/Carlboms-Data-AB/myai/internal/config"
	"github.com/Carlboms-Data-AB/myai/internal/secrets"
	"github.com/Carlboms-Data-AB/myai/internal/service"
)

// WebParams describes the OpenCode Web background service.
type WebParams struct {
	// Config is the current configuration.
	Config config.Config
	// Name is the platform-specific service name.
	Name string
	// Supervisor is the myai executable. The service runs MyAI rather than
	// OpenCode directly so the Web UI password never has to be written into a
	// launch agent, systemd unit or the Windows service registry, all of which
	// are readable by others.
	Supervisor string
	// WorkDir is the working directory for the service.
	WorkDir string
	// StdoutLog and StderrLog are where the service writes output.
	StdoutLog string
	StderrLog string
	// Account is the identity to run as, where the platform needs one.
	Account service.Account
}

// WebServiceSpec describes the OpenCode Web service.
func WebServiceSpec(p WebParams) (service.Spec, error) {
	if p.Supervisor == "" {
		return service.Spec{}, fmt.Errorf("the MyAI executable path is required to run the web service")
	}
	return service.Spec{
		Role:        service.RoleWeb,
		Name:        p.Name,
		DisplayName: service.DisplayName(service.RoleWeb),
		Description: service.Description(service.RoleWeb),
		Exec:        p.Supervisor,
		Args:        []string{"serve-web"},
		Env:         map[string]string{"MYAI_ROLE": service.RoleWeb},
		Dir:         p.WorkDir,
		StdoutLog:   p.StdoutLog,
		StderrLog:   p.StderrLog,
		Account:     p.Account,
	}, nil
}

// WebArgs returns the OpenCode command line for the Web UI.
func WebArgs(cfg config.Config) []string {
	return []string{
		"web",
		"--hostname", cfg.Web.Host,
		"--port", strconv.Itoa(cfg.Web.Port),
	}
}

// WebEnv returns the environment for the Web UI process, including the
// credentials OpenCode uses for basic authentication.
func (o *OpenCode) WebEnv(configPath string, cfg config.Config, creds secrets.Credentials) (map[string]string, error) {
	env, err := o.Env(configPath, cfg.Tools.WebSearch)
	if err != nil {
		return nil, err
	}
	if cfg.WebExposedBeyondLoopback() && !creds.Complete() {
		return nil, fmt.Errorf("the Web UI is reachable from the network, so a password is required")
	}
	if creds.Complete() {
		env["OPENCODE_SERVER_USERNAME"] = creds.Username
		env["OPENCODE_SERVER_PASSWORD"] = creds.Password
	}
	return env, nil
}

// ServeWeb runs the OpenCode Web UI in the foreground. It is what the
// background service invokes.
func (o *OpenCode) ServeWeb(ctx context.Context, configPath string, cfg config.Config, creds secrets.Credentials) error {
	path, err := o.Path(ctx)
	if err != nil {
		return err
	}
	env, err := o.WebEnv(configPath, cfg, creds)
	if err != nil {
		return err
	}
	return execReplace(ctx, path, WebArgs(cfg), env)
}

// WebURL is the address to give someone on another machine.
func WebURL(host string, cfg config.Config) string {
	return "http://" + host + ":" + strconv.Itoa(cfg.Web.Port)
}

// LocalWebURL is the loopback address MyAI uses for its own health checks.
func LocalWebURL(cfg config.Config) string {
	return "http://127.0.0.1:" + strconv.Itoa(cfg.Web.Port)
}
