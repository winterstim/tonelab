package main

import (
	"fmt"
	"strings"
	"sync"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/styles"

	"tonelab/backend/app"
)

// renderResponse lays out one turn the way the window does: the answer,
// then what the DAW confirmed, then a plan if this was a preview, then the
// refusal if there was one. Colour carries the outcome.
func renderResponse(r app.AgentResponse) string {
	var out []string
	if r.Message != "" {
		out = append(out, markdown(strings.TrimSpace(r.Message), 0))
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

var (
	markdownFor map[int]*glamour.TermRenderer
	markdownMu  sync.Mutex
	// plain turns colour off in the markdown layout too, for a pipe or
	// NO_COLOR; the style library is told separately.
	plain bool
)

// markdown lays out the model's answer, which arrives as markdown, the
// way a terminal can show it: emphasis, lists and tables, no margins of
// its own. Width 0 means no wrapping, for a one-shot print. Falls back to
// the text itself if rendering fails, since an answer is never withheld
// over its formatting.
func markdown(text string, width int) string {
	markdownMu.Lock()
	defer markdownMu.Unlock()
	if markdownFor == nil {
		markdownFor = map[int]*glamour.TermRenderer{}
	}
	renderer, ok := markdownFor[width]
	if !ok {
		style := styles.DarkStyleConfig
		if plain {
			// The no-terminal style keeps markdown's own markers for
			// emphasis; a script reading a sentence wants the words.
			style = styles.NoTTYStyleConfig
			style.Strong.BlockPrefix, style.Strong.BlockSuffix = "", ""
			style.Emph.BlockPrefix, style.Emph.BlockSuffix = "", ""
		}
		none := uint(0)
		style.Document.Margin = &none
		style.Document.BlockPrefix, style.Document.BlockSuffix = "", ""
		style.Paragraph.BlockSuffix = ""
		if !plain {
			style.Document.Color = stringPtr("#E6F1FF")
			style.Strong.Color = stringPtr("#FF1F5A")
			style.Emph.Color = stringPtr("#FF7A1A")
			style.Code.BackgroundColor = nil
			style.Code.Color = stringPtr("#FFC857")
			style.Link.Color = stringPtr("#FFC400")
			style.Table.CenterSeparator = stringPtr("┼")
		}
		options := []glamour.TermRendererOption{glamour.WithStyles(style), glamour.WithEmoji()}
		if width > 0 {
			options = append(options, glamour.WithWordWrap(width))
		} else {
			options = append(options, glamour.WithWordWrap(0))
		}
		var err error
		renderer, err = glamour.NewTermRenderer(options...)
		if err != nil {
			return theme.Text.Render(text)
		}
		markdownFor[width] = renderer
	}
	rendered, err := renderer.Render(text)
	if err != nil {
		return theme.Text.Render(text)
	}
	return strings.TrimRight(rendered, "\n")
}

func stringPtr(s string) *string { return &s }
