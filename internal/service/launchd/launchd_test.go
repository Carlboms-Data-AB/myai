package launchd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Carlboms-Data-AB/myai/internal/run"
	"github.com/Carlboms-Data-AB/myai/internal/service"
)

func sampleSpec(dir string) service.Spec {
	return service.Spec{
		Role:        service.RoleInference,
		Name:        "se.carlbomsdata.myai",
		DisplayName: "MyAI Inference",
		Description: "Local model server for MyAI",
		Exec:        "/opt/homebrew/bin/mlx-serve",
		Args:        []string{"--serve", "--model-dir", filepath.Join(dir, "models"), "--host", "127.0.0.1", "--port", "11234"},
		Env:         map[string]string{"MYAI_ROLE": "inference"},
		StdoutLog:   filepath.Join(dir, "logs", "inference.log"),
		StderrLog:   filepath.Join(dir, "logs", "inference-error.log"),
	}
}

func TestPlistIsWellFormedAndComplete(t *testing.T) {
	dir := t.TempDir()
	got, err := Plist(sampleSpec(dir))
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		"<key>Label</key>",
		"<string>se.carlbomsdata.myai</string>",
		"<string>/opt/homebrew/bin/mlx-serve</string>",
		"<string>--serve</string>",
		"<string>11234</string>",
		"<key>EnvironmentVariables</key>",
		"<key>MYAI_ROLE</key>",
		"<key>RunAtLoad</key>",
		"<key>KeepAlive</key>",
		"<string>Background</string>",
		"<key>StandardOutPath</key>",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("plist missing %q\n%s", want, got)
		}
	}
	if !strings.HasPrefix(got, `<?xml version="1.0"`) {
		t.Error("plist must start with an XML declaration")
	}
}

func TestPlistEscapesXML(t *testing.T) {
	spec := service.Spec{
		Name: "se.carlbomsdata.myai",
		Exec: "/bin/tool",
		Args: []string{`a & b`, `<script>`},
		Env:  map[string]string{"Q": `"quoted"`},
	}
	got, err := Plist(spec)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "<script>") {
		t.Error("argument was not escaped")
	}
	if !strings.Contains(got, "&amp;") {
		t.Error("ampersand was not escaped")
	}
}

func TestPlistRejectsMissingExecutable(t *testing.T) {
	if _, err := Plist(service.Spec{Name: "x"}); err == nil {
		t.Error("expected an error when no executable is set")
	}
}

func TestInstallWritesAndLoadsAgent(t *testing.T) {
	dir := t.TempDir()
	agents := filepath.Join(dir, "LaunchAgents")
	fake := run.NewFake()
	m := New(agents, 501, fake)

	if err := m.Install(context.Background(), sampleSpec(dir)); err != nil {
		t.Fatalf("Install: %v", err)
	}

	path := filepath.Join(agents, "se.carlbomsdata.myai.plist")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("plist not written: %v", err)
	}
	if !fake.Ran("plutil -lint") {
		t.Error("generated plist should be validated")
	}
	if !fake.Ran("launchctl bootout gui/501/se.carlbomsdata.myai") {
		t.Errorf("existing agent should be unloaded first: %v", fake.CommandLines())
	}
	if !fake.Ran("launchctl bootstrap gui/501 " + path) {
		t.Errorf("agent should be bootstrapped: %v", fake.CommandLines())
	}
	// Log directories must exist or launchd refuses to start the job.
	if _, err := os.Stat(filepath.Join(dir, "logs")); err != nil {
		t.Errorf("log directory not created: %v", err)
	}
}

func TestStatusParsesLaunchctlOutput(t *testing.T) {
	dir := t.TempDir()
	agents := filepath.Join(dir, "LaunchAgents")
	fake := run.NewFake()
	m := New(agents, 501, fake)
	if err := m.Install(context.Background(), sampleSpec(dir)); err != nil {
		t.Fatal(err)
	}

	fake.Respond("launchctl print", "se.carlbomsdata.myai = {\n\tstate = running\n\tpid = 4242\n}")
	got, err := m.Status(context.Background(), "se.carlbomsdata.myai")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Installed || !got.Running {
		t.Errorf("state = %+v", got)
	}
	if got.PID != 4242 {
		t.Errorf("PID = %d", got.PID)
	}
	if got.Summary() != "running (pid 4242)" {
		t.Errorf("Summary = %q", got.Summary())
	}
}

func TestStatusOfUnloadedAgentIsStoppedNotAnError(t *testing.T) {
	dir := t.TempDir()
	fake := run.NewFake()
	fake.DefaultErr = os.ErrNotExist
	m := New(filepath.Join(dir, "LaunchAgents"), 501, fake)

	got, err := m.Status(context.Background(), "se.carlbomsdata.myai")
	if err != nil {
		t.Fatalf("an unloaded agent should not be an error: %v", err)
	}
	if got.Running {
		t.Error("agent should not report as running")
	}
	if got.Summary() != "not installed" {
		t.Errorf("Summary = %q", got.Summary())
	}
}

func TestRemoveUnloadsAndDeletes(t *testing.T) {
	dir := t.TempDir()
	agents := filepath.Join(dir, "LaunchAgents")
	fake := run.NewFake()
	m := New(agents, 501, fake)
	if err := m.Install(context.Background(), sampleSpec(dir)); err != nil {
		t.Fatal(err)
	}

	if err := m.Remove(context.Background(), "se.carlbomsdata.myai"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(agents, "se.carlbomsdata.myai.plist")); !os.IsNotExist(err) {
		t.Error("plist should be gone")
	}
	if !fake.Ran("bootout") {
		t.Error("agent should be unloaded before removal")
	}
}

func TestRemoveAbsentAgentSucceeds(t *testing.T) {
	m := New(t.TempDir(), 501, run.NewFake())
	if err := m.Remove(context.Background(), "se.carlbomsdata.myai"); err != nil {
		t.Errorf("removing an absent agent should succeed: %v", err)
	}
}

func TestStartRequiresInstalledAgent(t *testing.T) {
	m := New(t.TempDir(), 501, run.NewFake())
	if err := m.Start(context.Background(), "se.carlbomsdata.myai"); err == nil {
		t.Error("expected an error when the agent is not installed")
	}
}

func TestStopLeavesPlistInPlace(t *testing.T) {
	dir := t.TempDir()
	agents := filepath.Join(dir, "LaunchAgents")
	fake := run.NewFake()
	m := New(agents, 501, fake)
	if err := m.Install(context.Background(), sampleSpec(dir)); err != nil {
		t.Fatal(err)
	}
	if err := m.Stop(context.Background(), "se.carlbomsdata.myai"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(agents, "se.carlbomsdata.myai.plist")); err != nil {
		t.Error("stopping must not delete the agent definition")
	}
}

// noWait removes the retry delay so tests do not sit through it.
func noWait(m *Manager) *Manager {
	m.Sleep = func(time.Duration) {}
	return m
}

func TestInstallSurvivesAJobThatIsStillLoaded(t *testing.T) {
	// launchctl bootout returns before the job is gone, so bootstrap can fail
	// with "Bootstrap failed: 5". That is not a real failure: the definition
	// is in place and the job just has to pick it up.
	dir := t.TempDir()
	fake := run.NewFake()
	fake.Respond("launchctl print", "state = running\npid = 4242")
	fake.Fail("launchctl bootstrap", errors.New("exit status 5: Bootstrap failed: 5: Input/output error"))

	m := noWait(New(filepath.Join(dir, "LaunchAgents"), 501, fake))
	if err := m.Install(context.Background(), sampleSpec(dir)); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !fake.Ran("launchctl kickstart -k") {
		t.Errorf("a loaded job should be kickstarted: %v", fake.CommandLines())
	}
}

func TestInstallReportsARealBootstrapFailure(t *testing.T) {
	// When the job genuinely is not loaded, a bootstrap failure is real.
	dir := t.TempDir()
	fake := run.NewFake()
	fake.Fail("launchctl bootstrap", errors.New("exit status 5"))
	fake.Fail("launchctl print", errors.New("could not find service"))

	m := noWait(New(filepath.Join(dir, "LaunchAgents"), 501, fake))
	if err := m.Install(context.Background(), sampleSpec(dir)); err == nil {
		t.Fatal("expected the failure to be reported")
	}
}

func TestLoadRetriesThroughLaunchdsTransientStates(t *testing.T) {
	// The real failure on a Mac: bootout has not finished, so bootstrap
	// returns 5 and kickstart returns 37, "Operation already in progress".
	// Both clear on their own within a moment.
	dir := t.TempDir()
	fake := run.NewFake()
	fake.Respond("launchctl print", "state = running")
	fake.Fail("launchctl bootstrap", errors.New("exit status 5: Bootstrap failed: 5: Input/output error"))
	fake.Fail("launchctl kickstart", errors.New("exit status 37"))

	attempts := 0
	m := New(filepath.Join(dir, "LaunchAgents"), 501, fake)
	m.Sleep = func(time.Duration) {
		attempts++
		if attempts >= 2 {
			// launchd settles: bootstrap starts working.
			delete(fake.Errors, "launchctl bootstrap")
		}
	}

	if err := m.Install(context.Background(), sampleSpec(dir)); err != nil {
		t.Fatalf("Install should have recovered: %v", err)
	}
}

func TestLoadGivesUpOnAPersistentFailure(t *testing.T) {
	dir := t.TempDir()
	fake := run.NewFake()
	fake.Fail("launchctl bootstrap", errors.New("exit status 5"))
	fake.Fail("launchctl print", errors.New("could not find service"))

	m := noWait(New(filepath.Join(dir, "LaunchAgents"), 501, fake))
	err := m.Install(context.Background(), sampleSpec(dir))
	if err == nil {
		t.Fatal("a failure that never clears must be reported")
	}
	if !strings.Contains(err.Error(), "se.carlbomsdata.myai") {
		t.Errorf("err = %v, want it to name the service", err)
	}
}

func TestLoadReportsTheKickstartFailureWhenThatIsWhatPersists(t *testing.T) {
	dir := t.TempDir()
	fake := run.NewFake()
	fake.Respond("launchctl print", "state = running")
	fake.Fail("launchctl bootstrap", errors.New("exit status 5"))
	fake.Fail("launchctl kickstart", errors.New("exit status 37"))

	m := noWait(New(filepath.Join(dir, "LaunchAgents"), 501, fake))
	err := m.Install(context.Background(), sampleSpec(dir))
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "37") {
		t.Errorf("err = %v, want the persistent failure", err)
	}
}
