//go:build llm

// Against a real language model endpoint, behind a build tag because it needs
// one configured. The fake endpoint proves our wire handling; only this proves
// a model actually drives the tools we wrote for it.
//
//	go test -tags llm -count=1 ./backend/agent/...
package agent_test

import (
	"os"
	"strings"
	"testing"

	"tonelab/backend/agent"
	"tonelab/backend/config"
)

func liveConfig(t *testing.T) config.LLM {
	t.Helper()

	path := os.Getenv("TONELAB_CONFIG")
	if path == "" {
		var err error
		if path, err = config.Path(); err != nil {
			t.Skipf("no config path: %v", err)
		}
	}
	settings, err := config.Load(path)
	if err != nil {
		t.Skipf("no usable config at %s: %v", path, err)
	}
	return settings.LLM
}

// The question no fake can answer: given our schemas and descriptions, does a
// real model call the right tool with the right arguments?
func TestModelDrivesTheTools(t *testing.T) {
	backend := newFakeDAW()
	llm := liveConfig(t)
	orchestrator := agent.NewOrchestrator(
		agent.Config{BaseURL: llm.BaseURL, APIKey: llm.APIKey, Model: llm.Model},
		agent.NewTools(backend),
	)

	response := orchestrator.Send("Set the volume of track 2 to half.")

	if response.Error != nil {
		t.Fatalf("the model could not complete the command: %+v", response.Error)
	}
	if len(backend.setCalls) == 0 {
		t.Fatalf("the model answered without touching the DAW: %q", response.Message)
	}

	call := backend.setCalls[0]
	if call.track != 2 {
		t.Errorf("expected track 2, got %d", call.track)
	}
	if call.name != "volume" {
		t.Errorf("expected volume, got %q", call.name)
	}
	if value, ok := call.value.(float64); !ok || value < 0.4 || value > 0.6 {
		t.Errorf("expected roughly half, got %#v", call.value)
	}
	t.Logf("model said: %s", strings.TrimSpace(response.Message))
}

// Refusing to guess matters more than obeying: a wrong command changes a real
// project, and the system prompt asks for a question instead of a guess.
func TestModelDeclinesWhatTheToolsCannotDo(t *testing.T) {
	backend := newFakeDAW()
	llm := liveConfig(t)
	orchestrator := agent.NewOrchestrator(
		agent.Config{BaseURL: llm.BaseURL, APIKey: llm.APIKey, Model: llm.Model},
		agent.NewTools(backend),
	)

	response := orchestrator.Send("Add a reverb plugin to track 1 and set it to 30 percent wet.")

	if len(backend.setCalls) != 0 {
		t.Fatalf("the model invented a command for something the tools cannot do: %+v", backend.setCalls)
	}
	t.Logf("model said: %s", strings.TrimSpace(response.Message))
}
