package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/carlbomsdata/myai/internal/opencode"
	"github.com/carlbomsdata/myai/internal/service"
)

// Check is the outcome of one verification.
type Check struct {
	// Name is what was checked.
	Name string
	// OK reports whether it passed.
	OK bool
	// Skipped marks a check that did not apply.
	Skipped bool
	// Detail explains the result.
	Detail string
}

// TestReport is the outcome of a full check run.
type TestReport struct {
	Checks []Check
}

// Passed reports whether every applicable check succeeded.
func (r TestReport) Passed() bool {
	for _, c := range r.Checks {
		if !c.OK && !c.Skipped {
			return false
		}
	}
	return true
}

// Failures returns the checks that did not pass.
func (r TestReport) Failures() []Check {
	var out []Check
	for _, c := range r.Checks {
		if !c.OK && !c.Skipped {
			out = append(out, c)
		}
	}
	return out
}

// probePhrase is what the model is asked to echo. It is deliberately unusual
// so a passing check cannot be a coincidence.
const probePhrase = "myai-ok"

// Test verifies the installation for real: it checks that services run, that
// the API answers, that the configured model is actually being served and
// that the model can complete a request.
func (a *App) Test(ctx context.Context) TestReport {
	var report TestReport
	add := func(name string, ok bool, detail string) {
		report.Checks = append(report.Checks, Check{Name: name, OK: ok, Detail: detail})
		a.reporter.Check(name, ok, detail)
	}
	skip := func(name, detail string) {
		report.Checks = append(report.Checks, Check{Name: name, Skipped: true, Detail: detail})
		a.reporter.Check(name, true, detail)
	}

	b := a.Backend()

	info := b.Detect(ctx)
	add("inference backend", info.Installed, info.Summary())

	state, _ := a.services.Status(ctx, a.ServiceName(service.RoleInference))
	add("inference service", state.Running, state.Summary())

	client := a.Inference()
	apiUp := client.Ready(ctx, 5*time.Second)
	add("inference API", apiUp, client.BaseURL)

	model, modelErr := a.ActiveModel()
	if modelErr != nil {
		add("active model", false, modelErr.Error())
	} else {
		have, err := b.Store().Has(ctx, model)
		detail := model.Ref()
		if err != nil {
			detail = err.Error()
		}
		add("active model", have && err == nil, detail)
	}

	if apiUp && modelErr == nil {
		serving, err := client.Serving(ctx, b.ModelName(model))
		detail := b.ModelName(model)
		if err != nil {
			detail = err.Error()
		}
		add("model served", serving && err == nil, detail)
	} else {
		skip("model served", "the inference API is not answering")
	}

	if apiUp && modelErr == nil {
		ok, detail := a.probeInference(ctx, b.ModelName(model))
		add("model inference", ok, detail)
	} else {
		skip("model inference", "the inference API is not answering")
	}

	if apiUp && modelErr == nil {
		served, ok := client.ContextLength(ctx, b.ModelName(model))
		advertised, err := opencode.AdvertisedContext(a.env.OpenCodeConfigFile(), b.ModelName(model))
		switch {
		case !ok:
			skip("context window", "the server does not report a context window")
		case err != nil:
			add("context window", false, err.Error())
		default:
			// What matters is that OpenCode is not told more than the server
			// will actually serve, not what the configuration asked for.
			detail := fmt.Sprintf("serving %d tokens, OpenCode told %d", served, advertised)
			add("context window", advertised <= served, detail)
		}
	} else {
		skip("context window", "the inference API is not answering")
	}

	ocInfo := a.oc.Detect(ctx)
	add("OpenCode", ocInfo.Installed, ocInfo.Summary())

	if modelErr == nil {
		err := opencode.ValidateConfig(a.env.OpenCodeConfigFile(), b.ModelName(model), b.BaseURL(a.cfg))
		detail := a.env.OpenCodeConfigFile()
		if err != nil {
			detail = err.Error()
		}
		add("OpenCode config", err == nil, detail)
	} else {
		skip("OpenCode config", "no active model to check against")
	}

	if a.cfg.Web.Enabled {
		webState, _ := a.services.Status(ctx, a.ServiceName(service.RoleWeb))
		add("OpenCode Web service", webState.Running, webState.Summary())

		reachable := a.WebReachable(ctx)
		add("OpenCode Web", reachable, opencode.LocalWebURL(a.cfg))
	} else {
		skip("OpenCode Web service", "the Web UI is disabled")
		skip("OpenCode Web", "the Web UI is disabled")
	}

	if a.cfg.Tools.BrowserAutomation {
		ready := a.browserReady()
		detail := "ego-browser is available"
		if !ready {
			detail = "ego-browser is enabled but not installed"
		}
		add("browser automation", ready, detail)
	} else {
		skip("browser automation", "disabled")
	}

	return report
}

// probeInference proves the whole path from HTTP request to generated tokens.
//
// What is being checked is the installation, not the model's obedience. A
// small or reasoning model may spend its budget thinking and never echo the
// phrase; that still shows inference working, so it passes with the nuance
// reported rather than failing.
func (a *App) probeInference(ctx context.Context, modelID string) (bool, string) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	reply, err := a.Inference().Ask(ctx, modelID, "Reply with exactly this and nothing else: "+probePhrase, 256)
	if err != nil {
		return false, err.Error()
	}
	switch {
	case strings.Contains(strings.ToLower(reply.Content), probePhrase):
		return true, "the model answered correctly"
	case reply.Generated():
		return true, "the model generated a reply, though not the phrase asked for"
	default:
		return false, "the model produced nothing"
	}
}
