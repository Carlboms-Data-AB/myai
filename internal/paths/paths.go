// Package paths resolves the on-disk locations MyAI uses on each operating
// system. Resolution is a pure function of the target OS, the environment and
// the user's home directory so that every platform's layout can be tested from
// any machine.
package paths

import (
	"os"
	"path/filepath"
	"runtime"
)

// Env holds every directory MyAI reads or writes.
type Env struct {
	// OS is the target operating system, as in runtime.GOOS.
	OS string
	// Arch is the target architecture, as in runtime.GOARCH.
	Arch string
	// Home is the user's home directory.
	Home string
	// Config holds config.toml, the managed OpenCode config and credentials.
	Config string
	// State holds logs and other regenerable runtime state.
	State string
	// Data holds downloaded artifacts owned by MyAI, such as GGUF models.
	Data string
	// Bin is where the myai executable installs itself.
	Bin string
}

// LookupFunc reads an environment variable, mirroring os.LookupEnv.
type LookupFunc func(string) (string, bool)

// Current resolves the layout for the running machine.
func Current() (Env, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Env{}, err
	}
	return Resolve(runtime.GOOS, runtime.GOARCH, home, os.LookupEnv), nil
}

// Resolve computes the layout for an arbitrary platform. It never touches the
// filesystem.
func Resolve(goos, goarch, home string, lookup LookupFunc) Env {
	if lookup == nil {
		lookup = func(string) (string, bool) { return "", false }
	}
	env := Env{OS: goos, Arch: goarch, Home: home}

	switch goos {
	case "windows":
		appData := dirOr(lookup, "APPDATA", filepath.Join(home, "AppData", "Roaming"))
		localAppData := dirOr(lookup, "LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
		env.Config = filepath.Join(appData, "MyAI")
		env.State = filepath.Join(localAppData, "MyAI", "state")
		env.Data = filepath.Join(localAppData, "MyAI", "data")
		env.Bin = filepath.Join(localAppData, "MyAI", "bin")
	default:
		// macOS and Linux both follow the XDG layout the Bash prototype used,
		// which keeps the existing macOS install recognisable.
		env.Config = filepath.Join(dirOr(lookup, "XDG_CONFIG_HOME", filepath.Join(home, ".config")), "myai")
		env.State = filepath.Join(dirOr(lookup, "XDG_STATE_HOME", filepath.Join(home, ".local", "state")), "myai")
		env.Data = filepath.Join(dirOr(lookup, "XDG_DATA_HOME", filepath.Join(home, ".local", "share")), "myai")
		env.Bin = filepath.Join(home, ".local", "bin")
	}
	return env
}

func dirOr(lookup LookupFunc, key, fallback string) string {
	if v, ok := lookup(key); ok && v != "" {
		return v
	}
	return fallback
}

// ConfigFile is the main MyAI configuration file.
func (e Env) ConfigFile() string { return filepath.Join(e.Config, "config.toml") }

// CredentialsFile stores the generated OpenCode Web credentials.
func (e Env) CredentialsFile() string { return filepath.Join(e.Config, "web-auth.toml") }

// OpenCodeConfigFile is the isolated OpenCode config MyAI manages.
func (e Env) OpenCodeConfigFile() string { return filepath.Join(e.Config, "opencode.json") }

// LogDir holds service logs.
func (e Env) LogDir() string { return filepath.Join(e.State, "logs") }

// LogFile returns the log path for a named stream.
func (e Env) LogFile(name string) string { return filepath.Join(e.LogDir(), name+".log") }

// GGUFModelDir is where MyAI stores GGUF models for the llama.cpp backend.
func (e Env) GGUFModelDir() string { return filepath.Join(e.Data, "models") }

// MLXModelDir is the shared mlx-serve model store. mlx-serve owns this
// directory; MyAI only reads it and asks mlx-serve to populate it.
func (e Env) MLXModelDir() string { return filepath.Join(e.Home, ".mlx-serve", "models") }

// SystemdUserDir is where systemd looks for user units on Linux.
func (e Env) SystemdUserDir() string {
	return filepath.Join(filepath.Dir(e.Config), "systemd", "user")
}

// ToolsDir holds runtime dependencies MyAI installs itself, such as llama.cpp
// or OpenCode binaries downloaded from upstream releases.
func (e Env) ToolsDir() string { return filepath.Join(e.Data, "tools") }

// Executable is the installed myai command path.
func (e Env) Executable() string {
	name := "myai"
	if e.OS == "windows" {
		name += ".exe"
	}
	return filepath.Join(e.Bin, name)
}

// LegacyConfigDir is the Bash prototype's configuration directory, used only
// to migrate settings forward.
func (e Env) LegacyConfigDir() string {
	return filepath.Join(e.Home, ".config", "local-ai-mac-mini")
}

// EnsureDirs creates the directories MyAI needs to operate.
func (e Env) EnsureDirs() error {
	for _, dir := range []string{e.Config, e.State, e.Data, e.Bin, e.LogDir(), e.ToolsDir()} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	// The config directory holds credentials, so keep it owner-only where the
	// OS supports it.
	if e.OS != "windows" {
		if err := os.Chmod(e.Config, 0o700); err != nil {
			return err
		}
	}
	return nil
}
