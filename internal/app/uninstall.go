package app

import (
	"context"
	"fmt"
	"os"

	"github.com/Carlboms-Data-AB/myai/internal/platform"
	"github.com/Carlboms-Data-AB/myai/internal/service"
)

// UninstallMode says how much to remove. Models are large and expensive to
// download again, so removing them is never implied.
type UninstallMode int

const (
	// UninstallKeepModels removes MyAI and leaves downloaded models alone.
	// This is the default.
	UninstallKeepModels UninstallMode = iota
	// UninstallWithModels removes MyAI and the models it downloaded.
	UninstallWithModels
	// UninstallModelsOnly removes downloaded models and leaves MyAI in place.
	UninstallModelsOnly
)

// UninstallPlan describes exactly what an uninstall would do, so it can be
// shown before anything is removed.
type UninstallPlan struct {
	// Mode is the chosen scope.
	Mode UninstallMode
	// Removes lists the paths and services that will be removed.
	Removes []string
	// Keeps lists what will deliberately be left behind.
	Keeps []string
	// ModelBytes is how much model data is at stake.
	ModelBytes int64
	// ModelLocation is where those models live.
	ModelLocation string
}

// PlanUninstall works out what an uninstall would remove without removing
// anything.
func (a *App) PlanUninstall(ctx context.Context, mode UninstallMode) (UninstallPlan, error) {
	store := a.Backend().Store()
	plan := UninstallPlan{Mode: mode, ModelLocation: store.Location()}

	if usage, err := store.DiskUsage(ctx); err == nil {
		plan.ModelBytes = usage
	}

	removesModels := mode == UninstallWithModels || mode == UninstallModelsOnly
	removesApp := mode != UninstallModelsOnly

	if removesApp {
		plan.Removes = append(plan.Removes,
			"service "+a.ServiceName(service.RoleInference),
			"service "+a.ServiceName(service.RoleWeb),
			a.env.Config,
			a.env.State,
			a.env.Executable(),
		)
		plan.Keeps = append(plan.Keeps, "the inference backend and OpenCode")
	}
	if removesModels {
		plan.Removes = append(plan.Removes,
			fmt.Sprintf("%s (%s of downloaded models)", store.Location(), platform.HumanBytes(plan.ModelBytes)),
		)
	} else {
		plan.Keeps = append(plan.Keeps,
			fmt.Sprintf("downloaded models in %s (%s)", store.Location(), platform.HumanBytes(plan.ModelBytes)),
		)
	}
	return plan, nil
}

// Uninstall removes MyAI according to the chosen mode. Model files are only
// touched when the mode explicitly says so.
func (a *App) Uninstall(ctx context.Context, mode UninstallMode) error {
	if mode != UninstallModelsOnly {
		if err := a.removeApp(ctx); err != nil {
			return err
		}
	}
	if mode == UninstallWithModels || mode == UninstallModelsOnly {
		if err := a.removeModels(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) removeApp(ctx context.Context) error {
	a.reporter.Step("Removing MyAI services")
	for _, role := range []string{service.RoleWeb, service.RoleInference} {
		name := a.ServiceName(role)
		if err := a.services.Remove(ctx, name); err != nil {
			a.reporter.Warn("could not remove service " + name + ": " + err.Error())
		}
	}

	a.reporter.Step("Removing MyAI files")
	// The model directories are never inside these, so this cannot reach a
	// downloaded model.
	for _, dir := range []string{a.env.Config, a.env.State} {
		if err := os.RemoveAll(dir); err != nil {
			return err
		}
	}
	if err := os.RemoveAll(a.env.ToolsDir()); err != nil {
		return err
	}
	if err := os.Remove(a.env.Executable()); err != nil && !os.IsNotExist(err) {
		return err
	}

	if err := a.removePathBlock(); err != nil {
		a.reporter.Warn("could not tidy the PATH entry: " + err.Error())
	}

	// Remove the data directory only if nothing but empty structure is left,
	// so a GGUF store outside the default location is never disturbed.
	if entries, err := os.ReadDir(a.env.Data); err == nil && len(entries) == 0 {
		os.Remove(a.env.Data)
	}
	return nil
}

func (a *App) removeModels(ctx context.Context) error {
	store := a.Backend().Store()
	location := store.Location()

	installed, err := store.List(ctx)
	if err != nil {
		return err
	}
	if len(installed) == 0 {
		a.reporter.Info("no downloaded models to remove")
		return nil
	}

	var total int64
	for _, m := range installed {
		total += m.Size
	}
	a.reporter.Step(fmt.Sprintf("Removing %d model(s), %s, from %s", len(installed), platform.HumanBytes(total), location))

	for _, m := range installed {
		if err := store.Delete(ctx, m.Ref); err != nil {
			return err
		}
		a.reporter.Info("removed " + m.Ref)
	}
	return nil
}

// removePathBlock strips the PATH entry MyAI added to a shell profile.
func (a *App) removePathBlock() error {
	profile := a.shellProfile()
	if profile == "" || !fileExists(profile) {
		return nil
	}
	body, err := os.ReadFile(profile)
	if err != nil {
		return err
	}
	cleaned := removeBlock(string(body))
	if cleaned == string(body) {
		return nil
	}
	return os.WriteFile(profile, []byte(cleaned+"\n"), 0o644)
}
