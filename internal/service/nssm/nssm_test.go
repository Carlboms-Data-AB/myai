package nssm

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Carlboms-Data-AB/myai/internal/run"
	"github.com/Carlboms-Data-AB/myai/internal/service"
)

func sampleSpec(dir string) service.Spec {
	return service.Spec{
		Role:        service.RoleInference,
		Name:        "MyAI",
		DisplayName: "MyAI Inference",
		Description: "Local model server for MyAI",
		Exec:        filepath.Join(dir, "llama-server.exe"),
		Args:        []string{"--model", filepath.Join(dir, "model.gguf"), "--host", "127.0.0.1", "--port", "11234"},
		Env:         map[string]string{"MYAI_ROLE": "inference"},
		Dir:         dir,
		StdoutLog:   filepath.Join(dir, "logs", "inference.log"),
		StderrLog:   filepath.Join(dir, "logs", "inference-error.log"),
		Account:     service.Account{User: `MACHINE\tobias`, Password: "hunter2"},
	}
}

func settingsMap(spec service.Spec) map[string][]string {
	out := map[string][]string{}
	for _, s := range Settings(spec) {
		out[s[0]] = s[1:]
	}
	return out
}

func TestSettingsCoverEverythingNSSMNeeds(t *testing.T) {
	dir := t.TempDir()
	got := settingsMap(sampleSpec(dir))

	if got["Application"][0] != filepath.Join(dir, "llama-server.exe") {
		t.Errorf("Application = %v", got["Application"])
	}
	if !strings.Contains(got["AppParameters"][0], "--port 11234") {
		t.Errorf("AppParameters = %v", got["AppParameters"])
	}
	if got["Start"][0] != "SERVICE_AUTO_START" {
		t.Errorf("Start = %v", got["Start"])
	}
	if len(got["AppExit"]) != 2 || got["AppExit"][0] != "Default" || got["AppExit"][1] != "Restart" {
		t.Errorf("AppExit = %v", got["AppExit"])
	}
	if got["DisplayName"][0] != "MyAI Inference" {
		t.Errorf("DisplayName = %v", got["DisplayName"])
	}
	if got["AppDirectory"][0] != dir {
		t.Errorf("AppDirectory = %v", got["AppDirectory"])
	}
	if got["AppStdout"][0] == "" || got["AppStderr"][0] == "" {
		t.Errorf("logs not configured: %v", got)
	}
	if got["AppRotateFiles"][0] != "1" {
		t.Errorf("AppRotateFiles = %v", got["AppRotateFiles"])
	}
	if len(got["AppEnvironmentExtra"]) != 1 || got["AppEnvironmentExtra"][0] != "MYAI_ROLE=inference" {
		t.Errorf("AppEnvironmentExtra = %v", got["AppEnvironmentExtra"])
	}
}

func TestSettingsNeverContainThePassword(t *testing.T) {
	// Settings is rendered in tests and diagnostics, so credentials must not
	// travel through it.
	for _, setting := range Settings(sampleSpec(t.TempDir())) {
		for _, value := range setting {
			if strings.Contains(value, "hunter2") {
				t.Fatalf("password leaked into setting %v", setting)
			}
			if setting[0] == "ObjectName" {
				t.Fatal("ObjectName must be set separately from other parameters")
			}
		}
	}
}

func TestSettingsQuoteArgumentsWithSpaces(t *testing.T) {
	spec := service.Spec{
		Name:    "MyAI",
		Exec:    `C:\Program Files\MyAI\llama-server.exe`,
		Args:    []string{"--model", `C:\Users\t\AppData\Local\My AI\model.gguf`},
		Account: service.Account{User: `.\t`, Password: "x"},
	}
	got := settingsMap(spec)["AppParameters"][0]
	if !strings.Contains(got, `"C:\Users\t\AppData\Local\My AI\model.gguf"`) {
		t.Errorf("AppParameters = %q", got)
	}
}

func TestInstallRefusesWithoutAnAccount(t *testing.T) {
	spec := sampleSpec(t.TempDir())
	spec.Account = service.Account{}

	fake := run.NewFake()
	// A service that does not exist yet has no account to inherit.
	fake.Fail("status MyAI", errors.New("service does not exist"))
	err := New("nssm", fake).Install(context.Background(), spec)
	if !errors.Is(err, ErrAccountRequired) {
		t.Fatalf("err = %v, want ErrAccountRequired", err)
	}
	if fake.Ran("nssm install MyAI") {
		t.Error("nothing should be installed without an account")
	}
}

func TestExistingServiceKeepsItsAccount(t *testing.T) {
	// Reconfiguring an existing service must not need the password again,
	// otherwise every settings change would prompt for it.
	spec := sampleSpec(t.TempDir())
	spec.Account = service.Account{}

	fake := run.NewFake().Respond("status MyAI", "SERVICE_RUNNING")
	if err := New("nssm", fake).Install(context.Background(), spec); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if fake.Ran("ObjectName") {
		t.Errorf("the account should have been left alone: %v", fake.CommandLines())
	}
	if !fake.Ran("set MyAI Application") {
		t.Errorf("the service should still be reconfigured: %v", fake.CommandLines())
	}
}

func TestInstallCreatesAndConfiguresService(t *testing.T) {
	dir := t.TempDir()
	fake := run.NewFake()
	// A service that does not exist yet: status fails.
	fake.Fail("status MyAI", errors.New("service does not exist"))

	if err := New("nssm", fake).Install(context.Background(), sampleSpec(dir)); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !fake.Ran("nssm install MyAI") {
		t.Errorf("service not installed: %v", fake.CommandLines())
	}
	if !fake.Ran("set MyAI Start SERVICE_AUTO_START") {
		t.Errorf("start type not configured: %v", fake.CommandLines())
	}
	if !fake.Ran(`set MyAI ObjectName MACHINE\tobias hunter2`) {
		t.Errorf("service account not configured: %v", fake.CommandLines())
	}
}

func TestInstallUpdatesExistingServiceInPlace(t *testing.T) {
	dir := t.TempDir()
	fake := run.NewFake()
	fake.Respond("status MyAI", "SERVICE_RUNNING")

	if err := New("nssm", fake).Install(context.Background(), sampleSpec(dir)); err != nil {
		t.Fatal(err)
	}
	if fake.Ran("nssm install MyAI") {
		t.Error("an existing service should be updated, not reinstalled")
	}
	if !fake.Ran("set MyAI Application") {
		t.Errorf("existing service not reconfigured: %v", fake.CommandLines())
	}
}

func TestStatusMapsNSSMVocabulary(t *testing.T) {
	tests := []struct {
		output      string
		wantRunning bool
	}{
		{"SERVICE_RUNNING", true},
		{"SERVICE_STOPPED", false},
		{"SERVICE_PAUSED", false},
		// nssm writes UTF-16 to redirected handles.
		{"S\x00E\x00R\x00V\x00I\x00C\x00E\x00_\x00R\x00U\x00N\x00N\x00I\x00N\x00G\x00", true},
	}
	for _, tt := range tests {
		fake := run.NewFake().Respond("status MyAI", tt.output)
		got, err := New("nssm", fake).Status(context.Background(), "MyAI")
		if err != nil {
			t.Fatal(err)
		}
		if !got.Installed {
			t.Errorf("%q: service should report as installed", tt.output)
		}
		if got.Running != tt.wantRunning {
			t.Errorf("%q: Running = %v, want %v", tt.output, got.Running, tt.wantRunning)
		}
	}
}

func TestStatusOfMissingServiceIsNotAnError(t *testing.T) {
	fake := run.NewFake()
	fake.DefaultErr = errors.New("service does not exist")

	got, err := New("nssm", fake).Status(context.Background(), "MyAI")
	if err != nil {
		t.Fatalf("a missing service should not be an error: %v", err)
	}
	if got.Installed || got.Running {
		t.Errorf("state = %+v", got)
	}
}

func TestRemoveStopsThenDeletes(t *testing.T) {
	fake := run.NewFake().Respond("status MyAI", "SERVICE_RUNNING")

	if err := New("nssm", fake).Remove(context.Background(), "MyAI"); err != nil {
		t.Fatal(err)
	}
	lines := fake.CommandLines()
	stopAt, removeAt := -1, -1
	for i, l := range lines {
		if strings.Contains(l, "nssm stop MyAI") {
			stopAt = i
		}
		if strings.Contains(l, "nssm remove MyAI confirm") {
			removeAt = i
		}
	}
	if stopAt < 0 || removeAt < 0 {
		t.Fatalf("stop and remove not both run: %v", lines)
	}
	if stopAt > removeAt {
		t.Error("the service must be stopped before it is removed")
	}
}

func TestRemoveAbsentServiceSucceeds(t *testing.T) {
	fake := run.NewFake()
	fake.DefaultErr = errors.New("service does not exist")

	if err := New("nssm", fake).Remove(context.Background(), "MyAI"); err != nil {
		t.Errorf("removing an absent service should succeed: %v", err)
	}
	if fake.Ran("remove MyAI confirm") {
		t.Error("should not try to remove a service that is not there")
	}
}

func TestNeedsAccount(t *testing.T) {
	if !New("nssm", run.NewFake()).NeedsAccount() {
		t.Error("Windows services must declare that they need an account")
	}
}
