package main

import (
	"strings"
	"testing"
	"time"
)

// A stub rather than a real orchestrator: this layer's job is the boundary,
// and driving a language model to test a boundary would test the model.
type stubBrain struct {
	lastText string
	response AgentResponse
}

func (s *stubBrain) Send(text string) AgentResponse {
	s.lastText = text
	return s.response
}

type stubLiveness struct{ lastSeen time.Time }

func (s stubLiveness) LastSeen() time.Time { return s.lastSeen }

func TestSendCommandPassesTheTextThrough(t *testing.T) {
	brain := &stubBrain{response: AgentResponse{Message: "done"}}
	service := NewAgentService(brain, stubLiveness{})

	response, err := service.SendCommand("turn track 2 down")

	if err != nil {
		t.Fatalf("a domain call must not return a Go error: %v", err)
	}
	if brain.lastText != "turn track 2 down" {
		t.Errorf("expected the text to reach the agent, got %q", brain.lastText)
	}
	if response.Message != "done" {
		t.Errorf("expected the agent's answer to reach the UI, got %q", response.Message)
	}
}

// The error boundary: Go's error is for the RPC breaking, never
// for the agent failing to carry a command out.
func TestAgentFailuresAreNotGoErrors(t *testing.T) {
	brain := &stubBrain{response: AgentResponse{
		Error: &AgentError{Code: "param_not_found", Message: "No such parameter."},
	}}
	service := NewAgentService(brain, stubLiveness{})

	response, err := service.SendCommand("add reverb")

	if err != nil {
		t.Fatalf("a domain failure must not surface as a Go error: %v", err)
	}
	if response.Error == nil || response.Error.Code != "param_not_found" {
		t.Fatalf("expected the structured error to survive, got %+v", response.Error)
	}
}

// Empty input is worth catching here rather than spending a model call on it.
func TestEmptyCommandIsRejectedWithoutCallingTheAgent(t *testing.T) {
	brain := &stubBrain{}
	service := NewAgentService(brain, stubLiveness{})

	response, err := service.SendCommand("   ")

	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if response.Error == nil || response.Error.Code != "empty_command" {
		t.Fatalf("expected empty_command, got %+v", response.Error)
	}
	if brain.lastText != "" {
		t.Error("the agent should not have been called at all")
	}
}

// Connection is inferred from silence, because a fire-and-forget transport
// cannot tell a healthy DAW from a closed one any other way.
func TestDAWStatusFollowsRecentFeedback(t *testing.T) {
	for _, tc := range []struct {
		name      string
		lastSeen  time.Time
		connected bool
	}{
		{"never heard from", time.Time{}, false},
		{"heard from just now", time.Now(), true},
		{"silent for a long time", time.Now().Add(-time.Hour), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := NewAgentService(&stubBrain{}, stubLiveness{lastSeen: tc.lastSeen})

			status, err := service.GetDAWStatus()

			if err != nil {
				t.Fatalf("unexpected Go error: %v", err)
			}
			if status.Connected != tc.connected {
				t.Fatalf("expected connected=%v, got %v", tc.connected, status.Connected)
			}
		})
	}
}

// A backend that cannot report liveness must say "unknown" rather than claim a
// connection it cannot see.
func TestDAWStatusIsFalseWithoutALivenessSource(t *testing.T) {
	service := NewAgentService(&stubBrain{}, nil)

	status, err := service.GetDAWStatus()

	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if status.Connected {
		t.Error("expected no claim of connection without a way to observe one")
	}
	if !strings.Contains(strings.ToLower(status.Detail), "cannot") {
		t.Errorf("expected the reason to be stated, got %q", status.Detail)
	}
}

// The UI shows what the DAW confirmed rather than what the model said it did,
// because a model summarising its own work is the one account that cannot
// check itself.
func TestChangesReachTheUI(t *testing.T) {
	brain := &stubBrain{response: AgentResponse{
		Message: "Muted the vocals.",
		Changed: []ParamChange{{Track: 2, Param: "mute", Requested: true, NewValue: true}},
	}}
	service := NewAgentService(brain, stubLiveness{})

	response, err := service.SendCommand("mute the vocals")
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}

	if len(response.Changed) != 1 {
		t.Fatalf("expected one change, got %v", response.Changed)
	}
	if response.Changed[0].Track != 2 || response.Changed[0].NewValue != true {
		t.Fatalf("unexpected change %+v", response.Changed[0])
	}
}
