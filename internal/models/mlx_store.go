package models

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Carlboms-Data-AB/myai/internal/catalog"
	"github.com/Carlboms-Data-AB/myai/internal/progress"
	"github.com/Carlboms-Data-AB/myai/internal/run"
)

// MLXStore manages models in the shared mlx-serve directory. mlx-serve owns
// that directory and the MLX Core app reads it too, so MyAI downloads through
// mlx-serve and only ever removes the specific model the user chose.
type MLXStore struct {
	// Root is the model directory, normally ~/.mlx-serve/models.
	Root string
	// Exec is the mlx-serve executable name or path.
	Exec string
	// Runner executes mlx-serve.
	Runner run.Runner
	// Target identifies the platform and this store's backend, for catalog
	// lookups.
	Target catalog.Target
}

// NewMLXStore returns a store rooted at dir.
func NewMLXStore(dir, exec string, runner run.Runner, goos, goarch string) *MLXStore {
	if exec == "" {
		exec = "mlx-serve"
	}
	return &MLXStore{
		Root:   dir,
		Exec:   exec,
		Runner: runner,
		Target: catalog.HostTarget(goos, goarch).WithBackend(catalog.BackendMLXServe),
	}
}

// Backend returns the mlx-serve backend identifier.
func (s *MLXStore) Backend() string { return catalog.BackendMLXServe }

// Location returns the model directory.
func (s *MLXStore) Location() string { return s.Root }

// PathFor returns the directory an MLX repository occupies.
func (s *MLXStore) PathFor(r catalog.Resolved) string {
	return filepath.Join(s.Root, filepath.FromSlash(r.Artifact.Repo))
}

// List reports every model directory in the store. mlx-serve lays models out
// as <root>/<org>/<repo>, which is what this walk assumes.
func (s *MLXStore) List(ctx context.Context) ([]Installed, error) {
	orgs, err := os.ReadDir(s.Root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var out []Installed
	for _, org := range orgs {
		if !org.IsDir() {
			continue
		}
		orgPath := filepath.Join(s.Root, org.Name())

		// mlx-serve lays models out as <root>/<org>/<repo>, but a store
		// populated another way may hold weights one level up. Missing such a
		// model would report it as absent and invite a needless multi-gigabyte
		// download, so check for that case first.
		if holdsWeights(orgPath) {
			out = append(out, s.entry(org.Name(), orgPath))
			continue
		}

		repos, err := os.ReadDir(orgPath)
		if err != nil {
			continue
		}
		for _, repo := range repos {
			if !repo.IsDir() {
				continue
			}
			out = append(out, s.entry(org.Name()+"/"+repo.Name(), filepath.Join(orgPath, repo.Name())))
		}
	}
	out = withoutEmpty(out)
	sort.Slice(out, func(i, j int) bool { return out[i].Ref < out[j].Ref })
	return out, nil
}

// Prepare returns the artifact unchanged: an MLX artifact is a whole
// repository, so there is no file to work out.
func (s *MLXStore) Prepare(_ context.Context, r catalog.Resolved) (catalog.Resolved, error) {
	return r, nil
}

// entry describes one model directory.
func (s *MLXStore) entry(ref, path string) Installed {
	size, _ := dirSize(path)
	name, managed := displayName(ref, s.Target)
	return Installed{
		Ref:     ref,
		Name:    name,
		Path:    path,
		Size:    size,
		Backend: s.Backend(),
		Managed: managed,
	}
}

// holdsWeights reports whether a directory contains model weights directly.
func holdsWeights(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := strings.ToLower(e.Name())
		if strings.HasSuffix(name, ".safetensors") || strings.HasSuffix(name, ".gguf") {
			return true
		}
	}
	return false
}

// withoutEmpty drops directories that hold nothing.
func withoutEmpty(in []Installed) []Installed {
	out := in[:0]
	for _, m := range in {
		if m.Size > 0 {
			out = append(out, m)
		}
	}
	return out
}

// Has reports whether the repository directory exists and holds weights.
func (s *MLXStore) Has(_ context.Context, r catalog.Resolved) (bool, error) {
	path := s.PathFor(r)
	entries, err := os.ReadDir(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	// A directory holding only a partial download is not a usable model.
	for _, e := range entries {
		name := strings.ToLower(e.Name())
		if strings.HasSuffix(name, ".safetensors") || strings.HasSuffix(name, ".gguf") {
			return true, nil
		}
	}
	return false, nil
}

// Install downloads a repository through mlx-serve, which resumes partial
// transfers and shares the download with the MLX Core app.
func (s *MLXStore) Install(ctx context.Context, r catalog.Resolved, rep progress.Reporter) error {
	reporter := progress.Or(rep)

	have, err := s.Has(ctx, r)
	if err != nil {
		return err
	}
	if have {
		reporter.Info(fmt.Sprintf("%s is already installed", r.Artifact.Repo))
		return nil
	}

	if err := EnsureSpace(s.Root, r.Artifact.Size); err != nil {
		return err
	}
	if err := os.MkdirAll(s.Root, 0o755); err != nil {
		return err
	}

	reporter.Step("Downloading " + r.Artifact.Repo)
	_, err = s.Runner.Run(ctx, run.Spec{
		Name:   s.Exec,
		Args:   []string{"pull", r.Artifact.Repo},
		OnLine: func(line string) { reporter.Info(line) },
	})
	if err != nil {
		return fmt.Errorf("mlx-serve pull %s: %w", r.Artifact.Repo, err)
	}

	have, err = s.Has(ctx, r)
	if err != nil {
		return err
	}
	if !have {
		return fmt.Errorf("mlx-serve reported success but %s is not in %s", r.Artifact.Repo, s.Root)
	}
	return nil
}

// Delete removes one repository directory and prunes the organisation
// directory if it becomes empty.
func (s *MLXStore) Delete(ctx context.Context, ref string) error {
	installed, err := s.List(ctx)
	if err != nil {
		return err
	}
	for _, m := range installed {
		if m.Ref == ref {
			if err := os.RemoveAll(m.Path); err != nil {
				return err
			}
			pruneEmptyParents(m.Path, s.Root)
			return nil
		}
	}
	return fmt.Errorf("%w: %s", ErrNotInstalled, ref)
}

// DiskUsage totals the whole store.
func (s *MLXStore) DiskUsage(context.Context) (int64, error) { return dirSize(s.Root) }
