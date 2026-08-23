// Package nssm manages MyAI's background services on Windows through NSSM,
// the Non-Sucking Service Manager. NSSM turns an ordinary executable into a
// native Windows service, so MyAI does not have to ship a service host of its
// own.
//
// Windows services default to running as LocalSystem. That is the wrong
// identity for a coding agent: it has no user profile and far too much
// privilege. MyAI therefore always sets ObjectName to the operator's own
// account, which is why installing services on Windows needs an elevated
// prompt and a password.
package nssm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Carlboms-Data-AB/myai/internal/run"
	"github.com/Carlboms-Data-AB/myai/internal/service"
)

// ErrAccountRequired is returned when a service would otherwise be installed
// as LocalSystem.
var ErrAccountRequired = errors.New("a Windows account is required to install MyAI services")

// Manager controls services through the nssm executable.
type Manager struct {
	// Exec is the nssm executable name or path.
	Exec string
	// Runner executes nssm.
	Runner run.Runner
}

// New returns a Manager driving the given nssm executable.
func New(exec string, runner run.Runner) *Manager {
	if exec == "" {
		exec = "nssm"
	}
	return &Manager{Exec: exec, Runner: runner}
}

// Kind names the mechanism.
func (m *Manager) Kind() string { return service.KindNSSM }

// NeedsAccount reports true: Windows services must be told which account to
// run as.
func (m *Manager) NeedsAccount() bool { return true }

func (m *Manager) nssm(ctx context.Context, args ...string) (run.Result, error) {
	return m.Runner.Run(ctx, run.Spec{Name: m.Exec, Args: args})
}

// Exists reports whether the service is already registered.
func (m *Manager) Exists(ctx context.Context, name string) bool {
	_, err := m.nssm(ctx, "status", name)
	return err == nil
}

// Install registers the service, or updates it when it already exists.
// Updating in place avoids the window where a removed service still lingers in
// the service database and a fresh install fails.
func (m *Manager) Install(ctx context.Context, spec service.Spec) error {
	if strings.TrimSpace(spec.Exec) == "" {
		return fmt.Errorf("service %s has no executable", spec.Name)
	}
	exists := m.Exists(ctx, spec.Name)
	if !exists && strings.TrimSpace(spec.Account.User) == "" {
		return ErrAccountRequired
	}
	for _, p := range []string{spec.StdoutLog, spec.StderrLog} {
		if p != "" {
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return err
			}
		}
	}

	if !exists {
		if _, err := m.nssm(ctx, "install", spec.Name, spec.Exec); err != nil {
			return fmt.Errorf("install %s: %w", spec.Name, err)
		}
	}

	for _, setting := range Settings(spec) {
		if _, err := m.nssm(ctx, append([]string{"set", spec.Name}, setting...)...); err != nil {
			return fmt.Errorf("configure %s %s: %w", spec.Name, setting[0], err)
		}
	}

	// The account is set separately so the password never reaches Settings,
	// which is rendered in tests and diagnostics. An existing service keeps
	// the account it already has, so routine reconfiguration does not have to
	// ask for a password again.
	if strings.TrimSpace(spec.Account.User) != "" {
		if _, err := m.nssm(ctx, "set", spec.Name, "ObjectName", spec.Account.User, spec.Account.Password); err != nil {
			return fmt.Errorf("set service account for %s: %w", spec.Name, err)
		}
	}
	return nil
}

// Settings returns the nssm parameters for a service, excluding the account
// credentials. Each entry is a parameter followed by its values.
func Settings(spec service.Spec) [][]string {
	settings := [][]string{
		{"Application", spec.Exec},
		{"AppParameters", strings.Join(quoteAll(spec.Args), " ")},
		{"Start", "SERVICE_AUTO_START"},
		{"AppExit", "Default", "Restart"},
		{"AppRestartDelay", "5000"},
	}
	if spec.DisplayName != "" {
		settings = append(settings, []string{"DisplayName", spec.DisplayName})
	}
	if spec.Description != "" {
		settings = append(settings, []string{"Description", spec.Description})
	}
	if spec.Dir != "" {
		settings = append(settings, []string{"AppDirectory", spec.Dir})
	}
	if spec.StdoutLog != "" {
		settings = append(settings, []string{"AppStdout", spec.StdoutLog})
	}
	if spec.StderrLog != "" {
		settings = append(settings, []string{"AppStderr", spec.StderrLog})
	}
	if spec.StdoutLog != "" || spec.StderrLog != "" {
		settings = append(settings, []string{"AppRotateFiles", "1"})
	}
	if pairs := spec.EnvPairs(); len(pairs) > 0 {
		settings = append(settings, append([]string{"AppEnvironmentExtra"}, pairs...))
	}
	return settings
}

// Remove stops and deregisters the service. Removing an absent service is not
// an error.
func (m *Manager) Remove(ctx context.Context, name string) error {
	if !m.Exists(ctx, name) {
		return nil
	}
	_, _ = m.nssm(ctx, "stop", name)
	_, err := m.nssm(ctx, "remove", name, "confirm")
	return err
}

// Start starts the service.
func (m *Manager) Start(ctx context.Context, name string) error {
	_, err := m.nssm(ctx, "start", name)
	return err
}

// Stop stops the service.
func (m *Manager) Stop(ctx context.Context, name string) error {
	_, err := m.nssm(ctx, "stop", name)
	return err
}

// Restart restarts the service.
func (m *Manager) Restart(ctx context.Context, name string) error {
	_, err := m.nssm(ctx, "restart", name)
	return err
}

// Status reports the service state from nssm's own vocabulary.
func (m *Manager) Status(ctx context.Context, name string) (service.State, error) {
	state := service.State{Name: name}

	res, err := m.nssm(ctx, "status", name)
	if err != nil {
		return state, nil
	}
	state.Installed = true
	state.Detail = strings.TrimSpace(cleanUTF16(res.Output))
	state.Running = strings.EqualFold(state.Detail, "SERVICE_RUNNING")
	return state, nil
}

// cleanUTF16 strips the NUL bytes nssm emits when it writes UTF-16 output to a
// redirected handle.
func cleanUTF16(s string) string {
	return strings.ReplaceAll(s, "\x00", "")
}

// quoteAll wraps arguments containing spaces so NSSM passes them through as
// single arguments.
func quoteAll(args []string) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		if strings.ContainsAny(a, " \t") {
			out = append(out, `"`+a+`"`)
			continue
		}
		out = append(out, a)
	}
	return out
}
