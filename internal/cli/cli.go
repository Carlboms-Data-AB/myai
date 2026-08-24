// Package cli is MyAI's command line. It parses arguments, calls the core in
// internal/app and renders the results. All behaviour lives in the core; this
// package only decides what to call and how to display the answer.
package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/Carlboms-Data-AB/myai/internal/app"
	"github.com/Carlboms-Data-AB/myai/internal/ui"
)

// Run executes a MyAI command line and returns the process exit code.
func Run(ctx context.Context, args []string) int {
	console := ui.NewConsole()

	instance, err := app.New(app.Options{Reporter: console, Asker: console})
	if err != nil {
		console.Error(err)
		return 1
	}
	if console.Interactive() {
		// With somebody there to ask, install offers the model choice rather
		// than silently downloading whatever happens to be configured.
		instance.SetModelChooser(modelChooser(console))
	}

	command := ""
	rest := []string(nil)
	if len(args) > 0 {
		command = args[0]
		rest = args[1:]
	}

	if err := dispatch(ctx, instance, console, command, rest); err != nil {
		if errors.Is(err, errUsage) {
			console.Line(usage())
			return 2
		}
		console.Error(err)
		return 1
	}
	return 0
}

var errUsage = errors.New("usage")

func dispatch(ctx context.Context, a *app.App, c *ui.Console, command string, args []string) error {
	switch command {
	case "":
		if c.Interactive() {
			return MainMenu(ctx, a, c)
		}
		// Without a terminal, report rather than wait for input nobody can
		// give.
		renderStatus(c, a.Status(ctx))
		return nil

	case "status":
		renderStatus(c, a.Status(ctx))
		return nil

	case "test":
		report := a.Test(ctx)
		renderTestSummary(c, report)
		if !report.Passed() {
			return fmt.Errorf("%d check(s) failed", len(report.Failures()))
		}
		return nil

	case "web":
		access, err := a.WebAccess(ctx)
		if err != nil {
			return err
		}
		renderWebAccess(c, access)
		return nil

	case "restart":
		return a.Restart(ctx)

	case "start":
		return a.Start(ctx)

	case "stop":
		return a.Stop(ctx)

	case "models":
		return modelsCommand(ctx, a, c, args)

	case "install", "update":
		return a.Install(ctx, app.FullInstall())

	case "upgrade":
		return a.Install(ctx, app.UpgradeOnly())

	case "uninstall":
		return uninstallCommand(ctx, a, c, args)

	case "opencode", "launch":
		dir, err := os.Getwd()
		if err != nil {
			return err
		}
		return a.LaunchOpenCode(ctx, dir, args)

	case "serve-web":
		// Invoked by the background service. It runs in the foreground and
		// does not return until OpenCode Web exits.
		return a.ServeWeb(ctx)

	case "version", "--version", "-v":
		c.Line("myai " + app.Version)
		return nil

	case "help", "--help", "-h":
		c.Line(usage())
		return nil

	default:
		return fmt.Errorf("unknown command %q\n\n%s", command, usage())
	}
}

func usage() string {
	return strings.TrimSpace(`
myai - local coding agent stack

  myai                    open the interactive menu
  myai status             show what is running
  myai test               run the built-in checks
  myai web                show OpenCode Web access details
  myai opencode [args]    launch OpenCode in the current directory
  myai models             manage models
  myai restart            restart the background services
  myai start | stop       start or stop the background services
  myai install            install or update MyAI
  myai upgrade            update MyAI without touching models
  myai uninstall          remove MyAI
  myai version            print the version

  myai models list                 list installed models
  myai models available            list models MyAI can install
  myai models install <id>         download a model
  myai models select <id>          make a model active
  myai models delete <ref>         delete a downloaded model
  myai models usage                show model disk usage

  myai uninstall                   remove MyAI, keep models
  myai uninstall --with-models     remove MyAI and downloaded models
  myai uninstall --models-only     remove downloaded models only
`)
}
