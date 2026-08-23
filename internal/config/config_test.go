package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDefaultMatchesPrototypeBehaviour(t *testing.T) {
	// These values are what the macOS prototype runs today. Changing them
	// silently would alter a working machine on upgrade.
	d := Default()
	if d.ActiveModel != "qwen3.5-9b" {
		t.Errorf("ActiveModel = %q", d.ActiveModel)
	}
	if d.Inference.Port != 11234 {
		t.Errorf("inference port = %d, want 11234", d.Inference.Port)
	}
	if d.Inference.Context != 131072 {
		t.Errorf("context = %d, want 131072", d.Inference.Context)
	}
	if d.Inference.Output != 16384 {
		t.Errorf("output = %d, want 16384", d.Inference.Output)
	}
	if d.Inference.Host != "127.0.0.1" {
		t.Errorf("inference host = %q, want loopback", d.Inference.Host)
	}
	if d.Web.Port != 4096 || d.Web.Host != "0.0.0.0" || d.Web.Username != "opencode" {
		t.Errorf("web defaults = %+v", d.Web)
	}
	if !d.Tools.WebSearch || d.Tools.BrowserAutomation {
		t.Errorf("tool defaults = %+v", d.Tools)
	}
	// mlx-serve warms eagerly at boot and the prototype never passed
	// --idle-evict-secs, so the model stayed resident. Keep that.
	if !d.Inference.KeepInRAM || d.Inference.IdleUnloadMinutes != 0 {
		t.Errorf("lifecycle defaults = %+v", d.Inference)
	}
	if err := d.Validate(); err != nil {
		t.Fatalf("defaults do not validate: %v", err)
	}
}

func TestLoadMissingFileReturnsDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "absent.toml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg != Default() {
		t.Errorf("got %+v, want defaults", cfg)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	want := Default()
	want.ActiveModel = "qwen3.5-9b-compact"
	want.Inference.KeepInRAM = false
	want.Inference.IdleUnloadMinutes = 20
	want.Web.Port = 4444
	want.Tools.BrowserAutomation = true
	want.Runtime.Acceleration = AccelerationVulkan

	if err := Save(path, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != want {
		t.Errorf("round trip: got %+v want %+v", got, want)
	}
}

func TestSaveUsesOwnerOnlyPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not meaningful on Windows")
	}
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := Save(path, Default()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("permissions = %o, want 600", perm)
	}
}

func TestNormalizeFillsBlanksFromPartialFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("active_model = \"custom\"\n\n[web]\nport = 5000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ActiveModel != "custom" || cfg.Web.Port != 5000 {
		t.Errorf("explicit values lost: %+v", cfg)
	}
	if cfg.Inference.Port != 11234 || cfg.Inference.Context != 131072 || cfg.Web.Username != "opencode" {
		t.Errorf("defaults not applied: %+v", cfg)
	}
}

func TestKeepInRAMDisablesIdleUnload(t *testing.T) {
	cfg := Default()
	cfg.Inference.KeepInRAM = true
	cfg.Inference.IdleUnloadMinutes = 30
	cfg.Normalize()

	if cfg.Inference.IdleUnloadMinutes != 0 {
		t.Errorf("idle unload = %d, want 0 when keeping model in RAM", cfg.Inference.IdleUnloadMinutes)
	}
	if cfg.IdleUnloadSeconds() != 0 {
		t.Errorf("IdleUnloadSeconds = %d, want 0", cfg.IdleUnloadSeconds())
	}
}

func TestIdleUnloadSeconds(t *testing.T) {
	cfg := Default()
	cfg.Inference.KeepInRAM = false
	cfg.Inference.IdleUnloadMinutes = 15
	if got := cfg.IdleUnloadSeconds(); got != 900 {
		t.Errorf("IdleUnloadSeconds = %d, want 900", got)
	}
}

func TestValidateRejectsBadConfigurations(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{"same ports", func(c *Config) { c.Web.Port = c.Inference.Port }},
		{"privileged inference port", func(c *Config) { c.Inference.Port = 80 }},
		{"web port too high", func(c *Config) { c.Web.Port = 70000 }},
		{"unknown backend", func(c *Config) { c.Backend = "vllm" }},
		{"unknown acceleration", func(c *Config) { c.Runtime.Acceleration = "tpu" }},
		{"context too small", func(c *Config) { c.Inference.Context = 128 }},
		{"output exceeds context", func(c *Config) { c.Inference.Output = 200000; c.Inference.Context = 131072 }},
		{"idle unload too long", func(c *Config) { c.Inference.KeepInRAM = false; c.Inference.IdleUnloadMinutes = 5000 }},
		{"empty username", func(c *Config) { c.Web.Username = "" }},
		{"empty model", func(c *Config) { c.ActiveModel = "" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Default()
			tt.mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Errorf("expected validation failure for %+v", cfg)
			}
		})
	}
}

func TestWebExposedBeyondLoopback(t *testing.T) {
	tests := map[string]bool{
		"127.0.0.1": false,
		"localhost": false,
		"::1":       false,
		"0.0.0.0":   true,
		"10.0.0.5":  true,
	}
	for host, want := range tests {
		cfg := Default()
		cfg.Web.Host = host
		if got := cfg.WebExposedBeyondLoopback(); got != want {
			t.Errorf("host %q: exposed = %v, want %v", host, got, want)
		}
	}
}
