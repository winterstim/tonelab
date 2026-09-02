//go:build llm && reaper

// The whole product in one test, needing both a model and a DAW:
//
//	go test -tags "llm reaper" -count=1 -p 1 ./backend/agent/...
package agent_test

import (
	"testing"
	"time"

	"tonelab/backend/agent"
	"tonelab/backend/config"
	"tonelab/backend/daw"
	"tonelab/backend/osc"
)

// Done means: a free-text command makes a real REAPER
// reflect the new value. Nothing else in the suite asserts that, because
// nothing else has every layer present at once.
func TestFreeTextCommandChangesREAPER(t *testing.T) {
	llm := liveConfig(t)

	listener, err := osc.Listen("127.0.0.1", 9000)
	if err != nil {
		t.Fatalf("could not listen for REAPER's feedback: %v", err)
	}
	defer listener.Close()

	reaper := daw.NewREAPER(osc.NewTransport("127.0.0.1", 8000))
	reaper.Observe(listener.Messages())

	orchestrator := agent.NewOrchestrator(
		agent.Config{BaseURL: llm.BaseURL, APIKey: llm.APIKey, Model: llm.Model},
		agent.NewTools(reaper),
	)

	response := orchestrator.Send("Set the volume of track 1 to one quarter.")
	if response.Error != nil {
		t.Fatalf("the command failed: %+v", response.Error)
	}
	t.Logf("model said: %s", response.Message)

	// Asserted from REAPER's own report rather than from the command we sent,
	// which is the only evidence the DAW actually moved.
	value, err := reaper.ReadParam(1, "volume", 5*time.Second)
	if err != nil {
		t.Fatalf("REAPER never reported the value back: %v", err)
	}
	got, ok := value.(float64)
	if !ok || got < 0.2 || got > 0.3 {
		t.Fatalf("expected REAPER to hold roughly 0.25, got %#v", value)
	}
	t.Logf("REAPER reports track 1 volume as %v", got)
}

var _ = config.LLM{}

// A beginner should not need to know indices.
// The model is given no number: it has to ask the DAW what the tracks are
// called and match the name itself.
func TestNamedTrackCommandChangesREAPER(t *testing.T) {
	llm := liveConfig(t)

	listener, err := osc.Listen("127.0.0.1", 9000)
	if err != nil {
		t.Fatalf("could not listen for REAPER's feedback: %v", err)
	}
	defer listener.Close()

	transport := osc.NewTransport("127.0.0.1", 8000)
	reaper := daw.NewREAPER(transport)
	reaper.Observe(listener.Messages())

	// Names the track this test depends on. Unlike the read path this does
	// change the project, which is why it belongs in a tagged test rather
	// than anywhere the agent can reach.
	if err := transport.Send("/track/2/name", "Vocals"); err != nil {
		t.Fatalf("could not name the track: %v", err)
	}
	time.Sleep(300 * time.Millisecond)

	orchestrator := agent.NewOrchestrator(
		agent.Config{BaseURL: llm.BaseURL, APIKey: llm.APIKey, Model: llm.Model},
		agent.NewTools(reaper),
	)

	response := orchestrator.Send("Mute the vocals.")
	if response.Error != nil {
		t.Fatalf("the command failed: %+v", response.Error)
	}
	t.Logf("model said: %s", response.Message)

	value, err := reaper.ReadParam(2, "mute", 5*time.Second)
	if err != nil {
		t.Fatalf("REAPER never reported the mute state: %v", err)
	}
	if muted, ok := value.(bool); !ok || !muted {
		t.Fatalf("expected the vocals track to be muted, got %#v", value)
	}
	t.Log("REAPER reports track 2 (Vocals) muted")

	_ = reaper.SetTrackMute(2, false)
}

// The guardrail, end to end. An agent driving someone's project is safe to the
// degree its work can be taken back, so this asserts the value actually
// returns rather than that the tool was called.
func TestUndoReturnsTheValue(t *testing.T) {
	llm := liveConfig(t)

	listener, err := osc.Listen("127.0.0.1", 9000)
	if err != nil {
		t.Fatalf("could not listen for REAPER's feedback: %v", err)
	}
	defer listener.Close()

	reaper := daw.NewREAPER(osc.NewTransport("127.0.0.1", 8000))
	reaper.Observe(listener.Messages())

	// A known starting point, set directly so the test is about undo rather
	// than about the model.
	if err := reaper.SetTrackVolume(1, 0.5); err != nil {
		t.Fatalf("could not set the starting value: %v", err)
	}
	time.Sleep(400 * time.Millisecond)
	before, err := reaper.ReadParam(1, "volume", 5*time.Second)
	if err != nil {
		t.Fatalf("could not read the starting value: %v", err)
	}

	orchestrator := agent.NewOrchestrator(
		agent.Config{BaseURL: llm.BaseURL, APIKey: llm.APIKey, Model: llm.Model},
		agent.NewTools(reaper),
	)

	if response := orchestrator.Send("Set track 1 volume to 0.9."); response.Error != nil {
		t.Fatalf("the command failed: %+v", response.Error)
	}
	changed, err := reaper.ReadParam(1, "volume", 5*time.Second)
	if err != nil || changed == before {
		t.Fatalf("expected the value to have moved from %v, got %v (%v)", before, changed, err)
	}

	response := orchestrator.Send("Undo that.")
	if response.Error != nil {
		t.Fatalf("the undo failed: %+v", response.Error)
	}
	t.Logf("model said: %s", response.Message)

	time.Sleep(500 * time.Millisecond)
	restored, err := reaper.ReadParam(1, "volume", 5*time.Second)
	if err != nil {
		t.Fatalf("could not read the value back: %v", err)
	}
	if restored != before {
		t.Fatalf("expected the value to return to %v, got %v", before, restored)
	}
	t.Logf("volume went %v -> %v -> %v", before, changed, restored)
}

// The preview guardrail, against the real thing: the agent says
// what it would do, the project does not move, and accepting runs those exact
// steps rather than a second answer to the same question.
func TestPreviewThenApply(t *testing.T) {
	llm := liveConfig(t)

	listener, err := osc.Listen("127.0.0.1", 9000)
	if err != nil {
		t.Fatalf("could not listen for REAPER's feedback: %v", err)
	}
	defer listener.Close()

	reaper := daw.NewREAPER(osc.NewTransport("127.0.0.1", 8000))
	reaper.Observe(listener.Messages())

	if err := reaper.SetTrackVolume(3, 0.5); err != nil {
		t.Fatalf("could not set the starting value: %v", err)
	}
	time.Sleep(400 * time.Millisecond)
	before, err := reaper.ReadParam(3, "volume", 5*time.Second)
	if err != nil {
		t.Fatalf("could not read the starting value: %v", err)
	}

	config := agent.Config{BaseURL: llm.BaseURL, APIKey: llm.APIKey, Model: llm.Model}
	previews := agent.NewOrchestrator(config, agent.NewPreviewTools(reaper))
	live := agent.NewOrchestrator(config, agent.NewTools(reaper))

	plan := previews.Send("Set track 3 volume to 0.8.")
	if plan.Error != nil {
		t.Fatalf("the preview failed: %+v", plan.Error)
	}
	if len(plan.Plan) == 0 {
		t.Fatalf("expected a plan, got none: %q", plan.Message)
	}
	t.Logf("would: %s", plan.Plan[0].Description)

	// Nothing may have moved yet, which is the whole point.
	time.Sleep(400 * time.Millisecond)
	unchanged, err := reaper.ReadParam(3, "volume", 3*time.Second)
	if err == nil && unchanged != before {
		t.Fatalf("the preview changed the project: %v became %v", before, unchanged)
	}

	if applied := live.Apply(plan.Plan); applied.Error != nil {
		t.Fatalf("applying failed: %+v", applied.Error)
	}
	after, err := reaper.ReadParam(3, "volume", 5*time.Second)
	if err != nil {
		t.Fatalf("could not read the value back: %v", err)
	}
	if after == before {
		t.Fatalf("applying did not change anything: still %v", after)
	}
	t.Logf("volume %v -> preview (unchanged) -> applied %v", before, after)
}
