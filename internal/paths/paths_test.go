package paths

import (
	"path/filepath"
	"testing"
)

func lookupFrom(m map[string]string) LookupFunc {
	return func(k string) (string, bool) {
		v, ok := m[k]
		return v, ok
	}
}

func TestResolveDarwinUsesXDGLayout(t *testing.T) {
	env := Resolve("darwin", "arm64", "/Users/t", lookupFrom(nil))

	want := map[string]string{
		"config": "/Users/t/.config/myai",
		"state":  "/Users/t/.local/state/myai",
		"data":   "/Users/t/.local/share/myai",
		"bin":    "/Users/t/.local/bin",
	}
	got := map[string]string{
		"config": env.Config,
		"state":  env.State,
		"data":   env.Data,
		"bin":    env.Bin,
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s = %q, want %q", k, got[k], w)
		}
	}
}

func TestResolveLinuxHonoursXDGOverrides(t *testing.T) {
	env := Resolve("linux", "amd64", "/home/t", lookupFrom(map[string]string{
		"XDG_CONFIG_HOME": "/custom/config",
		"XDG_STATE_HOME":  "/custom/state",
		"XDG_DATA_HOME":   "/custom/data",
	}))

	if env.Config != "/custom/config/myai" {
		t.Errorf("Config = %q", env.Config)
	}
	if env.State != "/custom/state/myai" {
		t.Errorf("State = %q", env.State)
	}
	if env.Data != "/custom/data/myai" {
		t.Errorf("Data = %q", env.Data)
	}
}

func TestResolveWindowsUsesAppData(t *testing.T) {
	env := Resolve("windows", "amd64", `C:\Users\t`, lookupFrom(map[string]string{
		"APPDATA":      `C:\Users\t\AppData\Roaming`,
		"LOCALAPPDATA": `C:\Users\t\AppData\Local`,
	}))

	if env.Config != filepath.Join(`C:\Users\t\AppData\Roaming`, "MyAI") {
		t.Errorf("Config = %q", env.Config)
	}
	if env.Data != filepath.Join(`C:\Users\t\AppData\Local`, "MyAI", "data") {
		t.Errorf("Data = %q", env.Data)
	}
}

func TestResolveWindowsFallsBackWithoutEnv(t *testing.T) {
	env := Resolve("windows", "arm64", `C:\Users\t`, lookupFrom(nil))

	if env.Config != filepath.Join(`C:\Users\t`, "AppData", "Roaming", "MyAI") {
		t.Errorf("Config = %q", env.Config)
	}
}

func TestExecutableNameIsPlatformCorrect(t *testing.T) {
	unix := Resolve("linux", "amd64", "/home/t", nil).Executable()
	if filepath.Base(unix) != "myai" {
		t.Errorf("unix executable = %q", unix)
	}
	win := Resolve("windows", "amd64", `C:\Users\t`, nil).Executable()
	if filepath.Base(win) != "myai.exe" {
		t.Errorf("windows executable = %q", win)
	}
}

func TestMLXModelDirMatchesMLXServeDefault(t *testing.T) {
	// mlx-serve owns ~/.mlx-serve/models. Getting this wrong would hide the
	// user's existing multi-gigabyte download.
	env := Resolve("darwin", "arm64", "/Users/t", nil)
	if env.MLXModelDir() != "/Users/t/.mlx-serve/models" {
		t.Errorf("MLXModelDir = %q", env.MLXModelDir())
	}
}
