//go:build llm && (reaper || ableton || flstudio)

package agent_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"tonelab/backend/agent"
	"tonelab/backend/daw"
)

// The whole point of effect support, against a real model and a real DAW:
// the user names a control in words, the model finds it by search among
// whatever plugins happen to be on the track, and the DAW confirms the
// change. Nothing here knows what those plugins are.
func TestModelFindsAndSetsAnEffectParameter(t *testing.T) {
	llm := liveConfig(t)

	client := liveDAW(t)
	reaper := client.(interface {
		FXChain(int, time.Duration) ([]daw.FX, error)
		ReadFXParam(int, int, int, time.Duration) (float64, error)
		ConfirmFXParam(int, int, int, time.Duration) (float64, error)
	})

	// The first of the first few tracks with an effect that has named
	// parameters; a wrapped plugin may have one or none.
	var chain []daw.FX
	track := 0
	for candidate := 1; candidate <= 4 && track == 0; candidate++ {
		found, err := reaper.FXChain(candidate, 5*time.Second)
		if err == nil && len(found) > 0 && len(found[len(found)-1].Params) > 1 {
			chain, track = found, candidate
		}
	}
	if track == 0 {
		t.Skip("no track among the first four has an effect with named parameters")
	}
	// The last effect's second parameter, whatever it is: the command names
	// it by the words the DAW uses, so the test holds for any plugin. The
	// second, because some DAWs put an on/off switch first, which 0.8 does
	// not fit.
	target := chain[len(chain)-1]
	param := target.Params[0]
	if len(target.Params) > 1 {
		param = target.Params[1]
	}
	before, _ := reaper.ReadFXParam(track, target.Number, param.Number, time.Second)

	orchestrator := agent.NewOrchestrator(
		agent.Config{BaseURL: llm.BaseURL, APIKey: llm.APIKey, Model: llm.Model},
		agent.NewTools(client),
	)
	command := fmt.Sprintf("On track %d, set the %s of the %s to 0.8.", track, strings.ToLower(param.Name), target.Name)
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

	after, err := reaper.ConfirmFXParam(track, target.Number, param.Number, 2*time.Second)
	if err != nil {
		after, err = reaper.ReadFXParam(track, target.Number, param.Number, 2*time.Second)
	}
	if err != nil {
		t.Fatalf("the DAW never reported the parameter: %v", err)
	}
	if after < 0.75 || after > 0.85 {
		t.Fatalf("expected %q on %q near 0.8, got %v (was %v)", param.Name, target.Name, after, before)
	}
	t.Logf("the DAW reports %q on %q at %v", param.Name, target.Name, after)
}
