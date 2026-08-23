package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Carlboms-Data-AB/myai/internal/config"
	"github.com/Carlboms-Data-AB/myai/internal/paths"
	"github.com/Carlboms-Data-AB/myai/internal/platform"
	"github.com/Carlboms-Data-AB/myai/internal/run"
	"github.com/Carlboms-Data-AB/myai/internal/service"
)

// newTestApp builds an App rooted entirely inside a temporary directory, with
// every external command faked.
func newTestApp(t *testing.T, goos, goarch string) (*App, *run.Fake, paths.Env) {
	t.Helper()

	home := t.TempDir()
	env := paths.Resolve(goos, goarch, home, nil)
	fake := run.NewFake()

	a, err := New(Options{
		Env:        &env,
		Host:       &platform.Host{OS: goos, Arch: goarch, User: "tester"},
		Runner:     fake,
		Executable: filepath.Join(home, "myai-source"),
		// Nothing is really listening in tests, so do not sit through the
		// production wait for the inference API.
		ReadyTimeout: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(a.exe, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	return a, fake, env
}

func TestBackendSelectionPerPlatform(t *testing.T) {
	tests := map[[2]string]string{
		{"darwin", "arm64"}:  config.BackendMLXServe,
		{"windows", "amd64"}: config.BackendLlamaCPP,
		{"windows", "arm64"}: config.BackendLlamaCPP,
		{"linux", "amd64"}:   config.BackendLlamaCPP,
		{"linux", "arm64"}:   config.BackendLlamaCPP,
	}
	for p, want := range tests {
		a, _, _ := newTestApp(t, p[0], p[1])
		if got := a.Backend().ID(); got != want {
			t.Errorf("%s/%s selected %q, want %q", p[0], p[1], got, want)
		}
	}
}

func TestBackendCanBePinned(t *testing.T) {
	a, _, _ := newTestApp(t, "darwin", "arm64")
	if err := a.Update(func(c *config.Config) { c.Backend = config.BackendLlamaCPP }); err != nil {
		t.Fatal(err)
	}
	if got := a.Backend().ID(); got != config.BackendLlamaCPP {
		t.Errorf("Backend = %q, want the pinned one", got)
	}
}

func TestServiceManagerPerPlatform(t *testing.T) {
	tests := map[string]string{
		"darwin":  service.KindLaunchd,
		"windows": service.KindNSSM,
		"linux":   service.KindSystemd,
	}
	for goos, want := range tests {
		a, _, _ := newTestApp(t, goos, "arm64")
		if got := a.Services().Kind(); got != want {
			t.Errorf("%s uses %q, want %q", goos, got, want)
		}
	}
}

func TestOnlyWindowsNeedsAServiceAccount(t *testing.T) {
	for _, goos := range []string{"darwin", "linux"} {
		a, _, _ := newTestApp(t, goos, "arm64")
		if a.Services().NeedsAccount() {
			t.Errorf("%s should not need a service account", goos)
		}
		account, err := a.serviceAccount()
		if err != nil {
			t.Errorf("%s: %v", goos, err)
		}
		if account.User != "" {
			t.Errorf("%s: unexpected account %+v", goos, account)
		}
	}

	a, _, _ := newTestApp(t, "windows", "amd64")
	if !a.Services().NeedsAccount() {
		t.Error("Windows services must declare that they need an account")
	}
	// Without an interactive session there is nobody to ask, so this must
	// fail rather than silently install as LocalSystem.
	if _, err := a.serviceAccount(); err == nil {
		t.Error("expected an error when no password can be collected")
	}
}

func TestSetConfigRejectsInvalidSettings(t *testing.T) {
	a, _, env := newTestApp(t, "darwin", "arm64")

	cfg := a.Config()
	cfg.Web.Port = cfg.Inference.Port
	if err := a.SetConfig(cfg); err == nil {
		t.Error("expected clashing ports to be rejected")
	}
	if fileExists(env.ConfigFile()) {
		t.Error("an invalid configuration must not be written")
	}
}

func TestUpdatePersistsAndReloads(t *testing.T) {
	a, _, env := newTestApp(t, "linux", "amd64")

	if err := a.Update(func(c *config.Config) { c.Inference.Port = 12345 }); err != nil {
		t.Fatal(err)
	}
	if a.Config().Inference.Port != 12345 {
		t.Errorf("in-memory config not updated: %+v", a.Config())
	}
	reloaded, err := config.Load(env.ConfigFile())
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Inference.Port != 12345 {
		t.Errorf("config not persisted: %+v", reloaded)
	}
}

func TestWriteOpenCodeConfigIsPinnedToTheLocalModel(t *testing.T) {
	a, _, env := newTestApp(t, "darwin", "arm64")
	if err := a.env.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	if err := a.WriteOpenCodeConfig(context.Background()); err != nil {
		t.Fatal(err)
	}

	body, err := os.ReadFile(env.OpenCodeConfigFile())
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if !strings.Contains(text, "mlx-community/Qwen3.5-9B-6bit") {
		t.Errorf("config does not name the active model:\n%s", text)
	}
	if !strings.Contains(text, "http://127.0.0.1:11234/v1") {
		t.Errorf("config does not point at the local API:\n%s", text)
	}
}

func TestOpenCodeConfigDiffersPerPlatformButStaysLocal(t *testing.T) {
	for _, p := range [][2]string{{"darwin", "arm64"}, {"windows", "amd64"}, {"linux", "arm64"}} {
		a, _, env := newTestApp(t, p[0], p[1])
		if err := a.env.EnsureDirs(); err != nil {
			t.Fatal(err)
		}
		if err := a.WriteOpenCodeConfig(context.Background()); err != nil {
			t.Fatalf("%s/%s: %v", p[0], p[1], err)
		}
		body, err := os.ReadFile(env.OpenCodeConfigFile())
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(body), "http://127.0.0.1:11234/v1") {
			t.Errorf("%s/%s must point at the local API:\n%s", p[0], p[1], body)
		}
	}
}

func TestApplyRegeneratesAndRestarts(t *testing.T) {
	a, fake, env := newTestApp(t, "darwin", "arm64")
	fake.Respond("launchctl print", "state = running\npid = 100")

	if err := a.Apply(context.Background()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !fileExists(env.OpenCodeConfigFile()) {
		t.Error("the managed OpenCode config was not written")
	}
	if !fake.Ran("launchctl bootstrap") {
		t.Errorf("services were not installed: %v", fake.CommandLines())
	}
	if !fake.Ran("launchctl kickstart") {
		t.Errorf("services were not restarted: %v", fake.CommandLines())
	}
}

func TestServiceSpecsOmitWebWhenDisabled(t *testing.T) {
	a, _, _ := newTestApp(t, "linux", "amd64")
	if err := a.Update(func(c *config.Config) { c.Web.Enabled = false }); err != nil {
		t.Fatal(err)
	}

	specs, err := a.ServiceSpecs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 1 || specs[0].Role != service.RoleInference {
		t.Errorf("specs = %+v, want the inference service only", specs)
	}
}

func TestWebServiceRunsMyAINotOpenCodeDirectly(t *testing.T) {
	a, _, _ := newTestApp(t, "linux", "amd64")

	specs, err := a.ServiceSpecs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var web *service.Spec
	for i := range specs {
		if specs[i].Role == service.RoleWeb {
			web = &specs[i]
		}
	}
	if web == nil {
		t.Fatal("no web service spec")
	}
	if !strings.Contains(web.Exec, "myai") {
		t.Errorf("Exec = %q, want the MyAI supervisor so credentials stay out of the unit", web.Exec)
	}
	for _, pair := range web.EnvPairs() {
		if strings.Contains(strings.ToUpper(pair), "PASSWORD") {
			t.Errorf("credentials leaked into the service definition: %q", pair)
		}
	}
}

func TestStopLegacyServicesTargetsThePrototypeAgents(t *testing.T) {
	a, fake, _ := newTestApp(t, "darwin", "arm64")
	// Make every legacy agent look loaded.
	fake.Respond("launchctl print", "state = running")
	// The plist files must exist for Status to report them as installed.
	agentDir := filepath.Join(a.env.Home, "Library", "LaunchAgents")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range service.LegacyNames("darwin") {
		if err := os.WriteFile(filepath.Join(agentDir, name+".plist"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	a.StopLegacyServices(context.Background())
	for _, name := range service.LegacyNames("darwin") {
		if !fake.Ran("bootout gui/") || !strings.Contains(strings.Join(fake.CommandLines(), "\n"), name) {
			t.Errorf("legacy agent %q was not stopped: %v", name, fake.CommandLines())
		}
	}
}

func TestCredentialsAreGeneratedAndStable(t *testing.T) {
	a, _, _ := newTestApp(t, "linux", "amd64")
	if err := a.env.EnsureDirs(); err != nil {
		t.Fatal(err)
	}

	first, err := a.Credentials()
	if err != nil {
		t.Fatal(err)
	}
	if !first.Complete() {
		t.Fatal("credentials should be generated on demand")
	}
	second, err := a.Credentials()
	if err != nil {
		t.Fatal(err)
	}
	if second.Password != first.Password {
		t.Error("the Web UI password must not change between calls")
	}
}

func TestStatusCreatesNothing(t *testing.T) {
	a, _, env := newTestApp(t, "darwin", "arm64")

	a.Status(context.Background())

	// Reporting on an uninstalled machine must leave no trace, especially not
	// a credentials file.
	for _, path := range []string{env.Config, env.State, env.Data, env.CredentialsFile()} {
		if _, err := os.Stat(path); err == nil {
			t.Errorf("status created %s", path)
		}
	}
}

func TestWebAccessDoesNotGenerateOnUninstalledMachine(t *testing.T) {
	a, _, env := newTestApp(t, "linux", "amd64")

	access, err := a.WebAccess(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if access.Password != "" {
		t.Error("no password should be generated before MyAI is installed")
	}
	if _, err := os.Stat(env.CredentialsFile()); err == nil {
		t.Error("a credentials file was created by a read-only command")
	}
}

func TestWebAccessGeneratesOnceInstalled(t *testing.T) {
	a, _, _ := newTestApp(t, "linux", "amd64")
	if err := a.env.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	if err := a.SetConfig(a.Config()); err != nil {
		t.Fatal(err)
	}

	access, err := a.WebAccess(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if access.Password == "" || access.Username != "opencode" {
		t.Errorf("access = %+v", access)
	}
}

func TestPinnedBackendResolvesAMatchingModel(t *testing.T) {
	// The Runtime menu lets a Mac pin llama.cpp. The active model, the store
	// and the service arguments all have to agree, or llama-server is handed
	// an MLX repository it cannot load.
	a, _, _ := newTestApp(t, "darwin", "arm64")
	if err := a.Update(func(c *config.Config) { c.Backend = config.BackendLlamaCPP }); err != nil {
		t.Fatal(err)
	}

	model, err := a.ActiveModel()
	if err != nil {
		t.Fatalf("ActiveModel: %v", err)
	}
	if model.Backend() != config.BackendLlamaCPP {
		t.Errorf("model resolved to %q, want llama.cpp", model.Backend())
	}
	if model.Artifact.File == "" {
		t.Error("a llama.cpp model must name a GGUF file")
	}

	store := a.Backend().Store()
	if store.Backend() != config.BackendLlamaCPP {
		t.Errorf("store backend = %q", store.Backend())
	}
	if !strings.HasSuffix(store.PathFor(model), ".gguf") {
		t.Errorf("model path = %q, want a GGUF file", store.PathFor(model))
	}
}

func TestDefaultBackendStillWinsWhenNotPinned(t *testing.T) {
	a, _, _ := newTestApp(t, "darwin", "arm64")

	model, err := a.ActiveModel()
	if err != nil {
		t.Fatal(err)
	}
	if model.Backend() != config.BackendMLXServe {
		t.Errorf("model resolved to %q, want MLX by default on Apple Silicon", model.Backend())
	}
	if model.Artifact.Repo != "mlx-community/Qwen3.5-9B-6bit" {
		t.Errorf("repo = %q", model.Artifact.Repo)
	}
}

func TestModelsViewFollowsThePinnedBackend(t *testing.T) {
	a, _, _ := newTestApp(t, "darwin", "arm64")
	if err := a.Update(func(c *config.Config) { c.Backend = config.BackendLlamaCPP }); err != nil {
		t.Fatal(err)
	}

	view, err := a.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Available) == 0 {
		t.Fatal("no models offered")
	}
	for _, m := range view.Available {
		if m.Backend != config.BackendLlamaCPP {
			t.Errorf("offered model %q uses %q", m.ID, m.Backend)
		}
	}
}
