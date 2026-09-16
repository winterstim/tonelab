//go:build llm && reaper

package agent_test

import (
	"strings"
	"testing"
	"time"

	"tonelab/backend/agent"
	"tonelab/backend/daw"
	"tonelab/backend/osc"
)

// The whole point of effect support, against a real model and a real DAW:
// the user names a control in words, the model finds it by search among
// whatever plugins happen to be on the track, and the DAW confirms the
// change. Nothing here knows what those plugins are.
func TestModelFindsAndSetsAnEffectParameter(t *testing.T) {
	llm := liveConfig(t)

	listener, err := osc.Listen("127.0.0.1", 9000)
	if err != nil {
		t.Fatalf("could not listen for REAPER's feedback: %v", err)
	}
	defer listener.Close()
	reaper := daw.NewREAPER(osc.NewTransport("127.0.0.1", 8000))
	reaper.Observe(listener.Messages())

	chain, err := reaper.FXChain(1, 2*time.Second)
	if err != nil || len(chain) == 0 {
		t.Skip("track 1 of the open project has no effects")
	}
	// The last effect's first parameter, whatever it is: the command names
	// it by the words the DAW uses, so the test holds for any plugin.
	target := chain[len(chain)-1]
	param := target.Params[0]
	before, _ := reaper.ReadFXParam(1, target.Number, param.Number, time.Second)

	orchestrator := agent.NewOrchestrator(
		agent.Config{BaseURL: llm.BaseURL, APIKey: llm.APIKey, Model: llm.Model},
		agent.NewTools(reaper),
	)
	command := "On track 1, set the " + strings.ToLower(param.Name) + " of the " + target.Name + " to 0.8."
	response := orchestrator.Send(command)
	if response.Error != nil {
		t.Fatalf("the command failed: %+v", response.Error)
	}
	t.Logf("asked: %s", command)
	t.Logf("model said: %s", response.Message)
	for _, step := range response.Steps {
		t.Logf("step: %s %s", step.Tool, step.Arguments)
	}

	searched := false
	for _, step := range response.Steps {
		if step.Tool == "find_params" {
			searched = true
		}
	}
	if !searched {
		t.Error("the model was expected to search rather than guess indices")
	}

	after, err := reaper.ConfirmFXParam(1, target.Number, param.Number, 2*time.Second)
	if err != nil {
		after, err = reaper.ReadFXParam(1, target.Number, param.Number, 2*time.Second)
	}
	if err != nil {
		t.Fatalf("the DAW never reported the parameter: %v", err)
	}
	if after < 0.75 || after > 0.85 {
		t.Fatalf("expected %q on %q near 0.8, got %v (was %v)", param.Name, target.Name, after, before)
	}
	t.Logf("REAPER reports %q on %q at %v", param.Name, target.Name, after)
}
