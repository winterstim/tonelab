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

func TestStepsReadAsSentencesWithShortResults(t *testing.T) {
	cases := []struct {
		step         app.JournalStep
		call, result string
	}{
		{app.JournalStep{Tool: "get_param", Arguments: `{"track_id":1,"param_name":"volume"}`, Outcome: `{"value":0.30000001192092896}`}, "Read volume on track 1", "0.30"},
		{app.JournalStep{Tool: "list_tracks", Arguments: `{}`, Outcome: `{"value":[{"number":1,"name":"Guitar"}]}`}, "Looked up the tracks", "1 result"},
		{app.JournalStep{Tool: "find_params", Arguments: `{"track_id":1,"query":"reverb"}`, Outcome: `{"value":{"matches":[{},{}]}}`}, `Searched track 1 for "reverb"`, "2 matches"},
		{app.JournalStep{Tool: "set_fx_param", Arguments: `{"track_id":1,"fx_id":1,"param_id":90,"value":0.3}`, Outcome: `{"value":{"fx_name":"Amp","name":"Reverb","requested":0.3,"confirmed":0.3}}`}, "Set Amp / Reverb on track 1 to 0.30", "confirmed 0.30"},
		{app.JournalStep{Tool: "set_param", Arguments: `{"track_id":9,"param_name":"volume","value":0.5}`, Outcome: `{"error":{"code":"value_unknown","message":"The DAW has not reported that value."}}`, Failed: true}, "Set volume on track 9 to 0.50", "The DAW has not reported that value."},
		{app.JournalStep{Tool: "set_param", Arguments: `{"track_id":2,"param_name":"mute","value":true}`, Outcome: `{"value":{"would":"set track 2 mute to true"}}`}, "Set mute on track 2 to on", "would set track 2 mute to true"},
	}
	for _, tc := range cases {
		call, result := describeStep(tc.step)
		if call != tc.call || result != tc.result {
			t.Errorf("%s: got (%q, %q), want (%q, %q)", tc.step.Tool, call, result, tc.call, tc.result)
		}
	}
}

func TestMarkdownAnswersKeepTheirShape(t *testing.T) {
	out := markdown("Track 1 is **loud**.\n\n| n | name |\n|---|---|\n| 1 | Guitar |", 60)
	if !strings.Contains(out, "loud") || !strings.Contains(out, "Guitar") || strings.Contains(out, "**") || strings.Contains(out, "|---") {
		t.Fatalf("markdown should be laid out, not printed raw: %q", out)
	}
}

func TestExitCodesTellFailureFromUnverified(t *testing.T) {
	if exitCode(app.AgentResponse{Message: "ok"}) != 0 {
		t.Fatal("a clean turn exits 0")
	}
	if exitCode(app.AgentResponse{Error: &app.AgentError{Code: "llm_unreachable"}}) != 1 {
		t.Fatal("a failed turn exits 1")
	}
	if exitCode(app.AgentResponse{Changed: []app.ParamChange{{Track: 42, Param: "volume", Requested: 0.1, Note: "unverified"}}}) != 2 {
		t.Fatal("an unconfirmed change exits 2")
	}
}
