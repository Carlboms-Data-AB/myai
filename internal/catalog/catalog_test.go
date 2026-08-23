package catalog

import "testing"

func TestDefaultModelResolvesPerPlatform(t *testing.T) {
	tests := []struct {
		goos, goarch string
		wantBackend  string
		wantRepo     string
		wantFile     string
	}{
		{"darwin", "arm64", BackendMLXServe, "mlx-community/Qwen3.5-9B-6bit", ""},
		{"windows", "amd64", BackendLlamaCPP, "unsloth/Qwen3.5-9B-GGUF", "Qwen3.5-9B-Q6_K.gguf"},
		{"windows", "arm64", BackendLlamaCPP, "unsloth/Qwen3.5-9B-GGUF", "Qwen3.5-9B-Q6_K.gguf"},
		{"linux", "amd64", BackendLlamaCPP, "unsloth/Qwen3.5-9B-GGUF", "Qwen3.5-9B-Q6_K.gguf"},
		{"linux", "arm64", BackendLlamaCPP, "unsloth/Qwen3.5-9B-GGUF", "Qwen3.5-9B-Q6_K.gguf"},
	}
	for _, tt := range tests {
		t.Run(tt.goos+"/"+tt.goarch, func(t *testing.T) {
			got, err := Resolve("qwen3.5-9b", tt.goos, tt.goarch)
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if got.Backend() != tt.wantBackend {
				t.Errorf("backend = %q, want %q", got.Backend(), tt.wantBackend)
			}
			if got.Artifact.Repo != tt.wantRepo {
				t.Errorf("repo = %q, want %q", got.Artifact.Repo, tt.wantRepo)
			}
			if got.Artifact.File != tt.wantFile {
				t.Errorf("file = %q, want %q", got.Artifact.File, tt.wantFile)
			}
			if got.Artifact.Size <= 0 {
				t.Errorf("size = %d, want a positive size for disk checks", got.Artifact.Size)
			}
		})
	}
}

func TestMacOSResolvesToTheModelAlreadyInstalled(t *testing.T) {
	// The running Mac has this exact repository downloaded. Resolving to
	// anything else would trigger a needless multi-gigabyte download.
	got, err := Resolve("qwen3.5-9b", "darwin", "arm64")
	if err != nil {
		t.Fatal(err)
	}
	if got.Ref() != "mlx-community/Qwen3.5-9B-6bit" {
		t.Errorf("Ref = %q", got.Ref())
	}
	if got.Model.Context != 131072 || got.Model.Output != 16384 {
		t.Errorf("limits = %d/%d, want 131072/16384", got.Model.Context, got.Model.Output)
	}
}

func TestCompactModelResolves(t *testing.T) {
	mac, err := Resolve("qwen3.5-9b-compact", "darwin", "arm64")
	if err != nil {
		t.Fatal(err)
	}
	if mac.Artifact.Repo != "mlx-community/Qwen3.5-9B-4bit" {
		t.Errorf("mac repo = %q", mac.Artifact.Repo)
	}
	win, err := Resolve("qwen3.5-9b-compact", "windows", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	if win.Artifact.File != "Qwen3.5-9B-Q4_K_M.gguf" {
		t.Errorf("windows file = %q", win.Artifact.File)
	}
	if win.Artifact.Size >= mustResolve(t, "qwen3.5-9b", "windows", "amd64").Artifact.Size {
		t.Error("compact artifact should be smaller than the default one")
	}
}

func mustResolve(t *testing.T, id, goos, goarch string) Resolved {
	t.Helper()
	r, err := Resolve(id, goos, goarch)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestEveryCatalogModelCoversEverySupportedPlatform(t *testing.T) {
	platforms := [][2]string{
		{"darwin", "arm64"},
		{"windows", "amd64"},
		{"windows", "arm64"},
		{"linux", "amd64"},
		{"linux", "arm64"},
	}
	for _, m := range All() {
		for _, p := range platforms {
			if _, ok := m.artifactFor(p[0], p[1]); !ok {
				t.Errorf("model %q has no artifact for %s/%s", m.ID, p[0], p[1])
			}
		}
	}
}

func TestAvailableListsModelsForPlatform(t *testing.T) {
	got := Available("darwin", "arm64")
	if len(got) != len(All()) {
		t.Fatalf("Available returned %d models, want %d", len(got), len(All()))
	}
	for _, r := range got {
		if r.Backend() != BackendMLXServe {
			t.Errorf("model %q resolved to %q on Apple Silicon", r.Model.ID, r.Backend())
		}
	}
}

func TestDefaultBackend(t *testing.T) {
	tests := map[[2]string]string{
		{"darwin", "arm64"}:  BackendMLXServe,
		{"darwin", "amd64"}:  BackendLlamaCPP,
		{"windows", "amd64"}: BackendLlamaCPP,
		{"linux", "arm64"}:   BackendLlamaCPP,
	}
	for p, want := range tests {
		if got := DefaultBackend(p[0], p[1]); got != want {
			t.Errorf("%s/%s = %q, want %q", p[0], p[1], got, want)
		}
	}
}

func TestParseRef(t *testing.T) {
	tests := []struct {
		in                string
		backend           string
		repo, file, quant string
		wantErr           bool
	}{
		{in: "mlx-community/Qwen3.5-9B-6bit", backend: BackendMLXServe, repo: "mlx-community/Qwen3.5-9B-6bit"},
		{in: "unsloth/Qwen3.5-9B-GGUF", backend: BackendLlamaCPP, repo: "unsloth/Qwen3.5-9B-GGUF"},
		{in: "unsloth/Qwen3.5-9B-GGUF:Q6_K", backend: BackendLlamaCPP, repo: "unsloth/Qwen3.5-9B-GGUF", quant: "Q6_K"},
		{in: "unsloth/Qwen3.5-9B-GGUF/Qwen3.5-9B-Q6_K.gguf", backend: BackendLlamaCPP, repo: "unsloth/Qwen3.5-9B-GGUF", file: "Qwen3.5-9B-Q6_K.gguf"},
		{in: "unsloth/Qwen3.5-9B-GGUF/Qwen3.5-9B-Q6_K.gguf", backend: BackendMLXServe, wantErr: true},
		{in: "just-a-name", backend: BackendLlamaCPP, wantErr: true},
		{in: "a/b/c/d", backend: BackendLlamaCPP, wantErr: true},
		{in: "org/repo; rm -rf /", backend: BackendLlamaCPP, wantErr: true},
		{in: "", backend: BackendLlamaCPP, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.in+"/"+tt.backend, func(t *testing.T) {
			repo, file, quant, err := ParseRef(tt.in, tt.backend)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected an error for %q", tt.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseRef: %v", err)
			}
			if repo != tt.repo || file != tt.file || quant != tt.quant {
				t.Errorf("got (%q,%q,%q) want (%q,%q,%q)", repo, file, quant, tt.repo, tt.file, tt.quant)
			}
		})
	}
}

func TestResolveCustomReference(t *testing.T) {
	got, err := Resolve("mlx-community/Some-Other-Model-6bit", "darwin", "arm64")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Artifact.Custom {
		t.Error("custom reference should be marked as custom")
	}
	if got.Backend() != BackendMLXServe {
		t.Errorf("backend = %q", got.Backend())
	}
	if got.Ref() != "mlx-community/Some-Other-Model-6bit" {
		t.Errorf("Ref = %q", got.Ref())
	}
}

func TestResolveRejectsUnknownGarbage(t *testing.T) {
	if _, err := Resolve("not a model", "linux", "amd64"); err == nil {
		t.Error("expected an error for an unparseable reference")
	}
}

func TestRefIncludesFileForGGUF(t *testing.T) {
	r := mustResolve(t, "qwen3.5-9b", "linux", "amd64")
	if r.Ref() != "unsloth/Qwen3.5-9B-GGUF/Qwen3.5-9B-Q6_K.gguf" {
		t.Errorf("Ref = %q", r.Ref())
	}
	if r.Label() != "Qwen3.5 9B (Q6_K)" {
		t.Errorf("Label = %q", r.Label())
	}
}
