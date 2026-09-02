package main

import (
	"strconv"
	"testing"
)

// An agent that changes someone's project
// has to be answerable for what it did, and its own summary is the one account
// that cannot be checked.
func TestHistoryRecordsWhatTheAgentDid(t *testing.T) {
	brain := &stubBrain{response: AgentResponse{
		Message: "Muted the vocals.",
		Steps: []JournalStep{
			{Tool: "list_tracks", Arguments: `{}`, Outcome: `{"value":[]}`},
			{Tool: "set_param", Arguments: `{"track_id":2}`, Outcome: `{}`},
		},
	}}
	service := NewAgentService(brain, nil, stubLiveness{}, nil)

	service.SendCommand("mute the vocals")

	history, err := service.History()
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("expected one entry, got %d", len(history))
	}
	entry := history[0]
	if entry.Command != "mute the vocals" || entry.Answer != "Muted the vocals." {
		t.Fatalf("unexpected entry %+v", entry)
	}
	if len(entry.Steps) != 2 || entry.Steps[1].Tool != "set_param" {
		t.Fatalf("expected the tool calls to be kept, got %+v", entry.Steps)
	}
	if entry.At == "" {
		t.Error("expected the time to be recorded")
	}
}

// Newest first, which is the order someone looking for what just happened
// reads in.
func TestHistoryIsNewestFirst(t *testing.T) {
	service := NewAgentService(&stubBrain{}, nil, stubLiveness{}, nil)

	service.SendCommand("first")
	service.SendCommand("second")

	history, _ := service.History()
	if len(history) != 2 || history[0].Command != "second" {
		t.Fatalf("expected the newest first, got %+v", history)
	}
}

// A plan that was never applied must not read later as something that
// happened.
func TestPreviewsAreMarkedInHistory(t *testing.T) {
	service := NewAgentService(&stubBrain{}, &stubPlanner{}, stubLiveness{}, nil)

	service.PreviewCommand("turn track 2 down")

	history, _ := service.History()
	if len(history) != 1 || !history[0].Preview {
		t.Fatalf("expected the entry to be marked as a preview, got %+v", history)
	}
}

// Undo happens outside the agent, and a history missing it would be a history
// of the wrong thing.
func TestUndoIsRecorded(t *testing.T) {
	service := NewAgentService(&stubBrain{}, nil, stubLiveness{}, &stubReverser{})

	service.Undo()

	history, _ := service.History()
	if len(history) != 1 {
		t.Fatalf("expected the undo to be recorded, got %+v", history)
	}
}

// An app left running for a session must not grow a log without end.
func TestHistoryIsBounded(t *testing.T) {
	service := NewAgentService(&stubBrain{}, nil, stubLiveness{}, nil)

	for i := 0; i < journalLimit+20; i++ {
		service.SendCommand("command " + strconv.Itoa(i))
	}

	history, _ := service.History()
	if len(history) != journalLimit {
		t.Fatalf("expected %d entries, got %d", journalLimit, len(history))
	}
	// The newest must survive, not the oldest.
	if history[0].Command != "command "+strconv.Itoa(journalLimit+19) {
		t.Fatalf("expected the newest to be kept, got %q", history[0].Command)
	}
}

// Failures are the entries most worth keeping, since they are what a user
// comes to the history to understand.
func TestFailedTurnsAreRecorded(t *testing.T) {
	brain := &stubBrain{response: AgentResponse{
		Error: &AgentError{Code: "param_not_found", Message: "No such parameter."},
	}}
	service := NewAgentService(brain, nil, stubLiveness{}, nil)

	service.SendCommand("add reverb")

	history, _ := service.History()
	if len(history) != 1 || history[0].Error == nil {
		t.Fatalf("expected the failure to be recorded, got %+v", history)
	}
}
