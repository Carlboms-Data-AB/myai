package opencode

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Carlboms-Data-AB/myai/internal/config"
	"github.com/Carlboms-Data-AB/myai/internal/run"
	"github.com/Carlboms-Data-AB/myai/internal/secrets"
	"github.com/Carlboms-Data-AB/myai/internal/service"
)

func sampleInput() ConfigInput {
	return ConfigInput{
		BaseURL:   "http://127.0.0.1:11234",
		ModelID:   "mlx-community/Qwen3.5-9B-6bit",
		ModelName: "Qwen3.5 9B (6-bit)",
		Limits:    ModelLimits{Context: 131072, Output: 16384},
		WebTools:  true,
	}
}

func TestRenderConfigPinsToTheLocalProvider(t *testing.T) {
	body, err := RenderConfig(sampleInput())
	if err != nil {
		t.Fatal(err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("generated config is not valid JSON: %v\n%s", err, body)
	}

	if parsed["model"] != "myai/mlx-community/Qwen3.5-9B-6bit" {
		t.Errorf("model = %v", parsed["model"])
	}
	providers, _ := parsed["enabled_providers"].([]any)
	if len(providers) != 1 || providers[0] != ProviderID {
		t.Errorf("enabled_providers = %v; only the local provider may be enabled", providers)
	}

	provider := parsed["provider"].(map[string]any)[ProviderID].(map[string]any)
	if provider["npm"] != "@ai-sdk/openai-compatible" {
		t.Errorf("npm = %v", provider["npm"])
	}
	options := provider["options"].(map[string]any)
	if options["baseURL"] != "http://127.0.0.1:11234/v1" {
		t.Errorf("baseURL = %v", options["baseURL"])
	}

	limits := provider["models"].(map[string]any)["mlx-community/Qwen3.5-9B-6bit"].(map[string]any)["limit"].(map[string]any)
	if limits["context"].(float64) != 131072 || limits["output"].(float64) != 16384 {
		t.Errorf("limits = %v", limits)
	}
}

func TestRenderConfigNeverAllowsACloudProvider(t *testing.T) {
	body, err := RenderConfig(sampleInput())
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, forbidden := range []string{"anthropic", "openai\"", "google", "api.openai.com", "bedrock"} {
		if strings.Contains(strings.ToLower(text), forbidden) {
			t.Errorf("generated config mentions %q:\n%s", forbidden, text)
		}
	}
}

func TestRenderConfigWebToolPermission(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		in := sampleInput()
		in.WebTools = enabled
		body, err := RenderConfig(in)
		if err != nil {
			t.Fatal(err)
		}
		var parsed struct {
			Permission map[string]string `json:"permission"`
		}
		json.Unmarshal(body, &parsed)

		want := "deny"
		if enabled {
			want = "allow"
		}
		if parsed.Permission["websearch"] != want || parsed.Permission["webfetch"] != want {
			t.Errorf("web tools %v: permission = %v, want %q", enabled, parsed.Permission, want)
		}
	}
}

func TestRenderConfigRejectsIncompleteInput(t *testing.T) {
	in := sampleInput()
	in.ModelID = ""
	if _, err := RenderConfig(in); err == nil {
		t.Error("expected an error without a model id")
	}
	in = sampleInput()
	in.BaseURL = ""
	if _, err := RenderConfig(in); err == nil {
		t.Error("expected an error without a base URL")
	}
}

func TestWriteConfigUsesOwnerOnlyPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.json")
	if err := WriteConfig(path, sampleInput()); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("permissions = %o, want 600", perm)
	}
}

func TestValidateConfigAcceptsWhatWeWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.json")
	in := sampleInput()
	if err := WriteConfig(path, in); err != nil {
		t.Fatal(err)
	}
	if err := ValidateConfig(path, in.ModelID, in.BaseURL); err != nil {
		t.Errorf("ValidateConfig: %v", err)
	}
}

func TestValidateConfigRejectsDrift(t *testing.T) {
	dir := t.TempDir()
	in := sampleInput()

	tests := []struct {
		name    string
		body    string
		modelID string
	}{
		{"missing file", "", in.ModelID},
		{"not json", "{oh dear", in.ModelID},
		{"wrong model", `{"model":"myai/other","enabled_providers":["myai"],"provider":{"myai":{"options":{"baseURL":"http://127.0.0.1:11234/v1"},"models":{"other":{}}}}}`, in.ModelID},
		{"extra provider", `{"model":"myai/m","enabled_providers":["myai","anthropic"],"provider":{"myai":{"options":{"baseURL":"http://127.0.0.1:11234/v1"},"models":{"m":{}}}}}`, "m"},
		{"wrong base url", `{"model":"myai/m","enabled_providers":["myai"],"provider":{"myai":{"options":{"baseURL":"https://api.openai.com/v1"},"models":{"m":{}}}}}`, "m"},
		{"model not defined", `{"model":"myai/m","enabled_providers":["myai"],"provider":{"myai":{"options":{"baseURL":"http://127.0.0.1:11234/v1"},"models":{}}}}`, "m"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(dir, tt.name+".json")
			if tt.body != "" {
				if err := os.WriteFile(path, []byte(tt.body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := ValidateConfig(path, tt.modelID, in.BaseURL); err == nil {
				t.Error("expected validation to fail")
			}
		})
	}
}

func TestEnvSetsBothConfigVariables(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.json")
	if err := WriteConfig(path, sampleInput()); err != nil {
		t.Fatal(err)
	}
	o := New(t.TempDir(), run.NewFake(), "darwin", "arm64")

	env, err := o.Env(path, true)
	if err != nil {
		t.Fatal(err)
	}
	// The inline copy is what stops a repository's own opencode.json from
	// overriding the provider.
	if env["OPENCODE_CONFIG"] != path {
		t.Errorf("OPENCODE_CONFIG = %q", env["OPENCODE_CONFIG"])
	}
	if !strings.Contains(env["OPENCODE_CONFIG_CONTENT"], ProviderID) {
		t.Errorf("OPENCODE_CONFIG_CONTENT = %q", env["OPENCODE_CONFIG_CONTENT"])
	}
	if env["OPENCODE_ENABLE_EXA"] != "1" {
		t.Errorf("OPENCODE_ENABLE_EXA = %q", env["OPENCODE_ENABLE_EXA"])
	}
}

func TestEnvOmitsExaWhenWebToolsAreOff(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.json")
	if err := WriteConfig(path, sampleInput()); err != nil {
		t.Fatal(err)
	}
	o := New(t.TempDir(), run.NewFake(), "darwin", "arm64")

	env, err := o.Env(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := env["OPENCODE_ENABLE_EXA"]; ok {
		t.Error("web search is off, so Exa must not be enabled")
	}
}

func TestWebArgs(t *testing.T) {
	cfg := config.Default()
	got := strings.Join(WebArgs(cfg), " ")
	if got != "web --hostname 0.0.0.0 --port 4096" {
		t.Errorf("WebArgs = %q", got)
	}
}

func TestWebServiceSpecKeepsCredentialsOutOfTheServiceDefinition(t *testing.T) {
	spec, err := WebServiceSpec(WebParams{
		Config:     config.Default(),
		Name:       service.Name("darwin", service.RoleWeb),
		Supervisor: "/Users/t/.local/bin/myai",
		StdoutLog:  "/tmp/web.log",
	})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Exec != "/Users/t/.local/bin/myai" {
		t.Errorf("Exec = %q, want the MyAI supervisor", spec.Exec)
	}
	for _, pair := range spec.EnvPairs() {
		if strings.Contains(pair, "PASSWORD") {
			t.Errorf("the service definition must not carry credentials: %q", pair)
		}
	}
}

func TestWebServiceSpecNeedsSupervisor(t *testing.T) {
	if _, err := WebServiceSpec(WebParams{Config: config.Default(), Name: "x"}); err == nil {
		t.Error("expected an error without the MyAI executable")
	}
}

func TestWebEnvRequiresPasswordWhenExposed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.json")
	if err := WriteConfig(path, sampleInput()); err != nil {
		t.Fatal(err)
	}
	o := New(t.TempDir(), run.NewFake(), "darwin", "arm64")

	cfg := config.Default() // binds 0.0.0.0
	if _, err := o.WebEnv(path, cfg, secrets.Credentials{}); err == nil {
		t.Error("a network-exposed Web UI must not start without a password")
	}

	creds := secrets.Credentials{Username: "opencode", Password: "s3cret"}
	env, err := o.WebEnv(path, cfg, creds)
	if err != nil {
		t.Fatal(err)
	}
	if env["OPENCODE_SERVER_USERNAME"] != "opencode" || env["OPENCODE_SERVER_PASSWORD"] != "s3cret" {
		t.Errorf("credentials not passed to OpenCode: %v", env)
	}
}

func TestWebEnvAllowsLoopbackWithoutPassword(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.json")
	if err := WriteConfig(path, sampleInput()); err != nil {
		t.Fatal(err)
	}
	o := New(t.TempDir(), run.NewFake(), "darwin", "arm64")

	cfg := config.Default()
	cfg.Web.Host = "127.0.0.1"
	if _, err := o.WebEnv(path, cfg, secrets.Credentials{}); err != nil {
		t.Errorf("a loopback-only Web UI may run without a password: %v", err)
	}
}

func TestAssetNamePerPlatform(t *testing.T) {
	tests := map[[2]string]string{
		{"darwin", "arm64"}:  "opencode-darwin-arm64.zip",
		{"windows", "amd64"}: "opencode-windows-x64.zip",
		{"windows", "arm64"}: "opencode-windows-arm64.zip",
		{"linux", "amd64"}:   "opencode-linux-x64.tar.gz",
		{"linux", "arm64"}:   "opencode-linux-arm64.tar.gz",
	}
	for p, want := range tests {
		got, err := AssetName(p[0], p[1])
		if err != nil {
			t.Fatalf("%s/%s: %v", p[0], p[1], err)
		}
		if got != want {
			t.Errorf("%s/%s = %q, want %q", p[0], p[1], got, want)
		}
	}
	if _, err := AssetName("plan9", "amd64"); err == nil {
		t.Error("expected an error for an unsupported platform")
	}
}

func TestDetectReportsVersion(t *testing.T) {
	fake := run.NewFake().Respond("--version", "1.18.21\n")
	got := New(t.TempDir(), fake, "darwin", "arm64").Detect(context.Background())
	if !got.Installed || got.Version != "1.18.21" {
		t.Errorf("Detect = %+v", got)
	}
}

func TestDetectFindsMyAIInstalledCopy(t *testing.T) {
	dir := t.TempDir()
	fake := run.NewFake().Absent(Executable).Respond("--version", "1.18.21\n")
	o := New(dir, fake, "linux", "amd64")

	if err := os.MkdirAll(o.ToolDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(o.ToolDir, Executable), []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := o.Detect(context.Background()); !got.Installed {
		t.Error("the MyAI-installed OpenCode should be found")
	}
}

func TestPathFailsWhenAbsent(t *testing.T) {
	fake := run.NewFake().Absent(Executable)
	if _, err := New(t.TempDir(), fake, "linux", "amd64").Path(context.Background()); err == nil {
		t.Error("expected an error when OpenCode is missing")
	}
}

func TestWebURLs(t *testing.T) {
	cfg := config.Default()
	if got := WebURL("100.64.0.5", cfg); got != "http://100.64.0.5:4096" {
		t.Errorf("WebURL = %q", got)
	}
	if got := LocalWebURL(cfg); got != "http://127.0.0.1:4096" {
		t.Errorf("LocalWebURL = %q", got)
	}
}
