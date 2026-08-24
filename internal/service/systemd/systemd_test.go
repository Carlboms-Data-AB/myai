package systemd

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Carlboms-Data-AB/myai/internal/run"
	"github.com/Carlboms-Data-AB/myai/internal/service"
)

func sampleSpec(dir string) service.Spec {
	return service.Spec{
		Role:        service.RoleWeb,
		Name:        "myai-opencode",
		DisplayName: "MyAI OpenCode Web",
		Description: "OpenCode Web interface served against the local MyAI model",
		Exec:        "/usr/local/bin/opencode",
		Args:        []string{"web", "--hostname", "0.0.0.0", "--port", "4096"},
		Env: map[string]string{
			"OPENCODE_CONFIG":          filepath.Join(dir, "opencode.json"),
			"OPENCODE_SERVER_USERNAME": "opencode",
		},
		StdoutLog: filepath.Join(dir, "logs", "web.log"),
		StderrLog: filepath.Join(dir, "logs", "web-error.log"),
	}
}

func TestUnitContainsRequiredSections(t *testing.T) {
	got, err := Unit(sampleSpec(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"[Unit]",
		"Description=OpenCode Web interface served against the local MyAI model",
		"[Service]",
		"Type=simple",
		"ExecStart=/usr/local/bin/opencode web --hostname 0.0.0.0 --port 4096",
		"Environment=OPENCODE_SERVER_USERNAME=opencode",
		"Restart=always",
		"[Install]",
		"WantedBy=default.target",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("unit missing %q\n%s", want, got)
		}
	}
}

func TestUnitEnvironmentIsDeterministic(t *testing.T) {
	spec := sampleSpec(t.TempDir())
	first, _ := Unit(spec)
	for i := 0; i < 20; i++ {
		again, _ := Unit(spec)
		if again != first {
			t.Fatal("unit generation is not stable across runs")
		}
	}
}

func TestUnitQuotesValuesWithSpaces(t *testing.T) {
	spec := service.Spec{
		Name: "myai",
		Exec: "/opt/My Tools/llama-server",
		Args: []string{"--model", "/models/a model.gguf"},
		Env:  map[string]string{"NOTE": "two words"},
	}
	got, err := Unit(spec)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `ExecStart="/opt/My Tools/llama-server" --model "/models/a model.gguf"`) {
		t.Errorf("paths with spaces not quoted:\n%s", got)
	}
	if !strings.Contains(got, `Environment=NOTE="two words"`) {
		t.Errorf("environment value not quoted:\n%s", got)
	}
}

func TestUnitRejectsMissingExecutable(t *testing.T) {
	if _, err := Unit(service.Spec{Name: "myai"}); err == nil {
		t.Error("expected an error when no executable is set")
	}
}

func TestUnitNameAddsSuffixOnce(t *testing.T) {
	if got := UnitName("myai"); got != "myai.service" {
		t.Errorf("UnitName = %q", got)
	}
	if got := UnitName("myai.service"); got != "myai.service" {
		t.Errorf("UnitName double-suffixed: %q", got)
	}
}

func TestInstallWritesUnitAndEnables(t *testing.T) {
	dir := t.TempDir()
	units := filepath.Join(dir, "systemd", "user")
	fake := run.NewFake()
	m := New(units, "tobias", fake)

	if _, err := m.Install(context.Background(), sampleSpec(dir)); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if _, err := os.Stat(filepath.Join(units, "myai-opencode.service")); err != nil {
		t.Fatalf("unit not written: %v", err)
	}
	if !fake.Ran("systemctl --user daemon-reload") {
		t.Errorf("daemon-reload not run: %v", fake.CommandLines())
	}
	if !fake.Ran("systemctl --user enable --now myai-opencode.service") {
		t.Errorf("unit not enabled: %v", fake.CommandLines())
	}
	if _, err := os.Stat(filepath.Join(dir, "logs")); err != nil {
		t.Errorf("log directory not created: %v", err)
	}
}

func TestStatusParsesSystemctlShow(t *testing.T) {
	dir := t.TempDir()
	units := filepath.Join(dir, "systemd", "user")
	fake := run.NewFake()
	fake.Respond("systemctl --user show", "ActiveState=active\nSubState=running\nMainPID=9182\nLoadState=loaded\n")
	m := New(units, "tobias", fake)
	if _, err := m.Install(context.Background(), sampleSpec(dir)); err != nil {
		t.Fatal(err)
	}

	got, err := m.Status(context.Background(), "myai-opencode")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Running || got.PID != 9182 || !got.Installed {
		t.Errorf("state = %+v", got)
	}
}

func TestStatusOfInactiveUnit(t *testing.T) {
	fake := run.NewFake()
	fake.Respond("systemctl --user show", "ActiveState=inactive\nSubState=dead\nMainPID=0\nLoadState=loaded\n")
	m := New(t.TempDir(), "tobias", fake)

	got, err := m.Status(context.Background(), "myai")
	if err != nil {
		t.Fatal(err)
	}
	if got.Running {
		t.Error("inactive unit should not report as running")
	}
	if got.PID != 0 {
		t.Errorf("PID = %d, want 0", got.PID)
	}
}

func TestRemoveDisablesAndDeletes(t *testing.T) {
	dir := t.TempDir()
	units := filepath.Join(dir, "systemd", "user")
	fake := run.NewFake()
	m := New(units, "tobias", fake)
	if _, err := m.Install(context.Background(), sampleSpec(dir)); err != nil {
		t.Fatal(err)
	}

	if err := m.Remove(context.Background(), "myai-opencode"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(units, "myai-opencode.service")); !os.IsNotExist(err) {
		t.Error("unit file should be removed")
	}
	if !fake.Ran("systemctl --user disable --now myai-opencode.service") {
		t.Errorf("unit not disabled: %v", fake.CommandLines())
	}
}

func TestEnableLingerUsesLoginctl(t *testing.T) {
	fake := run.NewFake()
	m := New(t.TempDir(), "tobias", fake)
	if err := m.EnableLinger(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !fake.Ran("loginctl enable-linger tobias") {
		t.Errorf("linger not enabled: %v", fake.CommandLines())
	}
}

// TestUnitPassesSystemdAnalyze checks the generated unit with systemd itself
// when the test runs on a machine that has it.
func TestUnitPassesSystemdAnalyze(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("systemd-analyze is Linux only")
	}
	if _, err := exec.LookPath("systemd-analyze"); err != nil {
		t.Skip("systemd-analyze not available")
	}

	dir := t.TempDir()
	spec := sampleSpec(dir)
	// systemd-analyze checks that ExecStart names a real executable, so point
	// it at one. The unit's syntax is what this test is for.
	spec.Exec = filepath.Join(dir, "opencode")
	if err := os.WriteFile(spec.Exec, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "logs"), 0o755); err != nil {
		t.Fatal(err)
	}

	body, err := Unit(spec)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "myai-opencode.service")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("systemd-analyze", "verify", path).CombinedOutput(); err != nil {
		t.Fatalf("systemd-analyze rejected the unit: %v\n%s\n%s", err, out, body)
	}
}

func TestUnitEscapesPercentSoSystemdDoesNotExpandIt(t *testing.T) {
	// systemd expands specifiers such as %i inside unit values, so a literal
	// percent in a path or an environment value has to be doubled.
	spec := service.Spec{
		Name: "myai",
		Exec: "/opt/my%dir/llama-server",
		Args: []string{"--model", "/models/100%good.gguf"},
		Env:  map[string]string{"NOTE": "50% done"},
	}
	got, err := Unit(spec)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"my%%dir", "100%%good.gguf", "50%% done"} {
		if !strings.Contains(got, want) {
			t.Errorf("unit should contain %q:\n%s", want, got)
		}
	}
	// No single percent should survive.
	stripped := strings.ReplaceAll(got, "%%", "")
	if strings.Contains(stripped, "%") {
		t.Errorf("an unescaped percent remains:\n%s", got)
	}
}
