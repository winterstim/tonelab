//go:build llm && reaper

// The application as the window uses it: the same service methods the frontend
// calls, over the same wiring main.go builds, against a real DAW and a real
// model. Everything below has been tested in pieces; this is the only place
// the pieces are assembled the way they ship.
//
//	go test -tags "llm reaper" -count=1 -p 1 -run TestApp .
package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tonelab/backend/agent"
	"tonelab/backend/config"
	"tonelab/backend/daw"
	"tonelab/backend/osc"
)

// build assembles the services exactly as main.go does, so a difference
// between the test and the product would be a bug in one of them.
func build(t *testing.T) *AgentService {
	t.Helper()

	path := configPath(t)
	settings, err := config.Load(path)
	if err != nil {
		t.Skipf("no usable config at %s: %v", path, err)
	}

	client, err := daw.New(settings.DAW.Backend, osc.NewTransport(settings.DAW.Host, settings.DAW.Port))
	if err != nil {
		t.Fatalf("could not build the DAW backend: %v", err)
	}

	listener, err := osc.Listen(settings.DAW.Host, settings.DAW.FeedbackPort)
	if err != nil {
		t.Fatalf("could not listen for feedback: %v", err)
	}
	t.Cleanup(func() { listener.Close() })

	reaper := client.(*daw.REAPER)
	reaper.Observe(listener.Messages())

	llm := agent.Config{BaseURL: settings.LLM.BaseURL, APIKey: settings.LLM.APIKey, Model: settings.LLM.Model}
	live := agent.NewOrchestrator(llm, agent.NewTools(client))
	previews := agent.NewOrchestrator(llm, agent.NewPreviewTools(client))

	return NewAgentService(
		orchestratorBrain{orchestrator: live},
		previewBrain{orchestrator: previews, live: live},
		reaper, client, filepath.Join(t.TempDir(), "conversations.json"))
}

func configPath(t *testing.T) string {
	t.Helper()

	if path := envConfig(); path != "" {
		return path
	}
	path, err := config.Path()
	if err != nil {
		t.Skipf("no config path: %v", err)
	}
	return path
}

// The whole product in one pass, in the order a person would use it.
func TestAppEndToEnd(t *testing.T) {
	service := build(t)

	t.Run("the DAW reports as connected", func(t *testing.T) {
		// Feedback only arrives once something has been said, so a command
		// comes first, exactly as it would in use.
		service.SendCommand("Set track 1 volume to 0.5.")

		status, err := service.GetDAWStatus()
		if err != nil {
			t.Fatalf("unexpected Go error: %v", err)
		}
		if !status.Connected {
			t.Fatalf("expected the DAW to report connected: %s", status.Detail)
		}
	})

	t.Run("a command changes the project and says what it did", func(t *testing.T) {
		response, err := service.SendCommand("Set track 1 volume to 0.25.")
		if err != nil {
			t.Fatalf("unexpected Go error: %v", err)
		}
		if response.Error != nil {
			t.Fatalf("the command failed: %+v", response.Error)
		}
		if len(response.Changed) == 0 {
			t.Fatalf("expected a reported change, got %q", response.Message)
		}
		change := response.Changed[0]
		if change.NewValue == nil {
			t.Errorf("expected the DAW's own reading, got %+v", change)
		}
		t.Logf("said: %s | changed: %+v", response.Message, change)
	})

	t.Run("a preview proposes without acting, and applies on request", func(t *testing.T) {
		preview, err := service.PreviewCommand("Set track 1 volume to 0.75.")
		if err != nil {
			t.Fatalf("unexpected Go error: %v", err)
		}
		if preview.Error != nil {
			t.Fatalf("the preview failed: %+v", preview.Error)
		}
		if len(preview.Plan) == 0 {
			t.Fatalf("expected a plan, got %q", preview.Message)
		}

		applied, err := service.ApplyPlan()
		if err != nil {
			t.Fatalf("unexpected Go error: %v", err)
		}
		if applied.Error != nil {
			t.Fatalf("applying failed: %+v", applied.Error)
		}
		t.Logf("would: %s | applied: %+v", preview.Plan[0].Description, applied.Changed)
	})

	t.Run("undo works without the model", func(t *testing.T) {
		response, err := service.Undo()
		if err != nil {
			t.Fatalf("unexpected Go error: %v", err)
		}
		if response.Error != nil {
			t.Fatalf("undo failed: %+v", response.Error)
		}
	})

	t.Run("the history shows what happened", func(t *testing.T) {
		entries, err := service.History()
		if err != nil {
			t.Fatalf("unexpected Go error: %v", err)
		}
		if len(entries) < 4 {
			t.Fatalf("expected the turns above to be recorded, got %d", len(entries))
		}

		var previews, undos int
		for _, entry := range entries {
			if entry.Preview {
				previews++
			}
			if strings.Contains(entry.Command, "undo") {
				undos++
			}
		}
		if previews == 0 {
			t.Error("the preview is missing from the history")
		}
		if undos == 0 {
			t.Error("the undo is missing from the history")
		}
		t.Logf("history holds %d turns, newest %q", len(entries), entries[0].Command)
	})

	// Named for what it checks rather than what one might hope. A model asked
	// for volume "eleven" may decline, may refuse, or may decide the user
	// means the maximum and set 1.0, which is what was observed. That choice
	// is the model's to make and undo exists for it; what the product must
	// guarantee is narrower, and is what this asserts.
	t.Run("no out of range value reaches the DAW", func(t *testing.T) {
		response, err := service.SendCommand("Set track 1 volume to eleven.")
		if err != nil {
			t.Fatalf("unexpected Go error: %v", err)
		}
		for _, change := range response.Changed {
			if value, ok := change.NewValue.(float64); ok && (value < 0 || value > 1) {
				t.Fatalf("an out of range value reached the DAW: %+v", change)
			}
		}
		t.Logf("said: %s", response.Message)
	})

	t.Run("an empty command costs no model call", func(t *testing.T) {
		start := time.Now()
		response, _ := service.SendCommand("   ")

		if response.Error == nil || response.Error.Code != "empty_command" {
			t.Fatalf("expected empty_command, got %+v", response.Error)
		}
		if elapsed := time.Since(start); elapsed > time.Second {
			t.Errorf("an empty command should not reach the model, took %s", elapsed)
		}
	})
}
