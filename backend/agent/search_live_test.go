//go:build llm

package agent_test

import (
	"os"
	"strings"
	"testing"

	"tonelab/backend/agent"
	"tonelab/backend/config"
	"tonelab/backend/search"
)

// Against a real model and a real search provider, from the same config
// file the app reads. Skipped, not failed, when no provider is set up.
func TestModelSearchesForAdviceAndCitesIt(t *testing.T) {
	llm := liveConfig(t)
	path := os.Getenv("TONELAB_CONFIG")
	if path == "" {
		path, _ = config.Path()
	}
	settings, _ := config.Load(path)
	provider, err := search.New(search.Config{Provider: settings.Search.Provider, APIKey: settings.Search.APIKey, BaseURL: settings.Search.BaseURL})
	if err != nil || provider == nil {
		t.Skip("no search provider configured")
	}

	tools := agent.NewTools(newFakeDAW())
	tools.EnableSearch(provider)
	orchestrator := agent.NewOrchestrator(agent.Config{BaseURL: llm.BaseURL, APIKey: llm.APIKey, Model: llm.Model}, tools)

	response := orchestrator.Send("How is a Marshall amp usually set for a classic rock rhythm tone? Look it up and tell me where you found it.")
	if response.Error != nil {
		t.Fatalf("the turn failed: %+v", response.Error)
	}
	t.Logf("model said: %s", response.Message)

	searched := false
	for _, step := range response.Steps {
		t.Logf("step: %s %s", step.Tool, step.Arguments)
		if step.Tool == "search" {
			searched = true
		}
	}
	if !searched {
		t.Fatal("the model was expected to search")
	}
	if !strings.Contains(response.Message, "http") && !strings.Contains(strings.ToLower(response.Message), "source") {
		t.Errorf("expected the answer to say where it came from")
	}
}

func TestModelReadsAPageWhenTheSnippetIsNotEnough(t *testing.T) {
	llm := liveConfig(t)
	path := os.Getenv("TONELAB_CONFIG")
	if path == "" {
		path, _ = config.Path()
	}
	settings, _ := config.Load(path)
	provider, err := search.New(search.Config{Provider: settings.Search.Provider, APIKey: settings.Search.APIKey, BaseURL: settings.Search.BaseURL})
	if err != nil || provider == nil {
		t.Skip("no search provider configured")
	}

	tools := agent.NewTools(newFakeDAW())
	tools.EnableSearch(provider)
	orchestrator := agent.NewOrchestrator(agent.Config{BaseURL: llm.BaseURL, APIKey: llm.APIKey, Model: llm.Model}, tools)

	response := orchestrator.Send("What band types does ReaEQ offer? Search, then read one of the result pages rather than answering from snippets, and cite the page you read.")
	if response.Error != nil {
		t.Fatalf("the turn failed: %+v", response.Error)
	}
	t.Logf("model said: %s", response.Message)

	fetched := false
	for _, step := range response.Steps {
		t.Logf("step: %s %s -> %.120s", step.Tool, step.Arguments, step.Outcome)
		if step.Tool == "fetch_page" && !step.Failed {
			fetched = true
		}
	}
	if !fetched {
		t.Fatal("the model was expected to read a page")
	}
}
