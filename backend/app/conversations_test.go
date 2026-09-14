package app

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The button says "new conversation", and one that destroyed the previous one
// would be lying about the word.
func TestStartingAConversationKeepsTheOldOne(t *testing.T) {
	brain := &stubBrain{response: AgentResponse{Message: "Done."}}
	service := NewAgentService(brain, nil, &stubLiveness{}, nil, "")

	service.SendCommand("mute the vocals")
	if _, err := service.StartConversation(); err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	service.SendCommand("pan the drums")

	threads, _ := service.Conversations()
	if len(threads) != 2 {
		t.Fatalf("expected both conversations, got %d", len(threads))
	}
	if !threads[0].Active {
		t.Error("the newest should be the one being spoken to")
	}
}

// A list of threads all called "New conversation" is not a list.
func TestAConversationIsNamedAfterWhatWasAsked(t *testing.T) {
	service := NewAgentService(&stubBrain{}, nil, &stubLiveness{}, nil, "")

	service.SendCommand("turn the vocals down a little")

	threads, _ := service.Conversations()
	if threads[0].Title != "turn the vocals down a little" {
		t.Fatalf("unexpected title %q", threads[0].Title)
	}
}

func TestALongFirstMessageIsShortenedForTheList(t *testing.T) {
	service := NewAgentService(&stubBrain{}, nil, &stubLiveness{}, nil, "")

	service.SendCommand(strings.Repeat("make the vocals brighter ", 10))

	threads, _ := service.Conversations()
	if len([]rune(threads[0].Title)) > 44 {
		t.Fatalf("the title was not shortened: %q", threads[0].Title)
	}
}

// Going back must show what was said, or the conversation was not kept, only
// listed.
func TestReopeningAConversationBringsItBack(t *testing.T) {
	brain := &stubBrain{response: AgentResponse{Message: "Muted."}}
	service := NewAgentService(brain, nil, &stubLiveness{}, nil, "")

	service.SendCommand("mute the vocals")
	first, _ := service.CurrentConversation()

	service.StartConversation()
	service.SendCommand("pan the drums")

	reopened, err := service.OpenConversation(first.ID)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if len(reopened.Messages) != 2 {
		t.Fatalf("expected the question and its answer, got %d", len(reopened.Messages))
	}
	if reopened.Messages[0].Text != "mute the vocals" {
		t.Fatalf("unexpected first message %q", reopened.Messages[0].Text)
	}
}

// Switching must carry the agent's memory with the thread, or a follow-up in
// a reopened conversation would refer to whatever was said in another one.
func TestTheAgentsMemoryFollowsTheConversation(t *testing.T) {
	brain := &stubBrain{response: AgentResponse{Message: "Track 2."}}
	service := NewAgentService(brain, nil, &stubLiveness{}, nil, "")

	service.SendCommand("which track is the vocals")
	brain.Restore([]Exchange{{Question: "which track is the vocals", Answer: "Track 2."}})
	first, _ := service.CurrentConversation()

	service.StartConversation()
	if remembered := brain.Recall(); len(remembered) != 0 {
		t.Fatalf("a new conversation started with the old one's memory: %+v", remembered)
	}

	brain.Restore([]Exchange{{Question: "pan the drums", Answer: "Panned."}})
	service.OpenConversation(first.ID)

	remembered := brain.Recall()
	if len(remembered) != 1 || remembered[0].Question != "which track is the vocals" {
		t.Fatalf("the reopened conversation did not get its own memory back: %+v", remembered)
	}
}

// A window left open all day must not grow threads without end.
func TestConversationsAreBounded(t *testing.T) {
	service := NewAgentService(&stubBrain{}, nil, &stubLiveness{}, nil, "")

	for i := 0; i < conversationLimit+5; i++ {
		service.StartConversation()
	}

	threads, _ := service.Conversations()
	if len(threads) != conversationLimit {
		t.Fatalf("expected %d conversations, got %d", conversationLimit, len(threads))
	}
}

// Asking for a thread that is gone must not silently switch to another one.
func TestOpeningAMissingConversationChangesNothing(t *testing.T) {
	service := NewAgentService(&stubBrain{}, nil, &stubLiveness{}, nil, "")
	service.SendCommand("mute the vocals")
	before, _ := service.CurrentConversation()

	if _, err := service.OpenConversation("nonsense"); err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}

	after, _ := service.CurrentConversation()
	if after.ID != before.ID {
		t.Fatal("a missing conversation switched away from the current one")
	}
}

// A window closed at the end of a session should open at the start of the
// next one with the work still in it.
func TestConversationsSurviveARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conversations.json")
	brain := &stubBrain{response: AgentResponse{Message: "Muted."}}
	service := NewAgentService(brain, nil, &stubLiveness{}, nil, path)

	service.SendCommand("mute the vocals")
	service.StartConversation()
	service.SendCommand("pan the drums")

	// A fresh service over the same file, as a relaunch would build.
	reopened := NewAgentService(&stubBrain{}, nil, &stubLiveness{}, nil, path)

	threads, _ := reopened.Conversations()
	if len(threads) != 2 {
		t.Fatalf("expected both conversations to come back, got %d", len(threads))
	}
	current, _ := reopened.CurrentConversation()
	if len(current.Messages) != 2 || current.Messages[0].Text != "pan the drums" {
		t.Fatalf("expected the last conversation to be the open one, got %+v", current.Messages)
	}
}

// The agent's memory belongs to the thread, so it has to be written with it.
func TestRememberedContextSurvivesARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conversations.json")
	brain := &stubBrain{response: AgentResponse{Message: "Track 2."}}
	service := NewAgentService(brain, nil, &stubLiveness{}, nil, path)

	service.SendCommand("which track is the vocals")
	brain.Restore([]Exchange{{Question: "which track is the vocals", Answer: "Track 2."}})
	first, _ := service.CurrentConversation()
	service.StartConversation()

	reopened := NewAgentService(&stubBrain{}, nil, &stubLiveness{}, nil, path)
	reopened.OpenConversation(first.ID)

	remembered := reopened.agent.Recall()
	if len(remembered) != 1 || remembered[0].Answer != "Track 2." {
		t.Fatalf("the conversation came back without what the agent knew: %+v", remembered)
	}
}

// A generated title is a guess from the first thing said, and a guess should
// be correctable.
func TestAConversationCanBeRenamed(t *testing.T) {
	service := NewAgentService(&stubBrain{}, nil, &stubLiveness{}, nil, "")
	service.SendCommand("mute the vocals")
	current, _ := service.CurrentConversation()

	if _, err := service.RenameConversation(current.ID, "  vocal pass  "); err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}

	threads, _ := service.Conversations()
	if threads[0].Title != "vocal pass" {
		t.Fatalf("expected the new name, trimmed, got %q", threads[0].Title)
	}
}

// An empty name would leave a nameless row in the list.
func TestRenamingToNothingIsRefused(t *testing.T) {
	service := NewAgentService(&stubBrain{}, nil, &stubLiveness{}, nil, "")
	service.SendCommand("mute the vocals")
	current, _ := service.CurrentConversation()

	response, _ := service.RenameConversation(current.ID, "   ")

	if response.Error == nil {
		t.Fatal("expected an empty name to be refused")
	}
}

func TestAConversationCanBeDeleted(t *testing.T) {
	service := NewAgentService(&stubBrain{}, nil, &stubLiveness{}, nil, "")
	service.SendCommand("mute the vocals")
	first, _ := service.CurrentConversation()
	service.StartConversation()
	service.SendCommand("pan the drums")

	if _, err := service.DeleteConversation(first.ID); err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}

	threads, _ := service.Conversations()
	if len(threads) != 1 || threads[0].Title != "pan the drums" {
		t.Fatalf("expected only the other conversation, got %+v", threads)
	}
}

// Deleting the open one must leave the window something to draw, and must not
// leave the agent remembering a conversation that no longer exists.
func TestDeletingTheOpenConversationSwitchesToAnother(t *testing.T) {
	brain := &stubBrain{response: AgentResponse{Message: "Done."}}
	service := NewAgentService(brain, nil, &stubLiveness{}, nil, "")

	service.SendCommand("mute the vocals")
	service.StartConversation()
	service.SendCommand("pan the drums")
	open, _ := service.CurrentConversation()

	replacement, err := service.DeleteConversation(open.ID)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if replacement.ID == open.ID {
		t.Fatal("the deleted conversation is still the open one")
	}
	if len(replacement.Messages) == 0 {
		t.Fatal("expected the replacement to come back with its messages")
	}
}

// Deleting the last one leaves an empty list rather than a dangling
// selection.
func TestDeletingTheOnlyConversationLeavesAFreshOne(t *testing.T) {
	service := NewAgentService(&stubBrain{}, nil, &stubLiveness{}, nil, "")
	service.SendCommand("mute the vocals")
	only, _ := service.CurrentConversation()

	service.DeleteConversation(only.ID)

	current, _ := service.CurrentConversation()
	if current.ID == only.ID {
		t.Fatal("the deleted conversation is still current")
	}
	if len(current.Messages) != 0 {
		t.Fatalf("expected a fresh conversation, got %+v", current.Messages)
	}
}

// A turn can take most of a minute, and by the time it finishes the user may
// be reading another conversation. The answer belongs to the one that asked.
func TestAnAnswerLandsInTheConversationThatAskedIt(t *testing.T) {
	// The blocking channel is fitted after the first command, or that one
	// would block too and the test would wait on itself.
	brain := &stubBrain{response: AgentResponse{Message: "Muted."}}
	service := NewAgentService(brain, nil, &stubLiveness{}, nil, "")
	service.SendCommand("first")
	asking, _ := service.CurrentConversation()

	brain.mu.Lock()
	brain.block = make(chan struct{})
	block := brain.block
	brain.mu.Unlock()

	done := make(chan struct{})
	go func() {
		service.SendCommand("mute the vocals")
		close(done)
	}()

	// Wait for the turn to be under way, then move to another conversation.
	deadline := time.Now().Add(time.Second)
	for brain.seen() != "mute the vocals" {
		if time.Now().After(deadline) {
			t.Fatal("the turn never started")
		}
		time.Sleep(time.Millisecond)
	}
	service.StartConversation()
	close(block)
	<-done

	elsewhere, _ := service.CurrentConversation()
	if len(elsewhere.Messages) != 0 {
		t.Fatalf("the answer landed in the conversation the user moved to: %+v", elsewhere.Messages)
	}

	original, _ := service.OpenConversation(asking.ID)
	last := original.Messages[len(original.Messages)-1]
	if last.Text != "Muted." {
		t.Fatalf("the answer is missing from the conversation that asked: %+v", original.Messages)
	}
}

// A conversation deleted while its turn ran has nowhere for the answer to go,
// and putting it elsewhere would put it in a conversation it was not part of.
func TestAnAnswerToADeletedConversationIsDropped(t *testing.T) {
	// The two turns answer differently, or the assertion below would find the
	// first one's reply and call it the second one's.
	brain := &stubBrain{response: AgentResponse{Message: "The first answer."}}
	service := NewAgentService(brain, nil, &stubLiveness{}, nil, "")
	service.SendCommand("first")
	service.StartConversation()
	doomed, _ := service.CurrentConversation()

	brain.mu.Lock()
	brain.block = make(chan struct{})
	block := brain.block
	brain.response = AgentResponse{Message: "Muted."}
	brain.mu.Unlock()

	done := make(chan struct{})
	go func() {
		service.SendCommand("mute the vocals")
		close(done)
	}()

	deadline := time.Now().Add(time.Second)
	for brain.seen() != "mute the vocals" {
		if time.Now().After(deadline) {
			t.Fatal("the turn never started")
		}
		time.Sleep(time.Millisecond)
	}
	service.DeleteConversation(doomed.ID)
	close(block)
	<-done

	remaining, _ := service.CurrentConversation()
	for _, message := range remaining.Messages {
		if message.Text == "Muted." {
			t.Fatal("the answer was moved into a conversation it was not part of")
		}
	}
}
