package main

import (
	"sync"
	"time"
)

// journalLimit is how many turns are kept. A musician wants to see what just
// happened, not everything that ever did, and an unbounded log in a
// long-running app is a leak with extra steps.
const journalLimit = 50

// JournalEntry is one turn as it happened, for a user asking what the agent
// did. An agent that changes someone's
// project has to be answerable, and its own summary is the one account that
// cannot be checked.
type JournalEntry struct {
	At      string
	Command string
	Answer  string
	Steps   []JournalStep
	Error   *AgentError

	// Preview marks a turn that only proposed, so a plan that was never
	// applied cannot be read later as something that happened.
	Preview bool
}

// JournalStep is one tool call, kept as the model made it.
type JournalStep struct {
	Tool      string
	Arguments string
	Outcome   string
	Failed    bool
}

// journal keeps recent turns. In memory only: it exists to answer "what just
// happened", and writing a musician's commands to disk is a decision to make
// deliberately rather than by default.
type journal struct {
	mu      sync.Mutex
	entries []JournalEntry
}

func (j *journal) record(entry JournalEntry) {
	j.mu.Lock()
	defer j.mu.Unlock()

	entry.At = time.Now().Format("15:04:05")
	j.entries = append(j.entries, entry)
	if len(j.entries) > journalLimit {
		j.entries = j.entries[len(j.entries)-journalLimit:]
	}
}

// list returns the newest first, which is the order someone looking for what
// just happened reads in.
func (j *journal) list() []JournalEntry {
	j.mu.Lock()
	defer j.mu.Unlock()

	reversed := make([]JournalEntry, 0, len(j.entries))
	for i := len(j.entries) - 1; i >= 0; i-- {
		reversed = append(reversed, j.entries[i])
	}
	return reversed
}
