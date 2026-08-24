package app

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/Carlboms-Data-AB/myai/internal/catalog"
	"github.com/Carlboms-Data-AB/myai/internal/config"
	"github.com/Carlboms-Data-AB/myai/internal/models"
	"github.com/Carlboms-Data-AB/myai/internal/platform"
)

// ErrActiveModel is returned when an operation would remove the model MyAI is
// currently configured to use.
var ErrActiveModel = errors.New("that is the active model")

// ModelEntry describes one model for display, whether installed or offered.
type ModelEntry struct {
	// ID is the catalog id, or the reference for models outside the catalog.
	ID string
	// Name is the display name.
	Name string
	// Summary explains what the model is for.
	Summary string
	// Ref is the artifact reference on disk.
	Ref string
	// Backend is the inference backend that loads it.
	Backend string
	// Installed reports whether it is downloaded.
	Installed bool
	// Active reports whether it is the configured model.
	Active bool
	// Size is the on-disk size for installed models, or the download size for
	// ones that are not.
	Size int64
	// Path is where it lives on disk when installed.
	Path string
	// InCatalog reports whether MyAI ships this model.
	InCatalog bool
	// GoodAt says what the model is worth using for.
	GoodAt string
	// NeedsRAM is the memory it wants.
	NeedsRAM string
}

// ModelsView is the full picture of models on this machine.
type ModelsView struct {
	// Installed lists what is on disk.
	Installed []ModelEntry
	// Available lists what MyAI can install for this platform.
	Available []ModelEntry
	// Active is the reference of the configured model.
	Active string
	// Location is the model directory.
	Location string
	// DiskUsage is the total space models occupy.
	DiskUsage int64
	// FreeSpace is what remains on that filesystem.
	FreeSpace int64
}

// Models describes every model MyAI knows about on this machine.
func (a *App) Models(ctx context.Context) (ModelsView, error) {
	store := a.Backend().Store()

	view := ModelsView{Location: store.Location()}
	installed, err := store.List(ctx)
	if err != nil {
		return view, err
	}

	activeRef := ""
	if active, err := a.ActiveModel(); err == nil {
		activeRef = active.Ref()
	}
	view.Active = activeRef

	byRef := make(map[string]bool, len(installed))
	for _, m := range installed {
		byRef[m.Ref] = true
		view.Installed = append(view.Installed, ModelEntry{
			ID:        m.Ref,
			Name:      m.Name,
			Ref:       m.Ref,
			Backend:   m.Backend,
			Installed: true,
			Active:    m.Ref == activeRef,
			Size:      m.Size,
			Path:      m.Path,
			InCatalog: m.Managed,
		})
		view.DiskUsage += m.Size
	}

	for _, r := range catalog.Available(a.Target()) {
		view.Available = append(view.Available, ModelEntry{
			ID:        r.Model.ID,
			Name:      r.Label(),
			Summary:   r.Model.Summary,
			GoodAt:    r.Model.GoodAt,
			NeedsRAM:  r.Model.NeedsRAM,
			Ref:       r.Ref(),
			Backend:   r.Backend(),
			Installed: byRef[r.Ref()],
			Active:    r.Ref() == activeRef,
			Size:      r.Artifact.Size,
			InCatalog: true,
		})
	}
	sort.Slice(view.Available, func(i, j int) bool { return view.Available[i].ID < view.Available[j].ID })

	if free, err := platform.FreeSpace(store.Location()); err == nil {
		view.FreeSpace = free
	}
	return view, nil
}

// Offered returns the models MyAI can install on this machine, with their
// descriptions, for a caller that has to present a choice.
func (a *App) Offered(ctx context.Context) []ModelEntry {
	view, err := a.Models(ctx)
	if err != nil {
		return nil
	}
	return view.Available
}

// InstallModel downloads a model without changing which one is active.
func (a *App) InstallModel(ctx context.Context, id string) error {
	store := a.Backend().Store()

	resolved, err := a.resolveForStore(ctx, store, id)
	if err != nil {
		return err
	}
	return store.Install(ctx, resolved, a.reporter)
}

// resolveForStore turns a model id or reference into a fully settled artifact.
func (a *App) resolveForStore(ctx context.Context, store models.Store, id string) (catalog.Resolved, error) {
	resolved, err := catalog.Resolve(id, a.Target())
	if err != nil {
		return resolved, err
	}
	return store.Prepare(ctx, resolved)
}

// SelectModel makes a model active, downloading it first if necessary, then
// regenerates the OpenCode configuration and restarts the services.
func (a *App) SelectModel(ctx context.Context, id string) error {
	store := a.Backend().Store()

	resolved, err := a.resolveForStore(ctx, store, id)
	if err != nil {
		return err
	}

	have, err := store.Has(ctx, resolved)
	if err != nil {
		return err
	}
	if !have {
		if err := store.Install(ctx, resolved, a.reporter); err != nil {
			return err
		}
	}

	// A reference that named a quantization has now been settled to a file.
	// Record the settled form so nothing has to ask the network again.
	record := id
	if resolved.Artifact.Custom {
		record = resolved.Ref()
	}
	if err := a.Update(func(c *config.Config) { c.ActiveModel = record }); err != nil {
		return err
	}
	return a.Apply(ctx)
}

// DeleteModel removes a downloaded model. Deleting the active model is
// refused unless the caller confirms it deliberately, because doing so leaves
// the machine unable to serve anything.
func (a *App) DeleteModel(ctx context.Context, ref string, force bool) error {
	store := a.Backend().Store()

	installed, err := store.List(ctx)
	if err != nil {
		return err
	}
	var target *models.Installed
	for i := range installed {
		if installed[i].Ref == ref {
			target = &installed[i]
			break
		}
	}
	if target == nil {
		return fmt.Errorf("%w: %s", models.ErrNotInstalled, ref)
	}

	if active, err := a.ActiveModel(); err == nil && active.Ref() == ref && !force {
		return fmt.Errorf("%w: select another model first, or confirm the deletion", ErrActiveModel)
	}

	a.reporter.Step(fmt.Sprintf("Deleting %s (%s)", target.Name, platform.HumanBytes(target.Size)))
	return store.Delete(ctx, ref)
}

// catalogIDForRef finds the catalog id describing an artifact reference on any
// platform. It is how a model recorded by the Bash prototype is recognised.
func catalogIDForRef(ref string) (string, bool) {
	for _, m := range catalog.All() {
		for _, artifact := range m.Artifacts {
			if artifact.Repo == ref || artifact.Repo+"/"+artifact.File == ref {
				return m.ID, true
			}
		}
	}
	return "", false
}
