package models

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Carlboms-Data-AB/myai/internal/catalog"
	"github.com/Carlboms-Data-AB/myai/internal/hf"
	"github.com/Carlboms-Data-AB/myai/internal/progress"
)

// GGUFStore manages GGUF files for the llama.cpp backend. MyAI owns this
// directory, downloads into it directly and mirrors the Hugging Face layout so
// several quantizations of the same repository can coexist.
type GGUFStore struct {
	// Root is the model directory.
	Root string
	// Client downloads files. A nil client uses a default one.
	Client *hf.Client
	// Target identifies the platform and this store's backend, for catalog
	// lookups.
	Target catalog.Target
}

// NewGGUFStore returns a store rooted at dir.
func NewGGUFStore(dir, goos, goarch string) *GGUFStore {
	return &GGUFStore{
		Root:   dir,
		Client: hf.New(),
		Target: catalog.HostTarget(goos, goarch).WithBackend(catalog.BackendLlamaCPP),
	}
}

// Backend returns the llama.cpp backend identifier.
func (s *GGUFStore) Backend() string { return catalog.BackendLlamaCPP }

// Location returns the model directory.
func (s *GGUFStore) Location() string { return s.Root }

// PathFor returns the file a GGUF artifact occupies.
func (s *GGUFStore) PathFor(r catalog.Resolved) string {
	file := r.Artifact.File
	if file == "" {
		// A reference without an explicit file cannot be placed on disk
		// deterministically, so fall back to the repository name.
		file = filepath.Base(r.Artifact.Repo) + ".gguf"
	}
	return filepath.Join(s.Root, filepath.FromSlash(r.Artifact.Repo), file)
}

// List reports every GGUF file in the store.
func (s *GGUFStore) List(context.Context) ([]Installed, error) {
	var out []Installed
	err := filepath.WalkDir(s.Root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() || !strings.HasSuffix(strings.ToLower(d.Name()), ".gguf") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(s.Root, path)
		if err != nil {
			return nil
		}
		ref := filepath.ToSlash(rel)
		name, managed := displayName(ref, s.Target)
		out = append(out, Installed{
			Ref:     ref,
			Name:    name,
			Path:    path,
			Size:    info.Size(),
			Backend: catalog.BackendLlamaCPP,
			Managed: managed,
		})
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ref < out[j].Ref })
	return out, nil
}

// Prepare works out which file in the repository to use. A reference such as
// "org/repo:Q6_K" names a quantization rather than a file, so the repository
// is listed and the matching file chosen.
func (s *GGUFStore) Prepare(ctx context.Context, r catalog.Resolved) (catalog.Resolved, error) {
	if r.Artifact.File != "" {
		return r, nil
	}
	if r.Artifact.Quant == "" {
		return r, fmt.Errorf("%s does not name a file or a quantization; try %s:Q6_K", r.Artifact.Repo, r.Artifact.Repo)
	}

	files, err := s.client().Files(ctx, r.Artifact.Repo)
	if err != nil {
		return r, err
	}
	file, err := hf.MatchGGUF(files, r.Artifact.Quant)
	if err != nil {
		return r, fmt.Errorf("%s: %w", r.Artifact.Repo, err)
	}
	r.Artifact.File = file
	return r, nil
}

// Has reports whether the artifact's file is fully present.
func (s *GGUFStore) Has(_ context.Context, r catalog.Resolved) (bool, error) {
	info, err := os.Stat(s.PathFor(r))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return info.Size() > 0, nil
}

// Install downloads the artifact, resuming any interrupted transfer.
func (s *GGUFStore) Install(ctx context.Context, r catalog.Resolved, rep progress.Reporter) error {
	reporter := progress.Or(rep)

	if r.Artifact.File == "" {
		return fmt.Errorf("model %q does not name a GGUF file; use org/repo/file.gguf", r.Model.ID)
	}

	have, err := s.Has(ctx, r)
	if err != nil {
		return err
	}
	if have {
		reporter.Info(fmt.Sprintf("%s is already installed", r.Ref()))
		return nil
	}

	dest := s.PathFor(r)
	need := r.Artifact.Size
	if need == 0 {
		if size, err := s.client().Size(ctx, r.Artifact.Repo, r.Artifact.File); err == nil {
			need = size
		}
	}
	// Only the part still missing has to fit.
	if resumed := hf.PartialSize(dest); resumed > 0 && need > resumed {
		need -= resumed
		reporter.Info("resuming an interrupted download")
	}
	if err := EnsureSpace(s.Root, need); err != nil {
		return err
	}

	reporter.Step("Downloading " + r.Ref())
	return s.client().Download(ctx, r.Artifact.Repo, r.Artifact.File, dest, reporter)
}

// Delete removes one GGUF file and prunes directories left empty.
func (s *GGUFStore) Delete(ctx context.Context, ref string) error {
	installed, err := s.List(ctx)
	if err != nil {
		return err
	}
	for _, m := range installed {
		if m.Ref == ref {
			if err := os.Remove(m.Path); err != nil {
				return err
			}
			pruneEmptyParents(m.Path, s.Root)
			return nil
		}
	}
	return fmt.Errorf("%w: %s", ErrNotInstalled, ref)
}

// DiskUsage totals the whole store.
func (s *GGUFStore) DiskUsage(context.Context) (int64, error) { return dirSize(s.Root) }

func (s *GGUFStore) client() *hf.Client {
	if s.Client != nil {
		return s.Client
	}
	return hf.New()
}
