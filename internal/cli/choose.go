package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/Carlboms-Data-AB/myai/internal/app"
	"github.com/Carlboms-Data-AB/myai/internal/platform"
	"github.com/Carlboms-Data-AB/myai/internal/ui"
)

// modelChooser presents the models MyAI can install, with what each is for,
// and returns the ones to download. The first becomes the active model.
func modelChooser(c *ui.Console) app.ChooseModels {
	return func(offered []app.ModelEntry) ([]string, error) {
		if len(offered) == 0 {
			return nil, nil
		}

		c.Heading("MyAI · Choose a model")
		c.Line("  A model is downloaded once and runs entirely on this machine.")
		c.Blank()

		for i, m := range offered {
			state := ""
			if m.Installed {
				state = "   already downloaded"
			}
			c.Line(fmt.Sprintf("  %d  %-28s %10s   needs %s%s",
				i+1, m.Name, platform.HumanBytes(m.Size), m.NeedsRAM, state))
			if m.Summary != "" {
				c.Line("     " + m.Summary)
			}
			if m.GoodAt != "" {
				c.Line("     Good at: " + m.GoodAt)
			}
			c.Blank()
		}

		c.Line("  Pick one, or several separated by commas. The first becomes the one")
		c.Line("  MyAI uses; you can switch later under Models.")
		c.Blank()

		answer, err := c.Text("choose", "1")
		if err != nil {
			return nil, err
		}

		chosen, err := parseChoices(answer, len(offered))
		if err != nil {
			return nil, err
		}
		ids := make([]string, 0, len(chosen))
		for _, i := range chosen {
			ids = append(ids, offered[i].ID)
		}
		return ids, nil
	}
}

// parseChoices turns "1,3" into indexes, rejecting anything out of range or
// repeated.
func parseChoices(answer string, count int) ([]int, error) {
	fields := strings.FieldsFunc(answer, func(r rune) bool {
		return r == ',' || r == ' '
	})
	if len(fields) == 0 {
		return nil, fmt.Errorf("choose at least one model")
	}

	seen := make(map[int]bool, len(fields))
	out := make([]int, 0, len(fields))
	for _, f := range fields {
		n, err := strconv.Atoi(strings.TrimSpace(f))
		if err != nil {
			return nil, fmt.Errorf("%q is not a number", f)
		}
		if n < 1 || n > count {
			return nil, fmt.Errorf("choose numbers between 1 and %d", count)
		}
		if seen[n-1] {
			continue
		}
		seen[n-1] = true
		out = append(out, n-1)
	}
	return out, nil
}
