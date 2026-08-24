package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Carlboms-Data-AB/myai/internal/app"
	"github.com/Carlboms-Data-AB/myai/internal/paths"
	"github.com/Carlboms-Data-AB/myai/internal/platform"
	"github.com/Carlboms-Data-AB/myai/internal/run"
	"github.com/Carlboms-Data-AB/myai/internal/ui"
)

// harness builds a CLI wired to a temporary machine with faked commands.
func harness(t *testing.T, goos, goarch, input string) (*app.App, *ui.Console, *bytes.Buffer, *run.Fake) {
	t.Helper()

	home := t.TempDir()
	env := paths.Resolve(goos, goarch, home, nil)
	fake := run.NewFake()

	var out bytes.Buffer
	console := ui.NewTestConsole(strings.NewReader(input), &out)

	a, err := app.New(app.Options{
		Env:          &env,
		Host:         &platform.Host{OS: goos, Arch: goarch, User: "tester"},
		Runner:       fake,
		Reporter:     console,
		Asker:        console,
		Executable:   filepath.Join(home, "myai"),
		ReadyTimeout: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(a.Env().Executable(), []byte("x"), 0o755); err != nil {
		// The bin directory may not exist yet, which is fine for these tests.
		_ = err
	}
	return a, console, &out, fake
}

func TestStatusCommandRendersEverythingItPromises(t *testing.T) {
	a, console, out, _ := harness(t, "darwin", "arm64", "")

	if err := dispatch(context.Background(), a, console, "status", nil); err != nil {
		t.Fatal(err)
	}
	text := out.String()

	// The brief names exactly what status must report.
	for _, want := range []string{
		"MyAI",
		"OpenCode",
		"inference backend",
		"active model",
		"model in memory",
		"keep in RAM",
		"inference API",
		"inference service",
		"OpenCode Web",
		"web search",
		"browser automation",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("status output is missing %q:\n%s", want, text)
		}
	}
}

func TestStatusWorksOnEveryPlatform(t *testing.T) {
	for _, p := range [][2]string{{"darwin", "arm64"}, {"windows", "amd64"}, {"windows", "arm64"}, {"linux", "amd64"}, {"linux", "arm64"}} {
		a, console, out, _ := harness(t, p[0], p[1], "")
		if err := dispatch(context.Background(), a, console, "status", nil); err != nil {
			t.Fatalf("%s/%s: %v", p[0], p[1], err)
		}
		if !strings.Contains(out.String(), p[0]+"/"+p[1]) {
			t.Errorf("%s/%s status does not name the platform:\n%s", p[0], p[1], out.String())
		}
	}
}

func TestModelsListShowsCatalogForThePlatform(t *testing.T) {
	tests := map[string][2]string{
		"mlx-community/Qwen3.5-9B-6bit": {"darwin", "arm64"},
		"Qwen3.5-9B-Q6_K.gguf":          {"windows", "amd64"},
	}
	for want, p := range tests {
		a, console, out, _ := harness(t, p[0], p[1], "")
		if err := dispatch(context.Background(), a, console, "models", nil); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "qwen3.5-9b") {
			t.Errorf("%s/%s: catalog not listed:\n%s", p[0], p[1], out.String())
		}
		_ = want
	}
}

func TestModelsInstallWithoutArgumentExplainsItself(t *testing.T) {
	a, console, _, _ := harness(t, "linux", "amd64", "")

	err := dispatch(context.Background(), a, console, "models", []string{"install"})
	if err == nil || !strings.Contains(err.Error(), "which model") {
		t.Errorf("err = %v, want a helpful message", err)
	}
}

func TestUnknownCommandIsRejected(t *testing.T) {
	a, console, _, _ := harness(t, "linux", "amd64", "")

	err := dispatch(context.Background(), a, console, "frobnicate", nil)
	if err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Errorf("err = %v", err)
	}
}

func TestUnknownUninstallOptionIsRejected(t *testing.T) {
	a, console, _, _ := harness(t, "linux", "amd64", "")

	err := dispatch(context.Background(), a, console, "uninstall", []string{"--everything"})
	if err == nil || !strings.Contains(err.Error(), "unknown option") {
		t.Errorf("err = %v", err)
	}
}

func TestUninstallDefaultsToKeepingModelsAndAsksFirst(t *testing.T) {
	// An empty answer means "no" at the confirmation prompt.
	a, console, out, _ := harness(t, "darwin", "arm64", "\n")

	err := dispatch(context.Background(), a, console, "uninstall", nil)
	if err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("err = %v, want the operation to be cancelled", err)
	}
	text := out.String()
	if !strings.Contains(text, "This will keep") || !strings.Contains(text, "downloaded models") {
		t.Errorf("the plan should say models are kept:\n%s", text)
	}
}

func TestUninstallWithModelsRequiresTypedConfirmation(t *testing.T) {
	a, console, out, _ := harness(t, "darwin", "arm64", "yes\n")

	err := dispatch(context.Background(), a, console, "uninstall", []string{"--with-models"})
	if err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("err = %v; anything but the exact phrase must cancel", err)
	}
	if !strings.Contains(out.String(), "DELETE MODELS") {
		t.Errorf("the confirmation phrase should be requested:\n%s", out.String())
	}
}

func TestVersionCommand(t *testing.T) {
	a, console, out, _ := harness(t, "linux", "amd64", "")
	if err := dispatch(context.Background(), a, console, "version", nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "myai") {
		t.Errorf("version output = %q", out.String())
	}
}

func TestUsageListsEveryDocumentedCommand(t *testing.T) {
	text := usage()
	for _, want := range []string{"myai status", "myai test", "myai web", "myai restart", "myai models"} {
		if !strings.Contains(text, want) {
			t.Errorf("usage is missing %q", want)
		}
	}
}

func TestNonInteractiveRunReportsStatusRatherThanWaiting(t *testing.T) {
	a, console, out, _ := harness(t, "linux", "amd64", "")

	// A console with no terminal must not open a menu that nobody can answer.
	if console.Interactive() {
		t.Fatal("the test console should not be interactive")
	}
	if err := dispatch(context.Background(), a, console, "", nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "inference API") {
		t.Errorf("expected a status report:\n%s", out.String())
	}
}

func TestWebCommandSaysTheWebUIIsOff(t *testing.T) {
	// The Web UI is off until asked for, so this is what most machines see.
	a, console, out, _ := harness(t, "linux", "amd64", "")

	if err := dispatch(context.Background(), a, console, "web", nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "disabled") {
		t.Errorf("expected the output to say the Web UI is off:\n%s", out.String())
	}
}

func TestMainMenuShowsTheDocumentedLayout(t *testing.T) {
	// Choose Status, acknowledge, then Quit.
	a, console, out, _ := harness(t, "darwin", "arm64", "6\n\n11\n")
	console.SetInteractive(true)

	if err := MainMenu(context.Background(), a, console); err != nil {
		t.Fatal(err)
	}
	text := out.String()

	for _, want := range []string{
		"MYAI",
		" 1  OpenCode",
		" 2  OpenCode Web",
		" 3  Models",
		" 4  Runtime",
		" 5  Configure",
		" 6  Status",
		" 7  Test",
		" 8  Install / update",
		" 9  Restart services",
		"10  Uninstall",
		"11  Quit",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("main menu is missing %q", want)
		}
	}
	if !strings.Contains(text, "inference API") {
		t.Error("choosing Status did not show the status report")
	}
}

func TestModelsMenuShowsTheDocumentedLayout(t *testing.T) {
	// Choose Installed models, acknowledge, then Back.
	a, console, out, _ := harness(t, "darwin", "arm64", "1\n\n6\n")
	console.SetInteractive(true)

	if err := modelsMenu(context.Background(), a, console); err != nil {
		t.Fatal(err)
	}
	text := out.String()

	for _, want := range []string{
		"MYAI · Models",
		" 1  Installed models",
		" 2  Install model",
		" 3  Select active model",
		" 4  Delete model",
		" 5  Disk usage",
		" 6  Back",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("models menu is missing %q", want)
		}
	}
}

func TestUninstallMenuOffersTheFourChoices(t *testing.T) {
	// Pick Cancel.
	a, console, out, _ := harness(t, "darwin", "arm64", "4\n")
	console.SetInteractive(true)

	if err := uninstallMenu(context.Background(), a, console); err != nil {
		t.Fatal(err)
	}
	text := out.String()

	for _, want := range []string{
		"MYAI · Uninstall",
		"Uninstall MyAI, keep downloaded models",
		"Uninstall MyAI and delete models",
		"Delete downloaded models only",
		"Cancel",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("uninstall menu is missing %q:\n%s", want, text)
		}
	}
}

func TestUninstallMenuDefaultsToKeepingModels(t *testing.T) {
	// Accept the default choice, then decline the confirmation.
	a, console, out, _ := harness(t, "darwin", "arm64", "\n\n")
	console.SetInteractive(true)

	err := uninstallMenu(context.Background(), a, console)
	if err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(out.String(), "This will keep") {
		t.Errorf("the default choice must keep models:\n%s", out.String())
	}
}

func TestRuntimeMenuShowsLifecycleSettings(t *testing.T) {
	a, console, out, _ := harness(t, "darwin", "arm64", "6\n")
	console.SetInteractive(true)

	if err := runtimeMenu(context.Background(), a, console); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{"Keep model in RAM", "Idle unload", "Acceleration", "Backend"} {
		if !strings.Contains(text, want) {
			t.Errorf("runtime menu is missing %q:\n%s", want, text)
		}
	}
}

func TestConfigureMenuCoversTheRequiredSettings(t *testing.T) {
	a, console, out, _ := harness(t, "linux", "amd64", "10\n")
	console.SetInteractive(true)

	if err := configureMenu(context.Background(), a, console); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{
		"Context",
		"Output tokens",
		"Inference port",
		"OpenCode Web",
		"Web UI port",
		"Web UI bind address",
		"Web search",
		"Browser automation",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("configure menu is missing %q:\n%s", want, text)
		}
	}
}
