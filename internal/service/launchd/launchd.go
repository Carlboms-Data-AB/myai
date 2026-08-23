// Package launchd manages MyAI's background services on macOS using per-user
// LaunchAgents. They start when the user logs in; they are deliberately not
// pre-login system daemons, because the services need the user's environment
// and repositories.
package launchd

import (
	"context"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/Carlboms-Data-AB/myai/internal/run"
	"github.com/Carlboms-Data-AB/myai/internal/service"
)

// Manager controls LaunchAgents.
type Manager struct {
	// AgentDir is where plists are written, normally ~/Library/LaunchAgents.
	AgentDir string
	// UID is the user id whose GUI domain owns the agents.
	UID int
	// Runner executes launchctl and plutil.
	Runner run.Runner
}

// New returns a Manager writing agents into dir.
func New(agentDir string, uid int, runner run.Runner) *Manager {
	return &Manager{AgentDir: agentDir, UID: uid, Runner: runner}
}

// Kind names the mechanism.
func (m *Manager) Kind() string { return service.KindLaunchd }

// NeedsAccount reports false: LaunchAgents already run as the logged-in user.
func (m *Manager) NeedsAccount() bool { return false }

// PlistPath is the agent file for a label.
func (m *Manager) PlistPath(label string) string {
	return filepath.Join(m.AgentDir, label+".plist")
}

func (m *Manager) target(label string) string {
	return fmt.Sprintf("gui/%d/%s", m.UID, label)
}

func (m *Manager) domain() string { return fmt.Sprintf("gui/%d", m.UID) }

// Install writes the agent and loads it, replacing any earlier definition.
func (m *Manager) Install(ctx context.Context, spec service.Spec) error {
	if err := os.MkdirAll(m.AgentDir, 0o755); err != nil {
		return err
	}
	for _, dir := range logDirs(spec) {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}

	plist, err := Plist(spec)
	if err != nil {
		return err
	}
	path := m.PlistPath(spec.Name)
	if err := os.WriteFile(path, []byte(plist), 0o644); err != nil {
		return err
	}

	// plutil catches a malformed agent before launchd rejects it with a much
	// less helpful message.
	if _, err := m.Runner.Run(ctx, run.Spec{Name: "plutil", Args: []string{"-lint", path}}); err != nil {
		return fmt.Errorf("generated launch agent is invalid: %w", err)
	}

	// Booting out first makes a reinstall pick up the new definition.
	_, _ = m.Runner.Run(ctx, run.Spec{Name: "launchctl", Args: []string{"bootout", m.target(spec.Name)}})

	if _, err := m.Runner.Run(ctx, run.Spec{Name: "launchctl", Args: []string{"bootstrap", m.domain(), path}}); err != nil {
		return fmt.Errorf("load %s: %w", spec.Name, err)
	}
	return nil
}

// Remove unloads the agent and deletes its plist.
func (m *Manager) Remove(ctx context.Context, name string) error {
	_, _ = m.Runner.Run(ctx, run.Spec{Name: "launchctl", Args: []string{"bootout", m.target(name)}})
	err := os.Remove(m.PlistPath(name))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// Start loads the agent and kicks it off.
func (m *Manager) Start(ctx context.Context, name string) error {
	path := m.PlistPath(name)
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("launch agent %s is not installed", name)
	}
	// Bootstrapping an already-loaded agent fails harmlessly, so the kickstart
	// below is what actually guarantees it is running.
	_, _ = m.Runner.Run(ctx, run.Spec{Name: "launchctl", Args: []string{"bootstrap", m.domain(), path}})
	_, err := m.Runner.Run(ctx, run.Spec{Name: "launchctl", Args: []string{"kickstart", "-k", m.target(name)}})
	return err
}

// Stop unloads the agent. The plist stays on disk so it can be started again.
func (m *Manager) Stop(ctx context.Context, name string) error {
	_, _ = m.Runner.Run(ctx, run.Spec{Name: "launchctl", Args: []string{"bootout", m.target(name)}})
	return nil
}

// Restart restarts the agent.
func (m *Manager) Restart(ctx context.Context, name string) error {
	return m.Start(ctx, name)
}

var (
	statePattern = regexp.MustCompile(`state\s*=\s*(\S+)`)
	pidPattern   = regexp.MustCompile(`pid\s*=\s*(\d+)`)
)

// Status reports whether the agent is loaded and running.
func (m *Manager) Status(ctx context.Context, name string) (service.State, error) {
	state := service.State{Name: name}
	if _, err := os.Stat(m.PlistPath(name)); err == nil {
		state.Installed = true
	}

	res, err := m.Runner.Run(ctx, run.Spec{Name: "launchctl", Args: []string{"print", m.target(name)}})
	if err != nil {
		// launchctl fails when the label is not loaded, which is a normal
		// stopped state rather than a fault.
		return state, nil
	}
	state.Installed = true
	if match := statePattern.FindStringSubmatch(res.Output); match != nil {
		state.Detail = match[1]
		state.Running = match[1] == "running"
	}
	if match := pidPattern.FindStringSubmatch(res.Output); match != nil {
		state.PID, _ = strconv.Atoi(match[1])
		state.Running = true
	}
	return state, nil
}

func logDirs(spec service.Spec) []string {
	var out []string
	for _, p := range []string{spec.StdoutLog, spec.StderrLog} {
		if p != "" {
			out = append(out, filepath.Dir(p))
		}
	}
	return out
}

// Plist renders a LaunchAgent property list for a service.
func Plist(spec service.Spec) (string, error) {
	if strings.TrimSpace(spec.Exec) == "" {
		return "", fmt.Errorf("service %s has no executable", spec.Name)
	}

	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	b.WriteString(`<plist version="1.0">` + "\n<dict>\n")

	writeKeyString(&b, "Label", spec.Name)

	b.WriteString("\t<key>ProgramArguments</key>\n\t<array>\n")
	for _, arg := range append([]string{spec.Exec}, spec.Args...) {
		b.WriteString("\t\t<string>" + escape(arg) + "</string>\n")
	}
	b.WriteString("\t</array>\n")

	if pairs := spec.EnvPairs(); len(pairs) > 0 {
		b.WriteString("\t<key>EnvironmentVariables</key>\n\t<dict>\n")
		for _, pair := range pairs {
			k, v, _ := strings.Cut(pair, "=")
			b.WriteString("\t\t<key>" + escape(k) + "</key>\n")
			b.WriteString("\t\t<string>" + escape(v) + "</string>\n")
		}
		b.WriteString("\t</dict>\n")
	}

	if spec.Dir != "" {
		writeKeyString(&b, "WorkingDirectory", spec.Dir)
	}
	if spec.StdoutLog != "" {
		writeKeyString(&b, "StandardOutPath", spec.StdoutLog)
	}
	if spec.StderrLog != "" {
		writeKeyString(&b, "StandardErrorPath", spec.StderrLog)
	}

	b.WriteString("\t<key>RunAtLoad</key>\n\t<true/>\n")
	b.WriteString("\t<key>KeepAlive</key>\n\t<true/>\n")
	writeKeyString(&b, "ProcessType", "Background")

	b.WriteString("</dict>\n</plist>\n")
	return b.String(), nil
}

func writeKeyString(b *strings.Builder, key, value string) {
	b.WriteString("\t<key>" + escape(key) + "</key>\n")
	b.WriteString("\t<string>" + escape(value) + "</string>\n")
}

// escape renders a value safely inside plist XML.
func escape(s string) string {
	var b strings.Builder
	if err := xml.EscapeText(&b, []byte(s)); err != nil {
		return s
	}
	return b.String()
}
