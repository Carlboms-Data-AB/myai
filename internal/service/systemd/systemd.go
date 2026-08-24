// Package systemd manages MyAI's background services on Linux using systemd
// user units, the closest equivalent of a macOS LaunchAgent: they run as the
// logged-in user and need no root.
package systemd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Carlboms-Data-AB/myai/internal/run"
	"github.com/Carlboms-Data-AB/myai/internal/service"
)

// Manager controls systemd user units.
type Manager struct {
	// UnitDir is where unit files are written, normally
	// ~/.config/systemd/user.
	UnitDir string
	// Runner executes systemctl and loginctl.
	Runner run.Runner
	// User is the account whose lingering is enabled so the services survive
	// logout. Empty skips that step.
	User string
}

// New returns a Manager writing units into dir.
func New(unitDir, user string, runner run.Runner) *Manager {
	return &Manager{UnitDir: unitDir, User: user, Runner: runner}
}

// Kind names the mechanism.
func (m *Manager) Kind() string { return service.KindSystemd }

// NeedsAccount reports false: user units already run as the user.
func (m *Manager) NeedsAccount() bool { return false }

// UnitName appends the systemd suffix.
func UnitName(name string) string {
	if strings.HasSuffix(name, ".service") {
		return name
	}
	return name + ".service"
}

// UnitPath is the unit file for a service.
func (m *Manager) UnitPath(name string) string {
	return filepath.Join(m.UnitDir, UnitName(name))
}

func (m *Manager) systemctl(ctx context.Context, args ...string) (run.Result, error) {
	return m.Runner.Run(ctx, run.Spec{Name: "systemctl", Args: append([]string{"--user"}, args...)})
}

// Install writes the unit and enables it, replacing any earlier definition.
func (m *Manager) Install(ctx context.Context, spec service.Spec) (bool, error) {
	if err := os.MkdirAll(m.UnitDir, 0o755); err != nil {
		return false, err
	}
	for _, p := range []string{spec.StdoutLog, spec.StderrLog} {
		if p != "" {
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return false, err
			}
		}
	}

	unit, err := Unit(spec)
	if err != nil {
		return false, err
	}
	path := m.UnitPath(spec.Name)

	// An unchanged unit that is already running needs nothing done to it.
	if existing, err := os.ReadFile(path); err == nil && string(existing) == unit {
		if state, err := m.Status(ctx, spec.Name); err == nil && state.Running {
			return false, nil
		}
	}

	if err := os.WriteFile(path, []byte(unit), 0o644); err != nil {
		return false, err
	}
	if _, err := m.systemctl(ctx, "daemon-reload"); err != nil {
		return false, fmt.Errorf("systemctl daemon-reload: %w", err)
	}
	if _, err := m.systemctl(ctx, "enable", "--now", UnitName(spec.Name)); err != nil {
		return false, fmt.Errorf("enable %s: %w", spec.Name, err)
	}
	return true, nil
}

// EnableLinger asks systemd to keep user services running when nobody is
// logged in. It needs privileges MyAI may not have, so callers treat failure
// as a warning rather than an error.
func (m *Manager) EnableLinger(ctx context.Context) error {
	if m.User == "" {
		return nil
	}
	_, err := m.Runner.Run(ctx, run.Spec{Name: "loginctl", Args: []string{"enable-linger", m.User}})
	return err
}

// Remove stops, disables and deletes the unit.
func (m *Manager) Remove(ctx context.Context, name string) error {
	_, _ = m.systemctl(ctx, "disable", "--now", UnitName(name))
	err := os.Remove(m.UnitPath(name))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	_, _ = m.systemctl(ctx, "daemon-reload")
	return nil
}

// Start starts the unit.
func (m *Manager) Start(ctx context.Context, name string) error {
	_, err := m.systemctl(ctx, "start", UnitName(name))
	return err
}

// Stop stops the unit.
func (m *Manager) Stop(ctx context.Context, name string) error {
	_, err := m.systemctl(ctx, "stop", UnitName(name))
	return err
}

// Restart restarts the unit.
func (m *Manager) Restart(ctx context.Context, name string) error {
	_, err := m.systemctl(ctx, "restart", UnitName(name))
	return err
}

// Status reports the unit's state.
func (m *Manager) Status(ctx context.Context, name string) (service.State, error) {
	state := service.State{Name: name}
	if _, err := os.Stat(m.UnitPath(name)); err == nil {
		state.Installed = true
	}

	res, err := m.systemctl(ctx, "show", UnitName(name), "--property=ActiveState,SubState,MainPID,LoadState")
	if err != nil {
		return state, nil
	}
	for _, line := range strings.Split(res.Output, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch key {
		case "ActiveState":
			state.Detail = value
			state.Running = value == "active"
		case "MainPID":
			if pid, err := strconv.Atoi(value); err == nil && pid > 0 {
				state.PID = pid
			}
		case "LoadState":
			if value == "loaded" {
				state.Installed = true
			}
		}
	}
	return state, nil
}

// Unit renders a systemd user unit for a service.
func Unit(spec service.Spec) (string, error) {
	if strings.TrimSpace(spec.Exec) == "" {
		return "", fmt.Errorf("service %s has no executable", spec.Name)
	}

	var b strings.Builder
	b.WriteString("[Unit]\n")
	description := spec.Description
	if description == "" {
		description = spec.DisplayName
	}
	fmt.Fprintf(&b, "Description=%s\n", description)
	b.WriteString("After=network.target\n\n")

	b.WriteString("[Service]\n")
	b.WriteString("Type=simple\n")
	fmt.Fprintf(&b, "ExecStart=%s\n", commandLine(spec))
	if spec.Dir != "" {
		fmt.Fprintf(&b, "WorkingDirectory=%s\n", spec.Dir)
	}
	for _, pair := range spec.EnvPairs() {
		key, value, _ := strings.Cut(pair, "=")
		fmt.Fprintf(&b, "Environment=%s=%s\n", key, quote(value))
	}
	if spec.StdoutLog != "" {
		fmt.Fprintf(&b, "StandardOutput=append:%s\n", spec.StdoutLog)
	}
	if spec.StderrLog != "" {
		fmt.Fprintf(&b, "StandardError=append:%s\n", spec.StderrLog)
	}
	b.WriteString("Restart=always\n")
	b.WriteString("RestartSec=5\n\n")

	b.WriteString("[Install]\n")
	b.WriteString("WantedBy=default.target\n")
	return b.String(), nil
}

func commandLine(spec service.Spec) string {
	parts := make([]string, 0, len(spec.Args)+1)
	parts = append(parts, quote(spec.Exec))
	for _, arg := range spec.Args {
		parts = append(parts, quote(arg))
	}
	return strings.Join(parts, " ")
}

// quote wraps a value in double quotes when systemd would otherwise split or
// reinterpret it. A literal percent has to be doubled, because systemd expands
// specifiers such as %i inside unit values.
func quote(s string) string {
	escaped := strings.ReplaceAll(s, "%", "%%")
	if escaped != "" && !strings.ContainsAny(escaped, " \t\"'\\$%") {
		return escaped
	}
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + replacer.Replace(escaped) + `"`
}
