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
