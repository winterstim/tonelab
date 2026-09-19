package main

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"tonelab/backend/agent"
	"tonelab/backend/app"
	"tonelab/backend/app/apptest"
	"tonelab/backend/config"
	"tonelab/backend/search"
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
	if !strings.Contains(refused, "✗ No model at that address.") || !strings.Contains(refused, "llm_unreachable") || !strings.Contains(refused, "255;77;255") {
		t.Fatalf("a refusal is a cross in the error colour with its code, got %q", refused)
	}
	unverified := renderResponse(app.AgentResponse{Changed: []app.ParamChange{{Track: 3, Param: "send", Requested: 0.2, Note: "the DAW does not report this parameter"}}})
	if !strings.Contains(unverified, "does not report") {
		t.Fatalf("an unverified change keeps its note, got %q", unverified)
	}
}

func TestGradientRunsSurfaceToDeep(t *testing.T) {
	if paletteAt(0) != fire[0] || paletteAt(1) != fire[len(fire)-1] {
		t.Fatal("the ends of the gradient are the ends of the palette")
	}
	if mid := paletteAt(0.5); !strings.EqualFold(mid, fire[2]) {
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

func TestSettingsLinesWithKeysAreNeverEchoedOrRecalled(t *testing.T) {
	if !secretLine("/settings key sk-abc") || !secretLine("/settings search_key BSA") {
		t.Fatal("a key line is secret")
	}
	if secretLine("/settings model gpt") || secretLine("/settings key") {
		t.Fatal("a model line, or a key line with no key, is not")
	}
}

func TestSettingFieldsParseTheirValues(t *testing.T) {
	var s app.Settings
	for _, f := range settingFields {
		switch f.name {
		case "port":
			if f.set(&s, "8000") != nil || s.DAWPort != 8000 || f.set(&s, "eight") == nil {
				t.Fatal("port takes a number and refuses a word")
			}
		case "preview":
			if f.set(&s, "on") != nil || !s.PreviewByDefault || f.set(&s, "maybe") == nil {
				t.Fatal("preview is on or off")
			}
		case "search":
			f.set(&s, "off")
			if s.SearchProvider != "" || f.get(s) != "off" {
				t.Fatal("search off is an empty provider shown as off")
			}
		}
	}
}

// hostedRuntime is the runtime a signed-out CLI stands on, pointed at the
// fake service: no DAW, since none of the account commands needs one.
func hostedRuntime(t *testing.T) (*app.Runtime, string, *string) {
	t.Helper()
	server, _ := apptest.FakeHosted(t)
	path := filepath.Join(t.TempDir(), "config.json")
	own := config.Config{LLM: config.LLM{BaseURL: "https://api.groq.com/openai/v1", APIKey: "gsk_mine", Model: "openai/gpt-oss-20b"}, DAW: config.DAW{Backend: "reaper", Host: "127.0.0.1", Port: 8000, FeedbackPort: 9000}, Hosted: config.Hosted{URL: server.URL}}
	if err := config.Save(path, own); err != nil {
		t.Fatal(err)
	}
	config.Load(path)
	live := agent.NewOrchestrator(agent.Config{BaseURL: own.LLM.BaseURL}, nil)
	previews := agent.NewOrchestrator(agent.Config{BaseURL: own.LLM.BaseURL}, nil)
	var opened string
	hosted := app.NewHostedService(path, live, previews, func(search.Provider) {}, func(url string) error { opened = url; return nil })
	return &app.Runtime{Agent: app.BuildAgentService(live, previews, nil, filepath.Join(filepath.Dir(path), "conversations.json")), Hosted: hosted, Settings: app.NewSettingsService(path, live, previews, func(search.Provider) {})}, path, &opened
}

// collect runs a command tree to its leaves and returns every message it
// produces, which is how a test sees what the prompt would have printed.
func collect(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range batch {
			out = append(out, collect(c)...)
		}
		return out
	}
	return []tea.Msg{msg}
}

// printed is the text of the messages with colour stripped, since the
// gradient paints every rune separately and a word would not match.
func printed(msgs []tea.Msg) string {
	var out strings.Builder
	for _, m := range msgs {
		out.WriteString(fmt.Sprint(m))
	}
	return ansi.ReplaceAllString(out.String(), "")
}

var ansi = regexp.MustCompile("\\x1b\\[[0-9;]*m")

func TestLoginShowsTheCodeThenSignsInAndLogoutUndoesIt(t *testing.T) {
	runtime, path, opened := hostedRuntime(t)
	settings, _ := config.Load(path)
	m := newModel(runtime, settings, path)

	if got := renderAccount(runtime); !strings.Contains(got, "not signed in") {
		t.Fatalf("before login: %q", got)
	}

	next, cmd := m.submit("/login")
	m = next.(model)
	msgs := collect(cmd)
	if out := printed(msgs); !strings.Contains(out, "ABCD-EFGH") || !strings.Contains(out, "http://x/device?code=ABCD-EFGH") {
		t.Fatalf("the code and the address are what the person needs, got %q", out)
	}
	if *opened != "http://x/device?code=ABCD-EFGH" {
		t.Fatalf("the browser is sent to the approval page, got %q", *opened)
	}
	var done *loginDone
	for _, msg := range msgs {
		if d, ok := msg.(loginDone); ok {
			done = &d
		}
	}
	if done == nil || !done.state.Done || done.state.Error != "" {
		t.Fatalf("the wait ends in an approved sign-in, got %+v", done)
	}

	next, cmd = m.Update(*done)
	m = next.(model)
	if out := printed(collect(cmd)); !strings.Contains(out, "signed in") || !strings.Contains(out, "ann@example.com") || !strings.Contains(out, "solo") {
		t.Fatalf("signing in reports the account, got %q", out)
	}
	if !m.settings.SignedIn() {
		t.Fatal("the model rereads the settings the sign-in rewrote")
	}
	if got := renderAccount(runtime); !strings.Contains(got, "this month") || !strings.Contains(got, "resets") {
		t.Fatalf("/account draws the windows, got %q", got)
	}

	next, cmd = m.submit("/logout")
	m = next.(model)
	if out := printed(collect(cmd)); !strings.Contains(out, "signed out") {
		t.Fatalf("got %q", out)
	}
	if m.settings.SignedIn() {
		t.Fatal("logout takes the subscription out of the settings the prompt shows")
	}
	if got := renderAccount(runtime); !strings.Contains(got, "not signed in") {
		t.Fatalf("after logout: %q", got)
	}
}

func TestThemesSwitchRememberAndRefuseUnknownNames(t *testing.T) {
	t.Cleanup(func() { theme = palettes[0] })
	runtime, path, _ := hostedRuntime(t)
	settings, _ := config.Load(path)
	m := newModel(runtime, settings, path)

	next, cmd := m.submit("/theme")
	m = next.(model)
	if out := printed(collect(cmd)); !strings.Contains(out, "fire") || !strings.Contains(out, "lagoon") || !strings.Contains(out, "emerald") || !strings.Contains(out, "white") {
		t.Fatalf("the list names every theme, got %q", out)
	}

	next, cmd = m.submit("/theme Lagoon")
	m = next.(model)
	if theme.Name != "lagoon" || !strings.Contains(printed(collect(cmd)), "now in lagoon") {
		t.Fatalf("switched to %s", theme.Name)
	}
	if !strings.Contains(m.input.Prompt, "92;240;230") {
		t.Fatalf("the prompt is repainted in the new palette, got %q", m.input.Prompt)
	}
	if strings.Count(gradient("tonelab"), "\x1b[") < 7 || paletteAt(1) != lagoon[len(lagoon)-1] {
		t.Fatal("the gradient runs the new palette")
	}

	theme = palettes[0]
	loadTheme(path)
	if theme.Name != "lagoon" {
		t.Fatalf("the choice survives a restart, got %s", theme.Name)
	}

	_, cmd = m.submit("/theme plaid")
	if out := printed(collect(cmd)); !strings.Contains(out, "no theme called plaid") || theme.Name != "lagoon" {
		t.Fatalf("an unknown name is refused and nothing changes, got %q", out)
	}

	if err := useTheme("white"); err != nil {
		t.Fatal(err)
	}
	if paletteAt(0) != "#FFFFFF" || paletteAt(0.5) != "#FFFFFF" || paletteAt(1) != "#FFFFFF" {
		t.Fatal("white is flat, not a gradient")
	}
	if !strings.Contains(theme.Success.Render("ok"), "60;220;151") || !strings.Contains(theme.Error.Render("no"), "255;77;255") {
		t.Fatal("outcome colours are the same in every theme")
	}
}
