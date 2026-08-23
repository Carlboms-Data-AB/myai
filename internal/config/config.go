// Package config defines MyAI's persisted settings. It is pure data plus
// validation: it never prints, prompts or touches services, so both the
// terminal UI and any future graphical frontend can share it.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// Backend selection values for Config.Backend.
const (
	// BackendAuto lets MyAI pick the right backend for the host platform.
	BackendAuto = "auto"
	// BackendMLXServe is mlx-serve, used on Apple Silicon.
	BackendMLXServe = "mlx-serve"
	// BackendLlamaCPP is llama.cpp's llama-server, used on Windows and Linux.
	BackendLlamaCPP = "llama.cpp"
)

// DefaultModel is the logical model MyAI installs when nothing is configured.
const DefaultModel = "qwen3.5-9b"

// Acceleration values for Runtime.Acceleration. They select which llama.cpp
// build MyAI installs; the MLX backend always uses Metal.
const (
	// AccelerationAuto picks the best build MyAI can verify actually runs.
	AccelerationAuto = "auto"
	// AccelerationCPU is the portable build with no GPU requirement.
	AccelerationCPU = "cpu"
	// AccelerationVulkan works across NVIDIA, AMD and Intel GPUs.
	AccelerationVulkan = "vulkan"
	// AccelerationCUDA is the NVIDIA-specific build.
	AccelerationCUDA = "cuda"
)

// Config is the complete MyAI configuration.
type Config struct {
	// ActiveModel is a catalog id such as "qwen3.5-9b", or a direct model
	// reference for advanced use.
	ActiveModel string `toml:"active_model"`
	// Backend is BackendAuto unless the user pins a specific backend.
	Backend string `toml:"backend"`

	Inference Inference `toml:"inference"`
	Runtime   Runtime   `toml:"runtime"`
	Web       Web       `toml:"web"`
	Tools     Tools     `toml:"tools"`
}

// Runtime configures how the inference backend is built and installed.
type Runtime struct {
	// Acceleration selects the llama.cpp build. It has no effect on Apple
	// Silicon, where MLX always uses Metal.
	Acceleration string `toml:"acceleration"`
}

// Inference configures the local model server.
type Inference struct {
	// Host is the bind address for the inference API. It stays on loopback
	// unless deliberately changed.
	Host string `toml:"host"`
	// Port is the inference API port.
	Port int `toml:"port"`
	// Context is the context window advertised to OpenCode, in tokens.
	Context int `toml:"context"`
	// Output caps generated tokens per response.
	Output int `toml:"output"`
	// KeepInRAM warms the active model when the backend starts and keeps it
	// resident. Idle unloading is disabled while this is true. This is the
	// default because it is what the macOS prototype effectively did:
	// mlx-serve warms eagerly at boot unless told not to.
	KeepInRAM bool `toml:"keep_in_ram"`
	// IdleUnloadMinutes unloads the model after this many idle minutes.
	// Zero means never unload. Ignored when KeepInRAM is true.
	IdleUnloadMinutes int `toml:"idle_unload_minutes"`
}

// Web configures the OpenCode Web service.
type Web struct {
	// Enabled controls whether the OpenCode Web background service runs.
	Enabled bool `toml:"enabled"`
	// Host is the bind address. Anything other than loopback requires a
	// password, which MyAI generates.
	Host string `toml:"host"`
	// Port is the Web UI port.
	Port int `toml:"port"`
	// Username is the OpenCode Web basic-auth user.
	Username string `toml:"username"`
}

// Tools configures optional agent capabilities that reach outside the machine.
type Tools struct {
	// WebSearch allows OpenCode's websearch and webfetch tools.
	WebSearch bool `toml:"web_search"`
	// BrowserAutomation enables the optional ego-browser skill.
	BrowserAutomation bool `toml:"browser_automation"`
}

// Default returns the configuration MyAI uses on a fresh install. The values
// match the behaviour of the macOS prototype so an existing machine keeps
// working after migration.
func Default() Config {
	return Config{
		ActiveModel: DefaultModel,
		Backend:     BackendAuto,
		Inference: Inference{
			Host:              "127.0.0.1",
			Port:              11234,
			Context:           131072,
			Output:            16384,
			KeepInRAM:         true,
			IdleUnloadMinutes: 0,
		},
		Runtime: Runtime{
			Acceleration: AccelerationAuto,
		},
		Web: Web{
			Enabled:  true,
			Host:     "0.0.0.0",
			Port:     4096,
			Username: "opencode",
		},
		Tools: Tools{
			// Off by default: the point of MyAI is that a session stays on
			// the machine, and web search would send queries to a third party.
			WebSearch:         false,
			BrowserAutomation: false,
		},
	}
}

// Load reads a config file, filling anything absent with defaults. A missing
// file is not an error: it yields the defaults, which makes first run and
// upgrade behave identically.
func Load(path string) (Config, error) {
	cfg := Default()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return Default(), fmt.Errorf("parse %s: %w", path, err)
	}
	cfg.Normalize()
	return cfg, nil
}

// Save writes the config atomically with owner-only permissions.
func Save(path string, cfg Config) error {
	cfg.Normalize()
	if err := cfg.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.toml")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())

	enc := toml.NewEncoder(tmp)
	if err := enc.Encode(cfg); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Normalize fills in blank fields and clamps contradictory combinations.
func (c *Config) Normalize() {
	d := Default()
	if strings.TrimSpace(c.ActiveModel) == "" {
		c.ActiveModel = d.ActiveModel
	}
	if strings.TrimSpace(c.Backend) == "" {
		c.Backend = d.Backend
	}
	if strings.TrimSpace(c.Inference.Host) == "" {
		c.Inference.Host = d.Inference.Host
	}
	if c.Inference.Port == 0 {
		c.Inference.Port = d.Inference.Port
	}
	if c.Inference.Context == 0 {
		c.Inference.Context = d.Inference.Context
	}
	if c.Inference.Output == 0 {
		c.Inference.Output = d.Inference.Output
	}
	if strings.TrimSpace(c.Runtime.Acceleration) == "" {
		c.Runtime.Acceleration = d.Runtime.Acceleration
	}
	if strings.TrimSpace(c.Web.Host) == "" {
		c.Web.Host = d.Web.Host
	}
	if c.Web.Port == 0 {
		c.Web.Port = d.Web.Port
	}
	if strings.TrimSpace(c.Web.Username) == "" {
		c.Web.Username = d.Web.Username
	}
	if c.Inference.IdleUnloadMinutes < 0 {
		c.Inference.IdleUnloadMinutes = 0
	}
	// Keeping the model resident and unloading it on idle are mutually
	// exclusive; the explicit keep-in-RAM choice wins.
	if c.Inference.KeepInRAM {
		c.Inference.IdleUnloadMinutes = 0
	}
}

// Validate reports why a configuration cannot be applied.
func (c Config) Validate() error {
	if strings.TrimSpace(c.ActiveModel) == "" {
		return errors.New("no active model configured")
	}
	switch c.Backend {
	case BackendAuto, BackendMLXServe, BackendLlamaCPP:
	default:
		return fmt.Errorf("unknown backend %q", c.Backend)
	}
	switch c.Runtime.Acceleration {
	case AccelerationAuto, AccelerationCPU, AccelerationVulkan, AccelerationCUDA:
	default:
		return fmt.Errorf("unknown acceleration %q", c.Runtime.Acceleration)
	}
	if err := validatePort("inference port", c.Inference.Port); err != nil {
		return err
	}
	if err := validatePort("web UI port", c.Web.Port); err != nil {
		return err
	}
	if c.Inference.Port == c.Web.Port {
		return errors.New("inference port and web UI port must differ")
	}
	if c.Inference.Context < 4096 || c.Inference.Context > 1048576 {
		return fmt.Errorf("context %d out of range 4096-1048576", c.Inference.Context)
	}
	if c.Inference.Output < 256 || c.Inference.Output > 262144 {
		return fmt.Errorf("output tokens %d out of range 256-262144", c.Inference.Output)
	}
	if c.Inference.Output >= c.Inference.Context {
		return errors.New("output tokens must be smaller than the context size")
	}
	if c.Inference.IdleUnloadMinutes < 0 || c.Inference.IdleUnloadMinutes > 1440 {
		return fmt.Errorf("idle unload %d out of range 0-1440 minutes", c.Inference.IdleUnloadMinutes)
	}
	if strings.TrimSpace(c.Web.Username) == "" {
		return errors.New("web UI username must not be empty")
	}
	return nil
}

func validatePort(label string, port int) error {
	if port < 1024 || port > 65535 {
		return fmt.Errorf("%s %d out of range 1024-65535", label, port)
	}
	return nil
}

// WebExposedBeyondLoopback reports whether the Web UI is reachable from other
// machines, which is what makes a password mandatory.
func (c Config) WebExposedBeyondLoopback() bool {
	switch c.Web.Host {
	case "127.0.0.1", "::1", "localhost":
		return false
	}
	return true
}

// IdleUnloadSeconds converts the configured idle window for backend flags.
// Zero means the backend must not unload the model.
func (c Config) IdleUnloadSeconds() int {
	if c.Inference.KeepInRAM {
		return 0
	}
	return c.Inference.IdleUnloadMinutes * 60
}
