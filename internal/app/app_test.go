package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Carlboms-Data-AB/myai/internal/config"
	"github.com/Carlboms-Data-AB/myai/internal/paths"
	"github.com/Carlboms-Data-AB/myai/internal/platform"
	"github.com/Carlboms-Data-AB/myai/internal/progress"
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
		account, err := a.serviceAccount(context.Background())
		if err != nil {
			t.Errorf("%s: %v", goos, err)
		}
		if account.User != "" {
			t.Errorf("%s: unexpected account %+v", goos, account)
		}
	}

	a, fake, _ := newTestApp(t, "windows", "amd64")
	if !a.Services().NeedsAccount() {
		t.Error("Windows services must declare that they need an account")
	}
	// No service exists yet, so one has to be created.
	fake.DefaultErr = errors.New("service does not exist")

	// Without an interactive session there is nobody to ask, so this must
	// fail rather than silently install as LocalSystem.
	if _, err := a.serviceAccount(context.Background()); err == nil {
		t.Error("expected an error when no password can be collected")
	}
}

func TestExistingWindowsServicesDoNotAskForThePasswordAgain(t *testing.T) {
	// Apply runs on every settings change. Prompting each time would make
	// changing the context size ask for a Windows password.
	a, fake, _ := newTestApp(t, "windows", "amd64")
	fake.Respond("status", "SERVICE_RUNNING")

	account, err := a.serviceAccount(context.Background())
	if err != nil {
		t.Fatalf("serviceAccount: %v", err)
	}
	if account.User != "" {
		t.Errorf("account = %+v, want the existing one left alone", account)
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
	if err := a.Update(func(c *config.Config) { c.Web.Enabled = true }); err != nil {
		t.Fatal(err)
	}

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
	if err := a.Update(func(c *config.Config) { c.Web.Enabled = true }); err != nil {
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

func TestStatusNeverAsksTheModelToGenerate(t *testing.T) {
	// Probing whether a model is resident would itself load it. Reporting
	// status must not move gigabytes into memory as a side effect.
	var completions, other int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "chat/completions") {
			completions++
			w.Write([]byte(`{"choices":[{"message":{"content":"."}}]}`))
			return
		}
		other++
		w.Write([]byte(`{"data":[{"id":"mlx-community/Qwen3.5-9B-6bit"}]}`))
	}))
	defer srv.Close()

	port, err := strconv.Atoi(strings.TrimPrefix(srv.URL, "http://127.0.0.1:"))
	if err != nil {
		t.Skipf("unexpected test server address %q", srv.URL)
	}

	a, _, _ := newTestApp(t, "darwin", "arm64")
	if err := a.Update(func(c *config.Config) { c.Inference.Port = port }); err != nil {
		t.Fatal(err)
	}

	status := a.Status(context.Background())

	if completions != 0 {
		t.Errorf("status sent %d completion request(s); it must not load the model", completions)
	}
	if !status.APIReachable {
		t.Errorf("the API should have been reached via a harmless endpoint (%d call(s))", other)
	}
}

func TestResidencyIsHonestAboutWhatItKnows(t *testing.T) {
	tests := []struct {
		name   string
		status Status
		want   string
	}{
		{"no model", Status{}, "not installed"},
		{"api down", Status{ModelInstalled: true}, "unknown, the inference API is not answering"},
		{"kept resident", Status{ModelInstalled: true, APIReachable: true, KeepInRAM: true}, "set to stay resident"},
		{"on demand", Status{ModelInstalled: true, APIReachable: true}, "loaded on demand"},
	}
	for _, tt := range tests {
		if got := residency(tt.status); got != tt.want {
			t.Errorf("%s: residency = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestRestartSaysWhenNothingIsInstalled(t *testing.T) {
	a, fake, _ := newTestApp(t, "linux", "amd64")
	fake.DefaultErr = errors.New("Unit myai.service could not be found.")

	err := a.Restart(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "Install / update") {
		t.Errorf("err = %v, want it to point at the install step", err)
	}
}

func TestApplyWarnsWhenTheModelIsNotDownloaded(t *testing.T) {
	// Otherwise the only symptom is a service that will not stay up.
	home := t.TempDir()
	env := paths.Resolve("darwin", "arm64", home, nil)
	fake := run.NewFake()

	var warnings []string
	rep := recordingReporter{warn: func(m string) { warnings = append(warnings, m) }}

	a, err := app_New(&env, fake, rep, filepath.Join(home, "myai"))
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Apply(context.Background()); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	found := false
	for _, w := range warnings {
		if strings.Contains(w, "not downloaded") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a warning about the missing model, got %v", warnings)
	}
}

// recordingReporter captures warnings and ignores the rest.
type recordingReporter struct{ warn func(string) }

func (recordingReporter) Step(string)                   {}
func (recordingReporter) Info(string)                   {}
func (r recordingReporter) Warn(m string)               { r.warn(m) }
func (recordingReporter) Check(string, bool, string)    {}
func (recordingReporter) Download(string, int64, int64) {}

func app_New(env *paths.Env, runner run.Runner, rep progress.Reporter, exe string) (*App, error) {
	return New(Options{
		Env:          env,
		Host:         &platform.Host{OS: "darwin", Arch: "arm64", User: "tester"},
		Runner:       runner,
		Reporter:     rep,
		Executable:   exe,
		ReadyTimeout: 10 * time.Millisecond,
	})
}
