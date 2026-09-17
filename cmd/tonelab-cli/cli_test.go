package main

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"tonelab/backend/app"
)

func init() {
	// Colour codes on, whatever the test runner's stdout is, so the
	// assertions below see what a terminal sees.
	lipgloss.SetColorProfile(termenv.TrueColor)
}

// Runes typed or pasted together must all land: the text field matches
// keys by name, and a batch spelling "up" is otherwise taken for the arrow.
func TestPastedRunesAllLand(t *testing.T) {
	input := textinput.New()
	input.Focus()
	input, _ = typeInto(input, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("turn it up")})
	if input.Value() != "turn it up" {
		t.Fatalf("got %q", input.Value())
	}
}

func TestResponseCarriesOutcomeInColour(t *testing.T) {
	ok := renderResponse(app.AgentResponse{Message: "Done.", Changed: []app.ParamChange{{Track: 1, Param: "volume", Requested: 0.5, NewValue: 0.5}}})
	if !strings.Contains(ok, "✓ track 1 volume: 0.50") || !strings.Contains(ok, "#3DDC97") && !strings.Contains(ok, "60;220;151") {
		t.Fatalf("a confirmed change is a green tick, got %q", ok)
	}
	plan := renderResponse(app.AgentResponse{Plan: []app.PlannedCall{{Description: "set track 2 mute to true"}}})
	if !strings.Contains(plan, "◇ would set track 2 mute to true") || !strings.Contains(plan, "/apply") {
		t.Fatalf("a plan says what it would do and how to apply it, got %q", plan)
	}
	refused := renderResponse(app.AgentResponse{Error: &app.AgentError{Code: "llm_unreachable", Message: "No model at that address."}})
	if !strings.Contains(refused, "✗ No model at that address.") || !strings.Contains(refused, "llm_unreachable") || !strings.Contains(refused, "255;107;107") {
		t.Fatalf("a refusal is a red cross with its code, got %q", refused)
	}
	unverified := renderResponse(app.AgentResponse{Changed: []app.ParamChange{{Track: 3, Param: "send", Requested: 0.2, Note: "the DAW does not report this parameter"}}})
	if !strings.Contains(unverified, "does not report") {
		t.Fatalf("an unverified change keeps its note, got %q", unverified)
	}
}

func TestGradientRunsSurfaceToDeep(t *testing.T) {
	if lagoonAt(0) != lagoon[0] || lagoonAt(1) != lagoon[len(lagoon)-1] {
		t.Fatal("the ends of the gradient are the ends of the palette")
	}
	if mid := lagoonAt(0.5); !strings.EqualFold(mid, lagoon[2]) {
		t.Fatalf("the middle of five stops is the third, got %s", mid)
	}
	if mix("#000000", "#ffffff", 0.5) != "#7f7f7f" {
		t.Fatalf("mix: %s", mix("#000000", "#ffffff", 0.5))
	}
	painted := gradient("tonelab")
	if strings.Count(painted, "\x1b[") < 7 {
		t.Fatalf("every rune gets its own colour, got %q", painted)
	}
}
