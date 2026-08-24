// Package service installs and controls the two persistent MyAI background
// services. Each operating system has its own manager: launchd on macOS,
// systemd user units on Linux and NSSM on Windows. Callers describe what they
// want to run and never learn which mechanism carries it.
package service

import (
	"context"
	"fmt"
	"sort"
)

// Roles of the two persistent services.
const (
	// RoleInference is the local model server.
	RoleInference = "inference"
	// RoleWeb is the OpenCode Web interface.
	RoleWeb = "web"
)

// Kinds of service manager.
const (
	KindLaunchd = "launchd"
	KindSystemd = "systemd --user"
	KindNSSM    = "nssm"
)

// Account is the identity a service runs as. It is only meaningful on
// Windows, where services otherwise run as LocalSystem and would see the
// wrong profile. The password is passed straight to the service manager and is
// never stored by MyAI.
type Account struct {
	User     string
	Password string
}

// Spec describes a service MyAI manages.
type Spec struct {
	// Role is RoleInference or RoleWeb.
	Role string
	// Name is the platform-specific service identifier.
	Name string
	// DisplayName is shown in system service tooling.
	DisplayName string
	// Description explains the service to an administrator.
	Description string
	// Exec is the absolute path to the executable.
	Exec string
	// Args are its arguments.
	Args []string
	// Env holds environment variables the service needs.
	Env map[string]string
	// Dir is the working directory.
	Dir string
	// StdoutLog and StderrLog are the log file paths.
	StdoutLog string
	StderrLog string
	// Account is the identity to run as, where the platform supports it.
	Account Account
}

// EnvPairs renders Env as sorted "KEY=value" strings so generated service
// definitions are stable between runs.
func (s Spec) EnvPairs() []string {
	keys := make([]string, 0, len(s.Env))
	for k := range s.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+s.Env[k])
	}
	return out
}

// State is what a service is currently doing.
type State struct {
	// Name is the service identifier.
	Name string
	// Installed reports whether the service is registered with the OS.
	Installed bool
	// Running reports whether it is currently up.
	Running bool
	// PID is the main process id when known.
	PID int
	// Detail carries the manager's own wording, useful when something is
	// wrong.
	Detail string
}

// Summary renders the state for display.
func (s State) Summary() string {
	switch {
	case !s.Installed:
		return "not installed"
	case s.Running && s.PID > 0:
		return fmt.Sprintf("running (pid %d)", s.PID)
	case s.Running:
		return "running"
	default:
		return "stopped"
	}
}

// Manager installs and controls services on one operating system.
type Manager interface {
	// Kind names the underlying mechanism.
	Kind() string
	// Install registers the service, replacing any earlier definition. It
	// reports whether anything actually changed, so a caller does not restart
	// a service whose definition is already in place and running.
	Install(ctx context.Context, spec Spec) (changed bool, err error)
	// Remove stops and deregisters the service. Removing an absent service
	// succeeds.
	Remove(ctx context.Context, name string) error
	// Start starts an installed service.
	Start(ctx context.Context, name string) error
	// Stop stops a running service.
	Stop(ctx context.Context, name string) error
	// Restart restarts a service.
	Restart(ctx context.Context, name string) error
	// Status reports the current state.
	Status(ctx context.Context, name string) (State, error)
	// NeedsAccount reports whether Install requires Spec.Account to be set.
	NeedsAccount() bool
}

// Name returns the service identifier for a role on a platform. These names
// are part of the product's contract with the operating system, so they are
// defined in one place.
func Name(goos, role string) string {
	switch goos {
	case "darwin":
		if role == RoleWeb {
			return "se.carlbomsdata.myai-opencode"
		}
		return "se.carlbomsdata.myai"
	case "windows":
		if role == RoleWeb {
			return "MyAI-OpenCode"
		}
		return "MyAI"
	default:
		if role == RoleWeb {
			return "myai-opencode"
		}
		return "myai"
	}
}

// DisplayName returns the human-readable service name for a role.
func DisplayName(role string) string {
	if role == RoleWeb {
		return "MyAI OpenCode Web"
	}
	return "MyAI Inference"
}

// Description returns the administrator-facing description for a role.
func Description(role string) string {
	if role == RoleWeb {
		return "OpenCode Web interface served against the local MyAI model"
	}
	return "Local model server for MyAI"
}

// LegacyNames lists service identifiers from earlier versions of this project
// that must be stopped before MyAI binds the same ports.
func LegacyNames(goos string) []string {
	if goos != "darwin" {
		return nil
	}
	return []string{
		"se.carlbomsdata.local-ai-mlx-serve",
		"se.carlbomsdata.local-ai-opencode-web",
		"se.carlbomsdata.mlx-serve",
	}
}
