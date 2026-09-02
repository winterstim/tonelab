package main

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// A stub rather than a real orchestrator: this layer's job is the boundary,
// and driving a language model to test a boundary would test the model.
// Guarded because a stopped turn is inspected from the test's goroutine while
// it runs in another, which is the shape the real service has too.
type stubBrain struct {
	mu        sync.Mutex
	lastText  string
	response  AgentResponse
	block     chan struct{}
	cancelled bool
	forgotten bool
}

func (s *stubBrain) Forget() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.forgotten = true
}

func (s *stubBrain) seen() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastText
}

func (s *stubBrain) wasCancelled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cancelled
}

func (s *stubBrain) SendContext(ctx context.Context, text string) AgentResponse {
	s.mu.Lock()
	s.lastText = text
	block := s.block
	response := s.response
	s.mu.Unlock()

	if block != nil {
		<-block
	}

	s.mu.Lock()
	s.cancelled = ctx.Err() != nil
	s.mu.Unlock()
	return response
}

// Mirrors the real backend: recent feedback proves the DAW is there, and
// silence proves nothing, so the probe is what decides.
type stubLiveness struct {
	lastSeen time.Time
	answers  bool
	probes   int
}

func (s *stubLiveness) LastSeen() time.Time { return s.lastSeen }

func (s *stubLiveness) Probe(timeout time.Duration) bool {
	s.probes++
	return s.answers
}

func TestSendCommandPassesTheTextThrough(t *testing.T) {
	brain := &stubBrain{response: AgentResponse{Message: "done"}}
	service := NewAgentService(brain, nil, &stubLiveness{}, nil)

	response, err := service.SendCommand("turn track 2 down")

	if err != nil {
		t.Fatalf("a domain call must not return a Go error: %v", err)
	}
	if brain.seen() != "turn track 2 down" {
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
	service := NewAgentService(brain, nil, &stubLiveness{}, nil)

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
	service := NewAgentService(brain, nil, &stubLiveness{}, nil)

	response, err := service.SendCommand("   ")

	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if response.Error == nil || response.Error.Code != "empty_command" {
		t.Fatalf("expected empty_command, got %+v", response.Error)
	}
	if brain.seen() != "" {
		t.Error("the agent should not have been called at all")
	}
}

// An idle DAW sends nothing at all, measured as zero messages in ten seconds,
// so silence means "nobody is touching the project" far more often than "the
// DAW is gone". Reading quiet as absence would show a disconnected light for
// as long as the musician was thinking.
func TestDAWStatusAsksWhenItHasNotHeardRecently(t *testing.T) {
	for _, tc := range []struct {
		name      string
		lastSeen  time.Time
		answers   bool
		connected bool
	}{
		{"heard from just now", time.Now(), false, true},
		{"quiet but answers when asked", time.Now().Add(-time.Hour), true, true},
		{"quiet and does not answer", time.Now().Add(-time.Hour), false, false},
		{"never heard from, answers when asked", time.Time{}, true, true},
		{"never heard from, silent when asked", time.Time{}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			observer := &stubLiveness{lastSeen: tc.lastSeen, answers: tc.answers}
			service := NewAgentService(&stubBrain{}, nil, observer, nil)

			status, err := service.GetDAWStatus()

			if err != nil {
				t.Fatalf("unexpected Go error: %v", err)
			}
			if status.Connected != tc.connected {
				t.Fatalf("expected connected=%v, got %v (%s)", tc.connected, status.Connected, status.Detail)
			}
			// Recent feedback is proof enough and must not cost a poke.
			recentlyHeard := !tc.lastSeen.IsZero() && time.Since(tc.lastSeen) < time.Second
			if recentlyHeard && observer.probes != 0 {
				t.Errorf("expected no probe when the DAW was just heard from, got %d", observer.probes)
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
	service := NewAgentService(brain, nil, &stubLiveness{}, nil)

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
	service := NewAgentService(brain, nil, &stubLiveness{}, daw)

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
	if brain.seen() != "" {
		t.Error("undo must not be routed through the language model")
	}
}

// A DAW that cannot undo says so rather than reporting a reversal that never
// happened.
func TestUndoOnABackendWithoutItIsReported(t *testing.T) {
	service := NewAgentService(&stubBrain{}, nil, &stubLiveness{}, nil)

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
	service := NewAgentService(&stubBrain{}, planner, &stubLiveness{}, nil)

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
	service := NewAgentService(&stubBrain{}, planner, &stubLiveness{}, nil)

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
	service := NewAgentService(&stubBrain{}, &stubPlanner{}, &stubLiveness{}, nil)

	response, err := service.ApplyPlan()

	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if response.Error == nil || response.Error.Code != "nothing_to_apply" {
		t.Fatalf("expected nothing_to_apply, got %+v", response.Error)
	}
}

// A turn against a local model can take most of a minute, and a user who
// changed their mind should not have to watch it finish.
func TestStopEndsTheTurnInFlight(t *testing.T) {
	brain := &stubBrain{block: make(chan struct{}), response: AgentResponse{Message: "Stopped."}}
	service := NewAgentService(brain, nil, &stubLiveness{}, nil)

	done := make(chan struct{})
	go func() {
		service.SendCommand("something slow")
		close(done)
	}()

	// Wait until the turn is actually running, or Stop would find nothing.
	deadline := time.Now().Add(time.Second)
	for {
		if brain.seen() != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the turn never started")
		}
		time.Sleep(time.Millisecond)
	}

	if _, err := service.Stop(); err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	close(brain.block)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("the turn did not finish after being stopped")
	}
	if !brain.wasCancelled() {
		t.Error("the agent was not told the turn was cancelled")
	}
}

// Stopping when nothing is running says so rather than pretending.
func TestStopWithNothingRunning(t *testing.T) {
	service := NewAgentService(&stubBrain{}, nil, &stubLiveness{}, nil)

	response, err := service.Stop()

	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if response.Error == nil || response.Error.Code != "nothing_running" {
		t.Fatalf("expected nothing_running, got %+v", response.Error)
	}
}

// A user starting a new idea should not have to fight the last one.
func TestForgettingClearsTheAgentsMemory(t *testing.T) {
	brain := &stubBrain{}
	service := NewAgentService(brain, nil, &stubLiveness{}, nil)

	if _, err := service.Forget(); err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}

	brain.mu.Lock()
	defer brain.mu.Unlock()
	if !brain.forgotten {
		t.Error("the agent was not told to forget")
	}
}
