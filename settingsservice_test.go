package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tonelab/backend/agent"
	"tonelab/backend/config"
)

func settingsService(t *testing.T) (*SettingsService, string) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.json")
	settings := config.Config{
		LLM: config.LLM{BaseURL: "http://localhost:11434/v1", APIKey: "secret-key", Model: "m"},
		DAW: config.DAW{Backend: "reaper", Host: "127.0.0.1", Port: 8000, FeedbackPort: 9000},
	}
	if err := config.Save(path, settings); err != nil {
		t.Fatalf("could not write the test config: %v", err)
	}

	live := agent.NewOrchestrator(agent.Config{}, nil)
	previews := agent.NewOrchestrator(agent.Config{}, nil)
	return NewSettingsService(path, live, previews), path
}

// A key that never leaves the backend cannot be read off a screen, copied out
// of a screenshot, or logged by accident.
func TestTheKeyIsNeverSentToTheScreen(t *testing.T) {
	service, _ := settingsService(t)

	settings, err := service.Get()
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}

	encoded, _ := json.Marshal(settings)
	if strings.Contains(string(encoded), "secret-key") {
		t.Fatalf("the key reached the screen: %s", encoded)
	}
	if !settings.APIKeySet {
		t.Error("the screen still has to know a key is set")
	}
}

// A user editing the model name must not have to retype a secret they cannot
// see.
func TestSavingWithoutAKeyKeepsTheOne(t *testing.T) {
	service, path := settingsService(t)

	current, _ := service.Get()
	current.Model = "another-model"
	if _, err := service.Save(current, ""); err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}

	saved, err := config.Load(path)
	if err != nil {
		t.Fatalf("the saved file does not load: %v", err)
	}
	if saved.LLM.APIKey != "secret-key" {
		t.Fatalf("the key was lost: %q", saved.LLM.APIKey)
	}
	if saved.LLM.Model != "another-model" {
		t.Fatalf("the change was not saved: %q", saved.LLM.Model)
	}
}

func TestSavingAKeyReplacesIt(t *testing.T) {
	service, path := settingsService(t)

	current, _ := service.Get()
	if _, err := service.Save(current, "  new-key  "); err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}

	saved, _ := config.Load(path)
	if saved.LLM.APIKey != "new-key" {
		t.Fatalf("expected the key to be replaced and trimmed, got %q", saved.LLM.APIKey)
	}
}

// Settings a startup would refuse must be refused here, or the app would
// break on next launch with no way back through the screen that broke it.
func TestInvalidSettingsAreRefused(t *testing.T) {
	service, path := settingsService(t)

	current, _ := service.Get()
	current.BaseURL = ""
	result, err := service.Save(current, "")

	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if result.Error == nil || result.Error.Code != "settings_invalid" {
		t.Fatalf("expected settings_invalid, got %+v", result)
	}
	if saved, _ := config.Load(path); saved.LLM.BaseURL == "" {
		t.Error("the refused settings were written anyway")
	}
}

// The endpoint applies at once, because a user correcting a key should find
// out immediately whether that was the problem. The DAW connection cannot,
// and saying so beats a screen that disagrees with the app.
func TestChangingTheDAWReportsARestart(t *testing.T) {
	service, _ := settingsService(t)

	current, _ := service.Get()
	current.DAWPort = 9999
	result, _ := service.Save(current, "")

	if !result.RestartNeeded {
		t.Error("expected the restart to be reported")
	}
	if !strings.Contains(result.Message, "restart") {
		t.Errorf("expected the message to say so, got %q", result.Message)
	}

	current, _ = service.Get()
	current.Model = "changed"
	result, _ = service.Save(current, "")
	if result.RestartNeeded {
		t.Error("an endpoint change needs no restart")
	}
}

// A file that does not load yet is not a reason to refuse the save that would
// fix it.
func TestSettingsCanBeSavedOverABrokenFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("{ not json"), 0o600); err != nil {
		t.Fatalf("could not write the broken file: %v", err)
	}
	service := NewSettingsService(path, agent.NewOrchestrator(agent.Config{}, nil), agent.NewOrchestrator(agent.Config{}, nil))

	result, err := service.Save(Settings{
		BaseURL: "http://localhost:11434/v1", Model: "m",
		DAWBackend: "reaper", DAWHost: "127.0.0.1", DAWPort: 8000, DAWFeedback: 9000,
	}, "key")

	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.Saved {
		t.Fatalf("expected the save to succeed, got %+v", result)
	}
}
