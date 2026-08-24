// Package opencode integrates the OpenCode coding agent. MyAI keeps its own
// OpenCode configuration, separate from the user's personal one, and pins it
// to the local model so a session cannot quietly fall back to a cloud
// provider.
package opencode

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Carlboms-Data-AB/myai/internal/run"
)

// Executable is the OpenCode command name.
const Executable = "opencode"

// ProviderID is the provider key MyAI writes into the managed OpenCode
// configuration. It is deliberately the same on every platform, so the model
// string does not change when the backend does.
const ProviderID = "myai"

// Info describes an installed OpenCode.
type Info struct {
	Installed bool
	Path      string
	Version   string
}

// Summary renders OpenCode's state for status output.
func (i Info) Summary() string {
	if !i.Installed {
		return "not installed"
	}
	if i.Version != "" {
		return i.Version
	}
	return i.Path
}

// OpenCode locates and runs the agent.
type OpenCode struct {
	// ToolDir is where MyAI installs OpenCode when it is not already present.
	ToolDir string
	// Runner executes OpenCode.
	Runner run.Runner
	// GOOS and GOARCH identify the platform.
	GOOS, GOARCH string
}

// New returns an OpenCode integration.
func New(toolDir string, runner run.Runner, goos, goarch string) *OpenCode {
	return &OpenCode{ToolDir: filepath.Join(toolDir, "opencode"), Runner: runner, GOOS: goos, GOARCH: goarch}
}

// Detect reports whether OpenCode is available, preferring a copy on PATH so
// an existing installation keeps being used.
func (o *OpenCode) Detect(ctx context.Context) Info {
	var info Info

	if path, err := o.Runner.Look(Executable); err == nil {
		info.Installed, info.Path = true, path
	} else if path, err := o.localExecutable(); err == nil {
		info.Installed, info.Path = true, path
	} else {
		return info
	}

	if res, err := o.Runner.Run(ctx, run.Spec{Name: info.Path, Args: []string{"--version"}}); err == nil {
		info.Version = strings.TrimSpace(firstLine(res.Output))
	}
	return info
}

func (o *OpenCode) localExecutable() (string, error) {
	name := Executable
	if o.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(o.ToolDir, name)
	if _, err := os.Stat(path); err != nil {
		return "", err
	}
	return path, nil
}

// Path returns the OpenCode executable, or an error when it is absent.
func (o *OpenCode) Path(ctx context.Context) (string, error) {
	info := o.Detect(ctx)
	if !info.Installed {
		return "", fmt.Errorf("OpenCode is not installed")
	}
	return info.Path, nil
}

// Env returns the environment that pins an OpenCode session to MyAI's managed
// configuration.
//
// Both OPENCODE_CONFIG and OPENCODE_CONFIG_CONTENT are set on purpose.
// OpenCode merges configuration from several places: the custom path is read
// before a project's own opencode.json, while the inline content is read
// after it. Setting both means a repository cannot override the provider and
// send work to a cloud model.
func (o *OpenCode) Env(configPath string, webTools bool) (map[string]string, error) {
	content, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("read managed OpenCode config: %w", err)
	}
	env := map[string]string{
		"OPENCODE_CONFIG":         configPath,
		"OPENCODE_CONFIG_CONTENT": string(content),
	}
	if webTools {
		env["OPENCODE_ENABLE_EXA"] = "1"
	}
	return env, nil
}

// Launch starts the OpenCode terminal interface in a directory, attached to
// the current terminal.
func (o *OpenCode) Launch(ctx context.Context, dir, configPath string, webTools bool, args []string) error {
	path, err := o.Path(ctx)
	if err != nil {
		return err
	}
	env, err := o.Env(configPath, webTools)
	if err != nil {
		return err
	}

	pairs := make([]string, 0, len(env))
	for k, v := range env {
		pairs = append(pairs, k+"="+v)
	}
	_, err = o.Runner.Run(ctx, run.Spec{
		Name:        path,
		Args:        args,
		Env:         pairs,
		Dir:         dir,
		Interactive: true,
	})
	return err
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}

// --- managed configuration ---

// ModelLimits are the context and output limits advertised to OpenCode.
type ModelLimits struct {
	Context int
	Output  int
}

// ConfigInput is everything needed to render the managed OpenCode config.
type ConfigInput struct {
	// BaseURL is the local inference API root, without the /v1 suffix.
	BaseURL string
	// ModelID is the identifier the inference server advertises.
	ModelID string
	// ModelName is the human-readable model name.
	ModelName string
	// Limits are the context and output token limits.
	Limits ModelLimits
	// WebTools allows the websearch and webfetch tools.
	WebTools bool
}

// RenderConfig produces the managed OpenCode configuration.
func RenderConfig(in ConfigInput) ([]byte, error) {
	if strings.TrimSpace(in.ModelID) == "" {
		return nil, fmt.Errorf("no model id for the OpenCode configuration")
	}
	if strings.TrimSpace(in.BaseURL) == "" {
		return nil, fmt.Errorf("no inference API address for the OpenCode configuration")
	}

	permission := "deny"
	if in.WebTools {
		permission = "allow"
	}
	name := in.ModelName
	if name == "" {
		name = in.ModelID
	}

	cfg := map[string]any{
		"$schema": "https://opencode.ai/config.json",
		"model":   ProviderID + "/" + in.ModelID,
		// Allowing only this provider is what stops OpenCode from reaching for
		// a cloud model when other credentials happen to be present.
		"enabled_providers": []string{ProviderID},
		"provider": map[string]any{
			ProviderID: map[string]any{
				"npm":     "@ai-sdk/openai-compatible",
				"name":    "MyAI (local)",
				"options": map[string]any{"baseURL": strings.TrimRight(in.BaseURL, "/") + "/v1"},
				"models": map[string]any{
					in.ModelID: map[string]any{
						"name": name,
						"limit": map[string]any{
							"context": in.Limits.Context,
							"output":  in.Limits.Output,
						},
					},
				},
			},
		},
		"permission": map[string]any{
			"websearch": permission,
			"webfetch":  permission,
		},
	}

	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// WriteConfig renders and saves the managed configuration.
func WriteConfig(path string, in ConfigInput) error {
	body, err := RenderConfig(in)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, body, 0o600)
}

// AdvertisedContext reports the context window the managed configuration tells
// OpenCode a model has.
func AdvertisedContext(path, modelID string) (int, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	var parsed struct {
		Provider map[string]struct {
			Models map[string]struct {
				Limit struct {
					Context int `json:"context"`
				} `json:"limit"`
			} `json:"models"`
		} `json:"provider"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return 0, err
	}
	model, ok := parsed.Provider[ProviderID].Models[modelID]
	if !ok {
		return 0, fmt.Errorf("managed OpenCode config does not define model %q", modelID)
	}
	return model.Limit.Context, nil
}

// ValidateConfig checks that a managed configuration is present, parseable and
// still pinned to the expected local model.
func ValidateConfig(path, modelID, baseURL string) error {
	body, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("managed OpenCode config is missing: %w", err)
	}

	var parsed struct {
		Model            string   `json:"model"`
		EnabledProviders []string `json:"enabled_providers"`
		Provider         map[string]struct {
			Options struct {
				BaseURL string `json:"baseURL"`
			} `json:"options"`
			Models map[string]json.RawMessage `json:"models"`
		} `json:"provider"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return fmt.Errorf("managed OpenCode config is not valid JSON: %w", err)
	}

	if want := ProviderID + "/" + modelID; parsed.Model != want {
		return fmt.Errorf("managed OpenCode config selects %q, expected %q", parsed.Model, want)
	}
	if len(parsed.EnabledProviders) != 1 || parsed.EnabledProviders[0] != ProviderID {
		return fmt.Errorf("managed OpenCode config does not restrict providers to %q", ProviderID)
	}
	provider, ok := parsed.Provider[ProviderID]
	if !ok {
		return fmt.Errorf("managed OpenCode config has no %q provider", ProviderID)
	}
	if want := strings.TrimRight(baseURL, "/") + "/v1"; provider.Options.BaseURL != want {
		return fmt.Errorf("managed OpenCode config points at %q, expected %q", provider.Options.BaseURL, want)
	}
	if _, ok := provider.Models[modelID]; !ok {
		return fmt.Errorf("managed OpenCode config does not define model %q", modelID)
	}
	return nil
}
