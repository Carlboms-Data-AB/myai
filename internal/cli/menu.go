package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/carlbomsdata/myai/internal/app"
	"github.com/carlbomsdata/myai/internal/config"
	"github.com/carlbomsdata/myai/internal/platform"
	"github.com/carlbomsdata/myai/internal/ui"
)

// errQuit ends the interactive session.
var errQuit = errors.New("quit")

// MainMenu runs the interactive interface.
func MainMenu(ctx context.Context, a *app.App, c *ui.Console) error {
	for {
		c.Clear()
		c.Line("MYAI")
		c.Line("local coding agent  ·  " + a.Host().Label())
		c.Blank()
		c.Line("  1  OpenCode")
		c.Line("  2  OpenCode Web")
		c.Line("  3  Models")
		c.Line("  4  Runtime")
		c.Line("  5  Configure")
		c.Line("  6  Status")
		c.Line("  7  Test")
		c.Line("  8  Install / update")
		c.Line("  9  Restart services")
		c.Line(" 10  Uninstall")
		c.Line(" 11  Quit")
		c.Blank()

		choice, err := c.Text("choose", "11")
		if err != nil {
			return nil
		}

		err = mainChoice(ctx, a, c, choice)
		if errors.Is(err, errQuit) {
			return nil
		}
		if err != nil {
			c.Error(err)
		}
		c.Pause()
	}
}

func mainChoice(ctx context.Context, a *app.App, c *ui.Console, choice string) error {
	switch choice {
	case "1":
		dir, err := os.Getwd()
		if err != nil {
			return err
		}
		c.Line("Starting OpenCode in " + dir)
		return a.LaunchOpenCode(ctx, dir, nil)

	case "2":
		access, err := a.WebAccess(ctx)
		if err != nil {
			return err
		}
		renderWebAccess(c, access)
		return nil

	case "3":
		return modelsMenu(ctx, a, c)

	case "4":
		return runtimeMenu(ctx, a, c)

	case "5":
		return configureMenu(ctx, a, c)

	case "6":
		renderStatus(c, a.Status(ctx))
		return nil

	case "7":
		c.Heading("MyAI · Test")
		report := a.Test(ctx)
		renderTestSummary(c, report)
		return nil

	case "8":
		return a.Install(ctx, app.FullInstall())

	case "9":
		return a.Restart(ctx)

	case "10":
		return uninstallMenu(ctx, a, c)

	case "11", "q", "quit", "":
		return errQuit
	}
	return fmt.Errorf("choose a number between 1 and 11")
}

// --- models ---

func modelsMenu(ctx context.Context, a *app.App, c *ui.Console) error {
	for {
		c.Clear()
		c.Line("MYAI · Models")
		c.Blank()
		c.Line("  1  Installed models")
		c.Line("  2  Install model")
		c.Line("  3  Select active model")
		c.Line("  4  Delete model")
		c.Line("  5  Disk usage")
		c.Line("  6  Back")
		c.Blank()

		choice, err := c.Text("choose", "6")
		if err != nil {
			return nil
		}
		if choice == "6" || choice == "" {
			return nil
		}

		if err := modelsChoice(ctx, a, c, choice); err != nil {
			c.Error(err)
		}
		c.Pause()
	}
}

func modelsChoice(ctx context.Context, a *app.App, c *ui.Console, choice string) error {
	view, err := a.Models(ctx)
	if err != nil {
		return err
	}

	switch choice {
	case "1":
		renderModels(c, view)
		return nil

	case "2":
		id, err := pickModel(c, "Install which model?", view.Available, true)
		if err != nil {
			return err
		}
		return a.InstallModel(ctx, id)

	case "3":
		id, err := pickModel(c, "Which model should MyAI use?", view.Available, true)
		if err != nil {
			return err
		}
		return a.SelectModel(ctx, id)

	case "4":
		if len(view.Installed) == 0 {
			c.Line("No models are installed.")
			return nil
		}
		ref, err := pickInstalled(c, "Delete which model?", view.Installed)
		if err != nil {
			return err
		}
		return deleteModel(ctx, a, c, ref)

	case "5":
		return modelsCommand(ctx, a, c, []string{"usage"})
	}
	return fmt.Errorf("choose a number between 1 and 6")
}

// pickModel offers the catalog, with an option to type a reference directly.
func pickModel(c *ui.Console, question string, entries []app.ModelEntry, allowCustom bool) (string, error) {
	options := make([]string, 0, len(entries)+1)
	ids := make([]string, 0, len(entries)+1)

	for _, m := range entries {
		state := ""
		switch {
		case m.Active:
			state = "  (active)"
		case m.Installed:
			state = "  (installed)"
		}
		options = append(options, fmt.Sprintf("%-34s %10s%s", m.Name, platform.HumanBytes(m.Size), state))
		ids = append(ids, m.ID)
	}
	if allowCustom {
		options = append(options, "Enter a model reference")
		ids = append(ids, "")
	}

	index, err := c.Choose(question, options, 0)
	if err != nil {
		return "", err
	}
	if ids[index] != "" {
		return ids[index], nil
	}
	return c.Text("Model reference (org/repo, or org/repo/file.gguf)", "")
}

// pickInstalled offers what is on disk.
func pickInstalled(c *ui.Console, question string, entries []app.ModelEntry) (string, error) {
	options := make([]string, 0, len(entries))
	refs := make([]string, 0, len(entries))
	for _, m := range entries {
		state := ""
		if m.Active {
			state = "  (active)"
		}
		options = append(options, fmt.Sprintf("%-34s %10s%s", m.Name, platform.HumanBytes(m.Size), state))
		refs = append(refs, m.Ref)
	}
	index, err := c.Choose(question, options, 0)
	if err != nil {
		return "", err
	}
	return refs[index], nil
}

// --- runtime ---

func runtimeMenu(ctx context.Context, a *app.App, c *ui.Console) error {
	for {
		cfg := a.Config()
		c.Clear()
		c.Line("MYAI · Runtime")
		c.Blank()
		c.Field("1  Keep model in RAM", ui.YesNo(cfg.Inference.KeepInRAM))
		c.Field("2  Idle unload", idleLabel(cfg))
		c.Field("3  Acceleration", accelerationLabel(a, cfg))
		c.Field("4  Backend", backendLabel(a, cfg))
		c.Field("5  Skip memory pre-flight", ui.YesNo(cfg.Runtime.SkipMemoryCheck))
		c.Line("  6  Warm the model now")
		c.Line("  7  Back")
		c.Blank()

		choice, err := c.Text("choose", "7")
		if err != nil {
			return nil
		}
		if choice == "7" || choice == "" {
			return nil
		}
		if err := runtimeChoice(ctx, a, c, choice); err != nil {
			c.Error(err)
			c.Pause()
		}
	}
}

func runtimeChoice(ctx context.Context, a *app.App, c *ui.Console, choice string) error {
	switch choice {
	case "1":
		keep, err := c.Confirm("Keep the active model loaded in memory?", !a.Config().Inference.KeepInRAM)
		if err != nil {
			return err
		}
		if err := a.Update(func(cfg *config.Config) { cfg.Inference.KeepInRAM = keep }); err != nil {
			return err
		}
		return a.Apply(ctx)

	case "2":
		if a.Config().Inference.KeepInRAM {
			c.Line("Idle unload does not apply while the model is kept in RAM.")
			c.Pause()
			return nil
		}
		support := a.Backend().IdleUnload(ctx)
		if !support.Supported {
			c.Line("This backend cannot unload an idle model: " + support.Reason)
			c.Pause()
			return nil
		}
		minutes, err := askNumber(c, "Idle minutes before unloading, 0 to never unload", a.Config().Inference.IdleUnloadMinutes, 0, 1440)
		if err != nil {
			return err
		}
		if err := a.Update(func(cfg *config.Config) { cfg.Inference.IdleUnloadMinutes = minutes }); err != nil {
			return err
		}
		return a.Apply(ctx)

	case "3":
		if a.Host().SupportsMLX() {
			c.Line("MLX always uses Metal on Apple Silicon, so there is nothing to choose.")
			c.Pause()
			return nil
		}
		options := []string{
			"auto     let MyAI choose and verify it runs",
			"cpu      portable, no GPU required",
			"vulkan   NVIDIA, AMD or Intel GPU",
			"cuda     NVIDIA only",
		}
		values := []string{config.AccelerationAuto, config.AccelerationCPU, config.AccelerationVulkan, config.AccelerationCUDA}
		index, err := c.Choose("Which llama.cpp build should MyAI install?", options, 0)
		if err != nil {
			return err
		}
		if err := a.Update(func(cfg *config.Config) { cfg.Runtime.Acceleration = values[index] }); err != nil {
			return err
		}
		c.Line("Run Install / update to fetch the matching build.")
		c.Pause()
		return nil

	case "4":
		options := []string{"auto     the right backend for this machine", "mlx-serve", "llama.cpp"}
		values := []string{config.BackendAuto, config.BackendMLXServe, config.BackendLlamaCPP}
		index, err := c.Choose("Which inference backend?", options, 0)
		if err != nil {
			return err
		}
		if err := a.Update(func(cfg *config.Config) { cfg.Backend = values[index] }); err != nil {
			return err
		}
		return a.Apply(ctx)

	case "5":
		if !a.Host().SupportsMLX() {
			c.Line("Only mlx-serve has a memory pre-flight to skip.")
			c.Pause()
			return nil
		}
		c.Line("mlx-serve refuses to load a model when its own memory check says there is")
		c.Line("not enough room. That check is conservative and can be wrong, for instance")
		c.Line("when the page cache still holds the weights just read from disk.")
		skip, err := c.Confirm("Load the model even when the check objects?", !a.Config().Runtime.SkipMemoryCheck)
		if err != nil {
			return err
		}
		if err := a.Update(func(cfg *config.Config) { cfg.Runtime.SkipMemoryCheck = skip }); err != nil {
			return err
		}
		return a.Apply(ctx)

	case "6":
		if err := a.WarmModel(ctx); err != nil {
			return err
		}
		c.Line("The model is loaded.")
		c.Pause()
		return nil
	}
	return fmt.Errorf("choose a number between 1 and 7")
}

func idleLabel(cfg config.Config) string {
	if cfg.Inference.KeepInRAM {
		return "not applicable, the model is kept in RAM"
	}
	if cfg.Inference.IdleUnloadMinutes == 0 {
		return "never"
	}
	return strconv.Itoa(cfg.Inference.IdleUnloadMinutes) + " min"
}

func accelerationLabel(a *app.App, cfg config.Config) string {
	if a.Host().SupportsMLX() {
		return "Metal (MLX)"
	}
	return cfg.Runtime.Acceleration
}

func backendLabel(a *app.App, cfg config.Config) string {
	if cfg.Backend == config.BackendAuto {
		return "auto  (" + a.Backend().ID() + ")"
	}
	return cfg.Backend
}

// --- configure ---

func configureMenu(ctx context.Context, a *app.App, c *ui.Console) error {
	for {
		cfg := a.Config()
		c.Clear()
		c.Line("MYAI · Configure")
		c.Blank()
		c.Field("1  Context", strconv.Itoa(cfg.Inference.Context))
		c.Field("2  Output tokens", strconv.Itoa(cfg.Inference.Output))
		c.Field("3  Inference port", strconv.Itoa(cfg.Inference.Port))
		c.Field("4  OpenCode Web", ui.EnabledDisabled(cfg.Web.Enabled))
		c.Field("5  Web UI port", strconv.Itoa(cfg.Web.Port))
		c.Field("6  Web UI bind address", cfg.Web.Host)
		c.Field("7  Web search", ui.EnabledDisabled(cfg.Tools.WebSearch))
		c.Field("8  Browser automation", ui.EnabledDisabled(cfg.Tools.BrowserAutomation))
		c.Line("  9  Rotate the Web UI password")
		c.Line(" 10  Back")
		c.Blank()

		choice, err := c.Text("choose", "10")
		if err != nil {
			return nil
		}
		if choice == "10" || choice == "" {
			return nil
		}
		if err := configureChoice(ctx, a, c, choice); err != nil {
			c.Error(err)
			c.Pause()
		}
	}
}

func configureChoice(ctx context.Context, a *app.App, c *ui.Console, choice string) error {
	cfg := a.Config()

	switch choice {
	case "1":
		value, err := askNumber(c, "Context tokens", cfg.Inference.Context, 4096, 1048576)
		if err != nil {
			return err
		}
		return applyChange(ctx, a, func(cfg *config.Config) { cfg.Inference.Context = value })

	case "2":
		value, err := askNumber(c, "Output tokens", cfg.Inference.Output, 256, 262144)
		if err != nil {
			return err
		}
		return applyChange(ctx, a, func(cfg *config.Config) { cfg.Inference.Output = value })

	case "3":
		value, err := askNumber(c, "Inference API port", cfg.Inference.Port, 1024, 65535)
		if err != nil {
			return err
		}
		return applyChange(ctx, a, func(cfg *config.Config) { cfg.Inference.Port = value })

	case "4":
		enabled, err := c.Confirm("Run the OpenCode Web interface?", !cfg.Web.Enabled)
		if err != nil {
			return err
		}
		return applyChange(ctx, a, func(cfg *config.Config) { cfg.Web.Enabled = enabled })

	case "5":
		value, err := askNumber(c, "Web UI port", cfg.Web.Port, 1024, 65535)
		if err != nil {
			return err
		}
		return applyChange(ctx, a, func(cfg *config.Config) { cfg.Web.Port = value })

	case "6":
		options := []string{
			"0.0.0.0     reachable from other machines, password required",
			"127.0.0.1   this machine only",
		}
		index, err := c.Choose("Where should the Web UI listen?", options, 0)
		if err != nil {
			return err
		}
		host := "0.0.0.0"
		if index == 1 {
			host = "127.0.0.1"
		}
		return applyChange(ctx, a, func(cfg *config.Config) { cfg.Web.Host = host })

	case "7":
		enabled, err := c.Confirm("Allow web search and web fetching?", !cfg.Tools.WebSearch)
		if err != nil {
			return err
		}
		if enabled {
			c.Line("Searches go to Exa and fetches reach the requested site. Inference stays local.")
		}
		return applyChange(ctx, a, func(cfg *config.Config) { cfg.Tools.WebSearch = enabled })

	case "8":
		return toggleBrowser(ctx, a, c)

	case "9":
		creds, err := a.RotateWebPassword()
		if err != nil {
			return err
		}
		c.Line("New password: " + creds.Password)
		if err := a.Restart(ctx); err != nil {
			return err
		}
		c.Pause()
		return nil
	}
	return fmt.Errorf("choose a number between 1 and 10")
}

// toggleBrowser switches the optional browser automation skill, installing it
// on first use.
func toggleBrowser(ctx context.Context, a *app.App, c *ui.Console) error {
	cfg := a.Config()
	if cfg.Tools.BrowserAutomation {
		return applyChange(ctx, a, func(cfg *config.Config) { cfg.Tools.BrowserAutomation = false })
	}

	c.Line("Browser automation uses a real browser and its signed-in sessions.")
	confirmed, err := c.Confirm("Enable it?", false)
	if err != nil || !confirmed {
		return nil
	}
	if err := a.InstallBrowserSkill(ctx); err != nil {
		return err
	}
	return applyChange(ctx, a, func(cfg *config.Config) { cfg.Tools.BrowserAutomation = true })
}

// applyChange saves a configuration change and puts it into effect.
func applyChange(ctx context.Context, a *app.App, mutate func(*config.Config)) error {
	if err := a.Update(mutate); err != nil {
		return err
	}
	return a.Apply(ctx)
}

// askNumber reads a bounded number, keeping the current value on an empty
// answer.
func askNumber(c *ui.Console, question string, current, min, max int) (int, error) {
	answer, err := c.Text(fmt.Sprintf("%s (%d-%d)", question, min, max), strconv.Itoa(current))
	if err != nil {
		return 0, err
	}
	value, err := strconv.Atoi(strings.TrimSpace(answer))
	if err != nil {
		return 0, fmt.Errorf("%q is not a number", answer)
	}
	if value < min || value > max {
		return 0, fmt.Errorf("choose a value between %d and %d", min, max)
	}
	return value, nil
}

// --- uninstall ---

func uninstallMenu(ctx context.Context, a *app.App, c *ui.Console) error {
	c.Clear()
	c.Line("MYAI · Uninstall")
	c.Blank()

	options := []string{
		"Uninstall MyAI, keep downloaded models",
		"Uninstall MyAI and delete models",
		"Delete downloaded models only",
		"Cancel",
	}
	index, err := c.Choose("What should be removed?", options, 0)
	if err != nil {
		return err
	}

	switch index {
	case 0:
		if err := runUninstall(ctx, a, c, app.UninstallKeepModels); err != nil {
			return err
		}
		return errQuit
	case 1:
		if err := runUninstall(ctx, a, c, app.UninstallWithModels); err != nil {
			return err
		}
		return errQuit
	case 2:
		return runUninstall(ctx, a, c, app.UninstallModelsOnly)
	}
	return nil
}
