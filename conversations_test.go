package main

import (
	"strings"
	"testing"
)

// The button says "new conversation", and one that destroyed the previous one
// would be lying about the word.
func TestStartingAConversationKeepsTheOldOne(t *testing.T) {
	brain := &stubBrain{response: AgentResponse{Message: "Done."}}
	service := NewAgentService(brain, nil, &stubLiveness{}, nil)

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
	service := NewAgentService(&stubBrain{}, nil, &stubLiveness{}, nil)

	service.SendCommand("turn the vocals down a little")

	threads, _ := service.Conversations()
	if threads[0].Title != "turn the vocals down a little" {
		t.Fatalf("unexpected title %q", threads[0].Title)
	}
}

func TestALongFirstMessageIsShortenedForTheList(t *testing.T) {
	service := NewAgentService(&stubBrain{}, nil, &stubLiveness{}, nil)

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
	service := NewAgentService(brain, nil, &stubLiveness{}, nil)

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
	service := NewAgentService(brain, nil, &stubLiveness{}, nil)

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
	service := NewAgentService(&stubBrain{}, nil, &stubLiveness{}, nil)

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
	service := NewAgentService(&stubBrain{}, nil, &stubLiveness{}, nil)
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
