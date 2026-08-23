// Package models manages downloaded model artifacts. It presents one
// interface over two very different stores: the shared mlx-serve directory on
// Apple Silicon and a MyAI-owned GGUF directory everywhere else.
package models

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/Carlboms-Data-AB/myai/internal/catalog"
	"github.com/Carlboms-Data-AB/myai/internal/platform"
	"github.com/Carlboms-Data-AB/myai/internal/progress"
)

// ErrNotInstalled is returned when an operation needs a model that is absent.
var ErrNotInstalled = errors.New("model is not installed")

// Installed describes one model present on disk.
type Installed struct {
	// Ref identifies the artifact: "org/repo" for MLX, "org/repo/file.gguf"
	// for GGUF.
	Ref string
	// Name is what the user sees. It is the catalog name when the artifact is
	// recognised, otherwise the reference itself.
	Name string
	// Path is the file or directory holding the model.
	Path string
	// Size is the space it occupies in bytes.
	Size int64
	// Backend is the inference backend that can load it.
	Backend string
	// Managed marks artifacts that came from the MyAI catalog.
	Managed bool
}

// Store manages the model artifacts for one backend.
type Store interface {
	// Backend names the inference backend this store serves.
	Backend() string
	// Location is the directory holding the models.
	Location() string
	// List returns everything installed, sorted by reference.
	List(ctx context.Context) ([]Installed, error)
	// Has reports whether a resolved artifact is already on disk.
	Has(ctx context.Context, r catalog.Resolved) (bool, error)
	// Install downloads an artifact, doing nothing if it is already present.
	Install(ctx context.Context, r catalog.Resolved, rep progress.Reporter) error
	// Delete removes an installed artifact.
	Delete(ctx context.Context, ref string) error
	// DiskUsage totals the space used by the store.
	DiskUsage(ctx context.Context) (int64, error)
	// PathFor returns the on-disk location an artifact would occupy.
	PathFor(r catalog.Resolved) string
}

// EnsureSpace returns an error when a download would not fit, leaving a
// margin so the disk does not end up completely full.
func EnsureSpace(dir string, need int64) error {
	if need <= 0 {
		return nil
	}
	const margin = 2 << 30 // 2 GiB

	free, err := platform.FreeSpace(dir)
	if err != nil {
		// A machine that will not report free space should not block an
		// install the user asked for.
		return nil
	}
	if free < need+margin {
		return fmt.Errorf("not enough free space in %s: %s available, %s needed",
			dir, platform.HumanBytes(free), platform.HumanBytes(need+margin))
	}
	return nil
}

// dirSize totals the bytes used by a directory tree. A missing directory
// counts as zero rather than an error.
func dirSize(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		total += info.Size()
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	return total, err
}

// pruneEmptyParents removes directories left empty after a deletion, stopping
// at root so the store directory itself survives.
func pruneEmptyParents(path, root string) {
	for dir := filepath.Dir(path); dir != root && len(dir) > len(root); dir = filepath.Dir(dir) {
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) > 0 {
			return
		}
		if err := os.Remove(dir); err != nil {
			return
		}
	}
}

// displayName looks up the catalog name for a reference, falling back to the
// reference itself for models MyAI does not ship.
func displayName(ref, goos, goarch string) (string, bool) {
	for _, r := range catalog.Available(goos, goarch) {
		if r.Ref() == ref {
			return r.Label(), true
		}
	}
	return ref, false
}
