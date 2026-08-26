package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/carlbomsdata/myai/internal/app"
	"github.com/carlbomsdata/myai/internal/platform"
	"github.com/carlbomsdata/myai/internal/ui"
)

// modelsCommand handles "myai models ...".
func modelsCommand(ctx context.Context, a *app.App, c *ui.Console, args []string) error {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
		args = args[1:]
	}

	switch sub {
	case "", "list", "available":
		view, err := a.Models(ctx)
		if err != nil {
			return err
		}
		renderModels(c, view)
		return nil

	case "install":
		if len(args) == 0 {
			return errors.New("which model? try: myai models install qwen3.5-9b")
		}
		return a.InstallModel(ctx, args[0])

	case "select", "use":
		if len(args) == 0 {
			return errors.New("which model? try: myai models select qwen3.5-9b")
		}
		return a.SelectModel(ctx, args[0])

	case "delete", "remove":
		if len(args) == 0 {
			return errors.New("which model? run: myai models list")
		}
		return deleteModel(ctx, a, c, args[0])

	case "usage", "disk":
		view, err := a.Models(ctx)
		if err != nil {
			return err
		}
		c.Heading("MyAI · Model disk usage")
		c.Field("location", view.Location)
		c.Field("used by models", platform.HumanBytes(view.DiskUsage))
		c.Field("free on disk", platform.HumanBytes(view.FreeSpace))
		c.Blank()
		for _, m := range view.Installed {
			c.Field(m.Name, platform.HumanBytes(m.Size))
		}
		c.Blank()
		return nil

	default:
		return fmt.Errorf("unknown models command %q", sub)
	}
}

// deleteModel removes a model, refusing to remove the active one until the
// operator confirms it deliberately.
func deleteModel(ctx context.Context, a *app.App, c *ui.Console, ref string) error {
	err := a.DeleteModel(ctx, ref, false)
	if !errors.Is(err, app.ErrActiveModel) {
		return err
	}

	c.Blank()
	c.Line(ref + " is the model MyAI is configured to use.")
	c.Line("Deleting it leaves nothing to serve until another model is selected.")

	confirmed, askErr := c.Confirm("Delete it anyway?", false)
	if askErr != nil || !confirmed {
		return errors.New("cancelled")
	}
	return a.DeleteModel(ctx, ref, true)
}

// uninstallCommand handles "myai uninstall".
func uninstallCommand(ctx context.Context, a *app.App, c *ui.Console, args []string) error {
	mode := app.UninstallKeepModels
	for _, arg := range args {
		switch strings.ToLower(arg) {
		case "--with-models":
			mode = app.UninstallWithModels
		case "--models-only":
			mode = app.UninstallModelsOnly
		default:
			return fmt.Errorf("unknown option %q", arg)
		}
	}
	return runUninstall(ctx, a, c, mode)
}

// runUninstall shows the plan and asks before removing anything.
func runUninstall(ctx context.Context, a *app.App, c *ui.Console, mode app.UninstallMode) error {
	plan, err := a.PlanUninstall(ctx, mode)
	if err != nil {
		return err
	}
	renderUninstallPlan(c, plan)

	if mode == app.UninstallWithModels || mode == app.UninstallModelsOnly {
		// Model data is expensive to replace, so this needs a deliberate
		// answer rather than a keystroke.
		c.Line(fmt.Sprintf("This deletes %s of downloaded models from %s.",
			platform.HumanBytes(plan.ModelBytes), plan.ModelLocation))
		answer, err := c.Text("Type DELETE MODELS to confirm", "")
		if err != nil {
			return err
		}
		if answer != "DELETE MODELS" {
			return errors.New("cancelled")
		}
	} else {
		confirmed, err := c.Confirm("Continue?", false)
		if err != nil || !confirmed {
			return errors.New("cancelled")
		}
	}
	return a.Uninstall(ctx, mode)
}
