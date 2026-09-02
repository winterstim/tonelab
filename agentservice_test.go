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
	service := NewAgentService(brain, nil, stubLiveness{}, nil)

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
	service := NewAgentService(brain, nil, stubLiveness{}, nil)

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
	service := NewAgentService(brain, nil, stubLiveness{}, nil)

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
			service := NewAgentService(&stubBrain{}, nil, stubLiveness{lastSeen: tc.lastSeen}, nil)

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
	service := NewAgentService(&stubBrain{}, nil, nil, nil)

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
	service := NewAgentService(brain, nil, stubLiveness{}, nil)

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

// Undo sits beside the agent, not behind it: when a command went wrong, asking
// the model to fix it means trusting the thing that just erred.
func TestUndoDoesNotGoThroughTheAgent(t *testing.T) {
	brain := &stubBrain{}
	daw := &stubReverser{}
	service := NewAgentService(brain, nil, stubLiveness{}, daw)

	response, err := service.Undo()

	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if response.Error != nil {
		t.Fatalf("expected success, got %+v", response.Error)
	}
	if daw.undos != 1 {
		t.Errorf("expected the DAW to be asked once, got %d", daw.undos)
	}
	if brain.lastText != "" {
		t.Error("undo must not be routed through the language model")
	}
}

// A DAW that cannot undo says so rather than reporting a reversal that never
// happened.
func TestUndoOnABackendWithoutItIsReported(t *testing.T) {
	service := NewAgentService(&stubBrain{}, nil, stubLiveness{}, nil)

	response, err := service.Undo()

	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if response.Error == nil || response.Error.Code != "not_supported" {
		t.Fatalf("expected not_supported, got %+v", response.Error)
	}
}

type stubReverser struct{ undos int }

func (s *stubReverser) Undo() error { s.undos++; return nil }

// A plan the user did not see must never be what runs, so applying uses the
// steps the preview showed rather than asking again.
type stubPlanner struct {
	plan    []PlannedCall
	applied []PlannedCall
}

func (s *stubPlanner) Preview(string) AgentResponse {
	return AgentResponse{Message: "I would turn track 2 down.", Plan: s.plan}
}

func (s *stubPlanner) Apply(plan []PlannedCall) AgentResponse {
	s.applied = plan
	return AgentResponse{Message: "Applied."}
}

func TestApplyRunsExactlyWhatWasPreviewed(t *testing.T) {
	planner := &stubPlanner{plan: []PlannedCall{
		{Tool: "set_param", Arguments: `{"track_id":2}`, Description: "set track 2 volume to 0.3"},
	}}
	service := NewAgentService(&stubBrain{}, planner, stubLiveness{}, nil)

	preview, err := service.PreviewCommand("turn track 2 down")
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if len(preview.Plan) != 1 {
		t.Fatalf("expected the plan to reach the UI, got %v", preview.Plan)
	}

	if _, err := service.ApplyPlan(); err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if len(planner.applied) != 1 || planner.applied[0].Arguments != `{"track_id":2}` {
		t.Fatalf("expected the previewed step to be applied, got %v", planner.applied)
	}
}

// Applying twice must not repeat the command: the plan was accepted once.
func TestAPlanIsAppliedOnlyOnce(t *testing.T) {
	planner := &stubPlanner{plan: []PlannedCall{{Tool: "set_param", Arguments: `{}`}}}
	service := NewAgentService(&stubBrain{}, planner, stubLiveness{}, nil)

	service.PreviewCommand("anything")
	service.ApplyPlan()
	second, _ := service.ApplyPlan()

	if second.Error == nil || second.Error.Code != "nothing_to_apply" {
		t.Fatalf("expected the second apply to refuse, got %+v", second.Error)
	}
}

// Applying with nothing pending must refuse rather than fall back to asking
// the model, since the user is accepting something specific.
func TestApplyingNothingRefuses(t *testing.T) {
	service := NewAgentService(&stubBrain{}, &stubPlanner{}, stubLiveness{}, nil)

	response, err := service.ApplyPlan()

	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if response.Error == nil || response.Error.Code != "nothing_to_apply" {
		t.Fatalf("expected nothing_to_apply, got %+v", response.Error)
	}
}
