//go:build llm && reaper

// Measures what a turn costs, so the token diet is a number before and
// after rather than a feeling. Against the real DAW and endpoint, since
// the cost depends on what the DAW reports and what the model does with it.
//
//	TONELAB_CONFIG="$HOME/Library/Application Support/tonelab/config.groq.json" \
//	  go test -tags "llm reaper" -count=1 -p 1 -run TestTokensPerTurn -v ./backend/agent/
package agent_test

import (
	"testing"

	"tonelab/backend/agent"
)

func TestTokensPerTurn(t *testing.T) {
	client := liveDAW(t)
	llm := liveConfig(t)
	orchestrator := agent.NewOrchestrator(
		agent.Config{BaseURL: llm.BaseURL, APIKey: llm.APIKey, Model: llm.Model},
		agent.NewTools(client),
	)

	// One conversation, in the order a person would have it: a question, a
	// change by name, a relative follow-up, a plain undo.
	turns := []struct{ class, text string }{
		{"question", "Which tracks are there?"},
		{"by name", "Set the volume of the guitar to 0.6."},
		{"follow-up", "A bit quieter."},
		{"undo", "Undo that."},
	}
	var total agent.Tokens
	for _, turn := range turns {
		response := orchestrator.Send(turn.text)
		if response.Error != nil {
			t.Fatalf("%s: %+v", turn.class, response.Error)
		}
		u := response.Tokens
		t.Logf("%-10s prompt=%5d cached=%5d completion=%4d calls=%d", turn.class, u.Prompt, u.Cached, u.Completion, u.Calls)
		total.Prompt += u.Prompt
		total.Completion += u.Completion
		total.Cached += u.Cached
		total.Calls += u.Calls
	}
	t.Logf("%-10s prompt=%5d cached=%5d completion=%4d calls=%d", "total", total.Prompt, total.Cached, total.Completion, total.Calls)
}
