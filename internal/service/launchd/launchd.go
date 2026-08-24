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
	"time"

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
	// Sleep waits between load attempts. Tests replace it so they do not have
	// to wait for a state that will never change.
	Sleep func(time.Duration)
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

// Install writes the agent and loads it, replacing any earlier definition. An
// agent whose definition is unchanged and already running is left alone, so
// applying a setting that does not concern it does not interrupt it.
func (m *Manager) Install(ctx context.Context, spec service.Spec) (bool, error) {
	if err := os.MkdirAll(m.AgentDir, 0o755); err != nil {
		return false, err
	}
	for _, dir := range logDirs(spec) {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return false, err
		}
	}

	plist, err := Plist(spec)
	if err != nil {
		return false, err
	}
	path := m.PlistPath(spec.Name)

	if existing, err := os.ReadFile(path); err == nil && string(existing) == plist {
		if m.loaded(ctx, spec.Name) {
			return false, nil
		}
	}

	if err := os.WriteFile(path, []byte(plist), 0o644); err != nil {
		return false, err
	}

	// plutil catches a malformed agent before launchd rejects it with a much
	// less helpful message.
	if _, err := m.Runner.Run(ctx, run.Spec{Name: "plutil", Args: []string{"-lint", path}}); err != nil {
		return false, fmt.Errorf("generated launch agent is invalid: %w", err)
	}

	// Booting out first makes a reinstall pick up the new definition.
	m.bootout(ctx, spec.Name)
	if err := m.load(ctx, spec.Name, path); err != nil {
		return false, err
	}
	return true, nil
}

// load gets the job running from its current definition.
//
// launchctl is asynchronous: bootout returns before the job is gone, so a
// bootstrap that follows too closely fails with "Bootstrap failed: 5", and a
// kickstart during the same window fails with 37, "Operation already in
// progress". Both are transient, so this retries through them and only
// reports a failure that persists.
func (m *Manager) load(ctx context.Context, name, path string) error {
	var lastErr error

	for attempt := 0; attempt < loadAttempts; attempt++ {
		if attempt > 0 {
			m.sleep(loadInterval)
		}

		_, err := m.Runner.Run(ctx, run.Spec{Name: "launchctl", Args: []string{"bootstrap", m.domain(), path}})
		if err == nil {
			return nil
		}
		lastErr = err

		// Already loaded is fine, as long as the running job picks up the
		// definition that was just written.
		if m.loaded(ctx, name) {
			if err := m.kickstart(ctx, name); err == nil {
				return nil
			} else {
				lastErr = err
			}
		}
	}
	return fmt.Errorf("load %s: %w", name, lastErr)
}

func (m *Manager) sleep(d time.Duration) {
	if m.Sleep != nil {
		m.Sleep(d)
		return
	}
	time.Sleep(d)
}

// loaded reports whether launchd currently knows the job. A successful print
// with nothing in it does not describe a job, so it does not count.
func (m *Manager) loaded(ctx context.Context, name string) bool {
	res, err := m.Runner.Run(ctx, run.Spec{Name: "launchctl", Args: []string{"print", m.target(name)}})
	if err != nil {
		return false
	}
	return strings.TrimSpace(res.Output) != ""
}

// bootout asks launchd to unload the job. It returns before launchd has
// necessarily finished, which is why callers cope with a job that is still
// loaded rather than assuming it is gone.
func (m *Manager) bootout(ctx context.Context, name string) {
	_, _ = m.Runner.Run(ctx, run.Spec{Name: "launchctl", Args: []string{"bootout", m.target(name)}})
}

// kickstart restarts a loaded job so it picks up a new definition.
func (m *Manager) kickstart(ctx context.Context, name string) error {
	_, err := m.Runner.Run(ctx, run.Spec{Name: "launchctl", Args: []string{"kickstart", "-k", m.target(name)}})
	return err
}

// restart forces the job to start again, retrying while launchd reports that
// it is busy with the previous transition.
func (m *Manager) restart(ctx context.Context, name string) error {
	var lastErr error
	for attempt := 0; attempt < loadAttempts; attempt++ {
		if attempt > 0 {
			m.sleep(loadInterval)
		}
		if err := m.kickstart(ctx, name); err == nil {
			return nil
		} else {
			lastErr = err
		}
	}
	return fmt.Errorf("restart %s: %w", name, lastErr)
}

// Remove unloads the agent and deletes its plist.
func (m *Manager) Remove(ctx context.Context, name string) error {
	m.bootout(ctx, name)
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
	// Loading makes sure launchd knows the job; the kickstart is what
	// actually restarts it, which is what callers asked for.
	if err := m.load(ctx, name, path); err != nil {
		return err
	}
	return m.restart(ctx, name)
}

// Stop unloads the agent. The plist stays on disk so it can be started again.
func (m *Manager) Stop(ctx context.Context, name string) error {
	m.bootout(ctx, name)
	return nil
}

// Restart restarts the agent.
func (m *Manager) Restart(ctx context.Context, name string) error {
	return m.Start(ctx, name)
}

// launchctl settles asynchronously, so loading retries briefly.
const (
	loadAttempts = 5
	loadInterval = 400 * time.Millisecond
)

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
