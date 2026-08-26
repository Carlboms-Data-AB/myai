package models

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

	"github.com/carlbomsdata/myai/internal/catalog"
	"github.com/carlbomsdata/myai/internal/run"
)

func mustResolve(t *testing.T, id, goos, goarch string) catalog.Resolved {
	t.Helper()
	r, err := catalog.Resolve(id, catalog.HostTarget(goos, goarch))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// --- MLX store ---

func writeMLXModel(t *testing.T, root, repo string, size int) string {
	t.Helper()
	dir := filepath.Join(root, filepath.FromSlash(repo))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "model.safetensors")
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestMLXStoreListsInstalledModels(t *testing.T) {
	root := t.TempDir()
	writeMLXModel(t, root, "mlx-community/Qwen3.5-9B-6bit", 2048)
	writeMLXModel(t, root, "someone/Other-Model", 1024)

	store := NewMLXStore(root, "mlx-serve", run.NewFake(), "darwin", "arm64")
	got, err := store.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d models, want 2: %+v", len(got), got)
	}
	if got[0].Ref != "mlx-community/Qwen3.5-9B-6bit" {
		t.Errorf("first ref = %q", got[0].Ref)
	}
	if !got[0].Managed {
		t.Error("the catalog model should be recognised as managed")
	}
	if got[0].Name != "Qwen3.5 9B (6-bit)" {
		t.Errorf("name = %q", got[0].Name)
	}
	if got[0].Size != 2048 {
		t.Errorf("size = %d", got[0].Size)
	}
	if got[1].Managed {
		t.Error("an unknown model should not be marked managed")
	}
}

func TestMLXStoreListsNothingWhenRootMissing(t *testing.T) {
	store := NewMLXStore(filepath.Join(t.TempDir(), "absent"), "mlx-serve", run.NewFake(), "darwin", "arm64")
	got, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("a missing store should not be an error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d models", len(got))
	}
}

func TestMLXStoreSkipsIfAlreadyInstalled(t *testing.T) {
	root := t.TempDir()
	r := mustResolve(t, "qwen3.5-9b", "darwin", "arm64")
	writeMLXModel(t, root, r.Artifact.Repo, 4096)

	fake := run.NewFake()
	store := NewMLXStore(root, "mlx-serve", fake, "darwin", "arm64")

	if err := store.Install(context.Background(), r, nil); err != nil {
		t.Fatal(err)
	}
	if fake.Ran("pull") {
		t.Error("an installed model must not be downloaded again")
	}
}

func TestMLXStoreInstallInvokesPull(t *testing.T) {
	root := t.TempDir()
	r := mustResolve(t, "qwen3.5-9b", "darwin", "arm64")

	fake := run.NewFake()
	store := NewMLXStore(root, "mlx-serve", fake, "darwin", "arm64")

	// mlx-serve is faked, so nothing lands on disk and the check after the
	// download must notice.
	err := store.Install(context.Background(), r, nil)
	if err == nil {
		t.Fatal("expected an error when the model is missing after pull")
	}
	if !fake.Ran("mlx-serve pull mlx-community/Qwen3.5-9B-6bit") {
		t.Errorf("wrong command: %v", fake.CommandLines())
	}
}

func TestMLXStoreDeleteRemovesOnlyTheChosenModel(t *testing.T) {
	root := t.TempDir()
	keep := writeMLXModel(t, root, "mlx-community/Qwen3.5-9B-6bit", 1024)
	drop := writeMLXModel(t, root, "someone/Other-Model", 1024)

	store := NewMLXStore(root, "mlx-serve", run.NewFake(), "darwin", "arm64")
	if err := store.Delete(context.Background(), "someone/Other-Model"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(drop); !os.IsNotExist(err) {
		t.Error("chosen model was not removed")
	}
	if _, err := os.Stat(keep); err != nil {
		t.Error("the other model must survive")
	}
	// The now-empty organisation directory should be gone too.
	if _, err := os.Stat(filepath.Join(root, "someone")); !os.IsNotExist(err) {
		t.Error("empty organisation directory should be pruned")
	}
}

func TestMLXStoreDeleteUnknownModel(t *testing.T) {
	store := NewMLXStore(t.TempDir(), "mlx-serve", run.NewFake(), "darwin", "arm64")
	err := store.Delete(context.Background(), "nobody/nothing")
	if !errors.Is(err, ErrNotInstalled) {
		t.Errorf("err = %v, want ErrNotInstalled", err)
	}
}

func TestMLXStoreHasIgnoresEmptyDirectory(t *testing.T) {
	root := t.TempDir()
	r := mustResolve(t, "qwen3.5-9b", "darwin", "arm64")
	if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(r.Artifact.Repo)), 0o755); err != nil {
		t.Fatal(err)
	}
	store := NewMLXStore(root, "mlx-serve", run.NewFake(), "darwin", "arm64")

	have, err := store.Has(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if have {
		t.Error("a directory without weights is not an installed model")
	}
}

func TestMLXStoreDiskUsage(t *testing.T) {
	root := t.TempDir()
	writeMLXModel(t, root, "a/b", 1000)
	writeMLXModel(t, root, "c/d", 2000)

	store := NewMLXStore(root, "mlx-serve", run.NewFake(), "darwin", "arm64")
	got, err := store.DiskUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != 3000 {
		t.Errorf("DiskUsage = %d, want 3000", got)
	}
}

// --- GGUF store ---

func ggufServer(t *testing.T, body []byte) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write(body)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("HF_ENDPOINT", srv.URL)
}

func TestGGUFStoreInstallAndList(t *testing.T) {
	body := []byte(strings.Repeat("g", 5000))
	ggufServer(t, body)

	root := t.TempDir()
	store := NewGGUFStore(root, "linux", "amd64")
	r := mustResolve(t, "qwen3.5-9b", "linux", "amd64")
	// The real artifact is gigabytes; the test server serves a stub, so clear
	// the recorded size to keep the space check honest.
	r.Artifact.Size = 0

	if err := store.Install(context.Background(), r, nil); err != nil {
		t.Fatalf("Install: %v", err)
	}

	got, err := store.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d models, want 1", len(got))
	}
	if got[0].Ref != "unsloth/Qwen3.5-9B-GGUF/Qwen3.5-9B-Q6_K.gguf" {
		t.Errorf("ref = %q", got[0].Ref)
	}
	if !got[0].Managed || got[0].Name != "Qwen3.5 9B (Q6_K)" {
		t.Errorf("catalog model not recognised: %+v", got[0])
	}
	if got[0].Size != int64(len(body)) {
		t.Errorf("size = %d, want %d", got[0].Size, len(body))
	}
}

func TestGGUFStoreDoesNotRedownload(t *testing.T) {
	var downloads int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			downloads++
		}
		w.Write([]byte("stub"))
	}))
	defer srv.Close()
	t.Setenv("HF_ENDPOINT", srv.URL)

	root := t.TempDir()
	store := NewGGUFStore(root, "linux", "amd64")
	r := mustResolve(t, "qwen3.5-9b", "linux", "amd64")
	r.Artifact.Size = 0

	if err := store.Install(context.Background(), r, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.Install(context.Background(), r, nil); err != nil {
		t.Fatal(err)
	}
	if downloads != 1 {
		t.Errorf("downloaded %d times, want 1: an installed model must not be fetched again", downloads)
	}
}

func TestGGUFStoreDeletePrunesEmptyDirectories(t *testing.T) {
	ggufServer(t, []byte("stub"))

	root := t.TempDir()
	store := NewGGUFStore(root, "linux", "amd64")
	r := mustResolve(t, "qwen3.5-9b", "linux", "amd64")
	r.Artifact.Size = 0
	if err := store.Install(context.Background(), r, nil); err != nil {
		t.Fatal(err)
	}

	if err := store.Delete(context.Background(), r.Ref()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(store.PathFor(r)); !os.IsNotExist(err) {
		t.Error("file was not removed")
	}
	if _, err := os.Stat(filepath.Join(root, "unsloth")); !os.IsNotExist(err) {
		t.Error("empty directories should be pruned")
	}
	if _, err := os.Stat(root); err != nil {
		t.Error("the store root itself must survive")
	}
}

func TestGGUFStoreDifferentQuantsCoexist(t *testing.T) {
	ggufServer(t, []byte("stub"))

	root := t.TempDir()
	store := NewGGUFStore(root, "linux", "amd64")
	for _, id := range []string{"qwen3.5-9b", "qwen3.5-9b-compact"} {
		r := mustResolve(t, id, "linux", "amd64")
		r.Artifact.Size = 0
		if err := store.Install(context.Background(), r, nil); err != nil {
			t.Fatal(err)
		}
	}
	got, err := store.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d models, want both quantizations: %+v", len(got), got)
	}
}

func TestGGUFStoreRejectsArtifactWithoutFile(t *testing.T) {
	store := NewGGUFStore(t.TempDir(), "linux", "amd64")
	r := mustResolve(t, "unsloth/Some-GGUF", "linux", "amd64")
	if err := store.Install(context.Background(), r, nil); err == nil {
		t.Error("expected an error when no GGUF file is named")
	}
}

func TestEnsureSpaceRejectsImpossibleDownload(t *testing.T) {
	const petabyte = 1 << 50
	if err := EnsureSpace(t.TempDir(), petabyte); err == nil {
		t.Error("expected a failure when the artifact cannot possibly fit")
	}
}

func TestEnsureSpaceAllowsSmallDownload(t *testing.T) {
	if err := EnsureSpace(t.TempDir(), 1024); err != nil {
		t.Errorf("EnsureSpace: %v", err)
	}
}

func TestGGUFStorePrepareResolvesAQuantizationLabel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"siblings":[{"rfilename":"Qwen3.5-9B-Q5_K_M.gguf"},{"rfilename":"Qwen3.5-9B-Q6_K.gguf"}]}`))
	}))
	defer srv.Close()
	t.Setenv("HF_ENDPOINT", srv.URL)

	store := NewGGUFStore(t.TempDir(), "linux", "amd64")
	// This is the form the menu and the README tell people they can use.
	r := mustResolve(t, "unsloth/Qwen3.5-9B-GGUF:Q5_K_M", "linux", "amd64")
	if r.Artifact.File != "" {
		t.Fatalf("precondition: the reference should carry no file, got %q", r.Artifact.File)
	}

	prepared, err := store.Prepare(context.Background(), r)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if prepared.Artifact.File != "Qwen3.5-9B-Q5_K_M.gguf" {
		t.Errorf("File = %q", prepared.Artifact.File)
	}
	if !strings.HasSuffix(store.PathFor(prepared), "Qwen3.5-9B-Q5_K_M.gguf") {
		t.Errorf("PathFor = %q", store.PathFor(prepared))
	}
}

func TestGGUFStorePrepareLeavesAnExplicitFileAlone(t *testing.T) {
	store := NewGGUFStore(t.TempDir(), "linux", "amd64")
	r := mustResolve(t, "qwen3.5-9b", "linux", "amd64")

	prepared, err := store.Prepare(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Artifact.File != r.Artifact.File {
		t.Errorf("File changed from %q to %q", r.Artifact.File, prepared.Artifact.File)
	}
}

func TestGGUFStorePrepareExplainsABareRepository(t *testing.T) {
	store := NewGGUFStore(t.TempDir(), "linux", "amd64")
	r := mustResolve(t, "unsloth/Some-GGUF", "linux", "amd64")

	_, err := store.Prepare(context.Background(), r)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "Q6_K") {
		t.Errorf("err = %v, want it to suggest a quantization", err)
	}
}

func TestMLXStorePrepareIsAPassThrough(t *testing.T) {
	store := NewMLXStore(t.TempDir(), "mlx-serve", run.NewFake(), "darwin", "arm64")
	r := mustResolve(t, "qwen3.5-9b", "darwin", "arm64")

	prepared, err := store.Prepare(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Artifact.Repo != r.Artifact.Repo {
		t.Errorf("Prepare changed the artifact: %+v", prepared.Artifact)
	}
}

func TestMLXStoreFindsAModelStoredWithoutAnOrgDirectory(t *testing.T) {
	// A store populated by other means may hold weights one level up.
	// Missing it would report the model as absent and invite a needless
	// multi-gigabyte download.
	root := t.TempDir()
	dir := filepath.Join(root, "Some-Local-Model")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "model.safetensors"), make([]byte, 512), 0o644); err != nil {
		t.Fatal(err)
	}

	store := NewMLXStore(root, "mlx-serve", run.NewFake(), "darwin", "arm64")
	got, err := store.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d models, want 1: %+v", len(got), got)
	}
	if got[0].Ref != "Some-Local-Model" || got[0].Size != 512 {
		t.Errorf("entry = %+v", got[0])
	}
}

func TestMLXStoreStillPrefersTheNestedLayout(t *testing.T) {
	root := t.TempDir()
	writeMLXModel(t, root, "mlx-community/Qwen3.5-9B-6bit", 1024)

	store := NewMLXStore(root, "mlx-serve", run.NewFake(), "darwin", "arm64")
	got, err := store.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Ref != "mlx-community/Qwen3.5-9B-6bit" {
		t.Errorf("entries = %+v", got)
	}
}

func TestMLXStoreIgnoresDirectoriesWithoutWeights(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "empty-org", "empty-repo"), 0o755); err != nil {
		t.Fatal(err)
	}

	store := NewMLXStore(root, "mlx-serve", run.NewFake(), "darwin", "arm64")
	got, err := store.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("got %+v, want nothing", got)
	}
}
