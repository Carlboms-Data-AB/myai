package cli

import (
	"fmt"

	"github.com/Carlboms-Data-AB/myai/internal/app"
	"github.com/Carlboms-Data-AB/myai/internal/platform"
	"github.com/Carlboms-Data-AB/myai/internal/ui"
)

// renderStatus prints the full status report.
func renderStatus(c *ui.Console, s app.Status) {
	c.Heading("MyAI")

	c.Field("MyAI", s.Version+" on "+s.Platform)
	c.Field("OpenCode", s.OpenCode.Summary())
	c.Field("inference backend", s.Backend.Name+"  "+s.Backend.Summary())
	c.Blank()

	c.Field("active model", s.Model)
	c.Field("model files", installedLabel(s.ModelInstalled)+"  "+s.ModelRef)
	c.Field("model in memory", s.ModelResidency)
	c.Field("keep in RAM", ui.YesNo(s.KeepInRAM))
	c.Field("idle unload", s.IdleUnload)
	c.Blank()

	c.Field("inference API", reachableLabel(s.APIReachable, s.API))
	c.Field("inference service", s.InferenceService.Summary())
	c.Blank()

	if s.WebEnabled {
		c.Field("OpenCode Web", s.WebService.Summary())
		c.Field("web UI address", reachableLabel(s.WebReachable, s.WebURL))
		c.Field("web UI exposure", exposureLabel(s.WebExposed))
	} else {
		c.Field("OpenCode Web", "disabled")
	}
	c.Field("web search", ui.EnabledDisabled(s.WebSearch))
	c.Field("browser automation", browserLabel(s))
	c.Blank()
}

func installedLabel(installed bool) string {
	if installed {
		return "installed  "
	}
	return "missing    "
}

func reachableLabel(ok bool, address string) string {
	if ok {
		return address
	}
	return address + "  (unreachable)"
}

func exposureLabel(exposed bool) string {
	if exposed {
		return "reachable from the network, password required"
	}
	return "loopback only"
}

func browserLabel(s app.Status) string {
	if !s.BrowserAutomation {
		return "disabled"
	}
	if s.BrowserReady {
		return "enabled"
	}
	return "enabled but ego-browser is not installed"
}

// renderTestSummary prints the closing line of a check run. The individual
// checks are already reported as they happen.
func renderTestSummary(c *ui.Console, report app.TestReport) {
	c.Blank()
	if report.Passed() {
		c.Line("All checks passed.")
		return
	}
	c.Line(fmt.Sprintf("%d check(s) failed:", len(report.Failures())))
	for _, f := range report.Failures() {
		c.Field(f.Name, f.Detail)
	}
}

// renderWebAccess prints how to reach the Web UI from another machine.
func renderWebAccess(c *ui.Console, access app.WebAccess) {
	c.Heading("OpenCode Web")

	if !access.Enabled {
		c.Field("state", "disabled")
		c.Blank()
		return
	}
	if access.Password == "" {
		c.Field("state", "MyAI is not installed on this machine yet")
		c.Line("  Run: myai install")
		c.Blank()
		return
	}
	c.Field("URL", access.URL)
	c.Field("username", access.Username)
	c.Field("password", access.Password)
	c.Field("state", reachableState(access.Reachable))
	if !access.Exposed {
		c.Field("note", "bound to loopback only, so it is not reachable from another machine")
	}
	c.Blank()
}

func reachableState(ok bool) string {
	if ok {
		return "running"
	}
	return "not answering"
}

// renderModels prints the installed and available models.
func renderModels(c *ui.Console, view app.ModelsView) {
	c.Heading("MyAI · Models")

	c.Field("location", view.Location)
	c.Field("disk usage", platform.HumanBytes(view.DiskUsage))
	c.Field("free space", platform.HumanBytes(view.FreeSpace))
	c.Blank()

	if len(view.Installed) == 0 {
		c.Line("  No models installed.")
	} else {
		c.Line("  Installed")
		for _, m := range view.Installed {
			marker := "  "
			if m.Active {
				marker = "->"
			}
			c.Line(fmt.Sprintf("  %s %-34s %10s  %s", marker, m.Name, platform.HumanBytes(m.Size), m.Ref))
		}
	}
	c.Blank()

	c.Line("  Available")
	for _, m := range view.Available {
		state := "not installed"
		if m.Installed {
			state = "installed"
		}
		if m.Active {
			state = "active"
		}
		c.Line(fmt.Sprintf("     %-24s %-34s %10s  %s", m.ID, m.Name, platform.HumanBytes(m.Size), state))
	}
	c.Blank()
}

// renderUninstallPlan shows exactly what an uninstall would do.
func renderUninstallPlan(c *ui.Console, plan app.UninstallPlan) {
	c.Heading("This will remove")
	for _, item := range plan.Removes {
		c.Line("  - " + item)
	}
	if len(plan.Keeps) > 0 {
		c.Heading("This will keep")
		for _, item := range plan.Keeps {
			c.Line("  - " + item)
		}
	}
	c.Blank()
}
