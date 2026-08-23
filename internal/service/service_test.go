package service

import "testing"

func TestServiceNamesMatchTheProductContract(t *testing.T) {
	tests := []struct {
		goos, role, want string
	}{
		{"darwin", RoleInference, "se.carlbomsdata.myai"},
		{"darwin", RoleWeb, "se.carlbomsdata.myai-opencode"},
		{"windows", RoleInference, "MyAI"},
		{"windows", RoleWeb, "MyAI-OpenCode"},
		{"linux", RoleInference, "myai"},
		{"linux", RoleWeb, "myai-opencode"},
	}
	for _, tt := range tests {
		if got := Name(tt.goos, tt.role); got != tt.want {
			t.Errorf("Name(%q,%q) = %q, want %q", tt.goos, tt.role, got, tt.want)
		}
	}
}

func TestEnvPairsAreSorted(t *testing.T) {
	spec := Spec{Env: map[string]string{
		"OPENCODE_SERVER_USERNAME": "opencode",
		"OPENCODE_CONFIG":          "/tmp/opencode.json",
		"MYAI_ROLE":                "web",
	}}
	got := spec.EnvPairs()
	want := []string{
		"MYAI_ROLE=web",
		"OPENCODE_CONFIG=/tmp/opencode.json",
		"OPENCODE_SERVER_USERNAME=opencode",
	}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("pair %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestStateSummary(t *testing.T) {
	tests := []struct {
		state State
		want  string
	}{
		{State{}, "not installed"},
		{State{Installed: true}, "stopped"},
		{State{Installed: true, Running: true}, "running"},
		{State{Installed: true, Running: true, PID: 42}, "running (pid 42)"},
	}
	for _, tt := range tests {
		if got := tt.state.Summary(); got != tt.want {
			t.Errorf("Summary = %q, want %q", got, tt.want)
		}
	}
}

func TestLegacyNamesCoverThePrototypeAgents(t *testing.T) {
	got := LegacyNames("darwin")
	want := map[string]bool{
		"se.carlbomsdata.local-ai-mlx-serve":    false,
		"se.carlbomsdata.local-ai-opencode-web": false,
		"se.carlbomsdata.mlx-serve":             false,
	}
	for _, name := range got {
		if _, ok := want[name]; !ok {
			t.Errorf("unexpected legacy name %q", name)
		}
		want[name] = true
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("legacy agent %q must be stopped during migration", name)
		}
	}
	if LegacyNames("linux") != nil {
		t.Error("only macOS had a prototype to migrate from")
	}
}
