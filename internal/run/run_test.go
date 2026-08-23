package run

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
)

func TestExecCapturesOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell utility")
	}
	res, err := Exec{}.Run(context.Background(), Spec{Name: "echo", Args: []string{"hello", "world"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.TrimSpace(res.Output) != "hello world" {
		t.Errorf("Output = %q", res.Output)
	}
	if res.ExitCode != 0 {
		t.Errorf("ExitCode = %d", res.ExitCode)
	}
}

func TestExecStreamsLines(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell utility")
	}
	var lines []string
	_, err := Exec{}.Run(context.Background(), Spec{
		Name:   "printf",
		Args:   []string{"one\ntwo\nthree\n"},
		OnLine: func(s string) { lines = append(lines, s) },
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(lines) != 3 || lines[0] != "one" || lines[2] != "three" {
		t.Errorf("streamed lines = %v", lines)
	}
}

func TestExecReportsFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell utility")
	}
	res, err := Exec{}.Run(context.Background(), Spec{Name: "false"})
	if err == nil {
		t.Fatal("expected an error from a non-zero exit")
	}
	if res.ExitCode == 0 {
		t.Errorf("ExitCode = %d, want non-zero", res.ExitCode)
	}
}

func TestFakeRecordsCalls(t *testing.T) {
	f := NewFake().Respond("launchctl print", "state = running")

	res, err := f.Run(context.Background(), Spec{Name: "launchctl", Args: []string{"print", "gui/501/se.carlbomsdata.myai"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Output != "state = running" {
		t.Errorf("Output = %q", res.Output)
	}
	if !f.Ran("launchctl print") {
		t.Errorf("call not recorded: %v", f.CommandLines())
	}
}

func TestFakeReturnsConfiguredError(t *testing.T) {
	want := errors.New("service not loaded")
	f := NewFake().Fail("bootout", want)

	if _, err := f.Run(context.Background(), Spec{Name: "launchctl", Args: []string{"bootout", "gui/501/x"}}); !errors.Is(err, want) {
		t.Errorf("err = %v, want %v", err, want)
	}
}

func TestFakeLookRespectsAbsent(t *testing.T) {
	f := NewFake().Absent("mlx-serve")
	if _, err := f.Look("mlx-serve"); err == nil {
		t.Error("expected mlx-serve to be missing")
	}
	if _, err := f.Look("opencode"); err != nil {
		t.Errorf("opencode should resolve: %v", err)
	}
	if Available(f, "mlx-serve") {
		t.Error("Available should be false for a missing executable")
	}
}

func TestFakeStreamsConfiguredOutput(t *testing.T) {
	f := NewFake().Respond("mlx-serve pull", "downloading\ndone\n")
	var lines []string
	if _, err := f.Run(context.Background(), Spec{
		Name:   "mlx-serve",
		Args:   []string{"pull", "org/repo"},
		OnLine: func(s string) { lines = append(lines, s) },
	}); err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 || lines[1] != "done" {
		t.Errorf("lines = %v", lines)
	}
}
