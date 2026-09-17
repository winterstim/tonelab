package main

import (
	"fmt"
	"strings"

	"tonelab/backend/app"
)

// renderResponse lays out one turn the way the window does: the answer,
// then what the DAW confirmed, then a plan if this was a preview, then the
// refusal if there was one. Colour carries the outcome.
func renderResponse(r app.AgentResponse) string {
	var out []string
	if r.Message != "" {
		out = append(out, theme.Text.Render(strings.TrimSpace(r.Message)))
	}
	for _, change := range r.Changed {
		out = append(out, theme.Success.Render("  ✓ "+describeChange(change)))
	}
	for _, step := range r.Plan {
		out = append(out, theme.Warning.Render("  ◇ would "+step.Description))
	}
	if len(r.Plan) > 0 {
		out = append(out, theme.Muted.Render("  /apply to carry it out, or say something else to drop it"))
	}
	if r.Error != nil {
		out = append(out, theme.Error.Render("  ✗ "+r.Error.Message)+theme.Muted.Render("  ("+r.Error.Code+")"))
	}
	return strings.Join(out, "\n")
}

func describeChange(c app.ParamChange) string {
	if c.NewValue == nil {
		return fmt.Sprintf("track %d %s: %v %s", c.Track, c.Param, format(c.Requested), theme.Muted.Render(c.Note))
	}
	return fmt.Sprintf("track %d %s: %v", c.Track, c.Param, format(c.NewValue))
}

func format(value any) string {
	switch v := value.(type) {
	case float64:
		return fmt.Sprintf("%.2f", v)
	case bool:
		if v {
			return "on"
		}
		return "off"
	}
	return fmt.Sprint(value)
}

func renderStatus(s app.DAWStatus) string {
	if s.Connected {
		return theme.Success.Render("● DAW connected") + theme.Muted.Render("  "+s.Detail)
	}
	return theme.Error.Render("○ DAW not answering") + theme.Muted.Render("  "+s.Detail)
}
