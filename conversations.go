package main

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// conversationLimit is how many threads are kept. A working session produces a
// handful of separate ideas, not a hundred, and an unbounded list in a
// long-running window is a leak with a nicer name.
const conversationLimit = 20

// ChatMessage is one line of a thread as the window shows it. The thread lives
// here rather than in the page because it has to survive switching away from
// it, and a thread that only exists in the DOM does not.
type ChatMessage struct {
	From    string // "you" or "tonelab"
	Text    string
	Changed []ParamChange
	Plan    []PlannedCall
	Error   *AgentError
}

// Conversation is one thread and what the agent should remember while it is
// the one being spoken to.
type Conversation struct {
	ID       string
	Title    string
	Started  string
	Messages []ChatMessage
}

// ConversationSummary is the list without the contents, which is all a
// switcher needs and avoids sending every thread to draw three names.
type ConversationSummary struct {
	ID     string
	Title  string
	Active bool
}

// conversations holds the threads of one session. In memory only: this is
// what was said just now, and writing a musician's requests to disk is a
// decision to take deliberately rather than by default.
type conversations struct {
	mu      sync.Mutex
	threads []*Conversation
	active  string

	// What the agent remembered in each thread, kept beside it so switching
	// back restores the subject rather than an empty head.
	memory map[string][]exchange
}

// exchange mirrors the agent's own, kept local so this file does not depend on
// the agent package for a pair of strings.
type exchange struct {
	Question string
	Answer   string
}

func newConversations() *conversations {
	c := &conversations{memory: map[string][]exchange{}}
	c.start()
	return c
}

// start opens a thread and makes it the one being spoken to.
func (c *conversations) start() *Conversation {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.startLocked()
}

func (c *conversations) startLocked() *Conversation {
	thread := &Conversation{
		ID:      fmt.Sprintf("c%d", time.Now().UnixNano()),
		Title:   "New conversation",
		Started: time.Now().Format("15:04"),
	}
	c.threads = append(c.threads, thread)
	c.active = thread.ID

	if len(c.threads) > conversationLimit {
		dropped := c.threads[0]
		c.threads = c.threads[1:]
		delete(c.memory, dropped.ID)
	}
	return thread
}

// current returns the thread being spoken to, opening one if a caller somehow
// arrives before any exists.
func (c *conversations) current() *Conversation {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, thread := range c.threads {
		if thread.ID == c.active {
			return thread
		}
	}
	return c.startLocked()
}

// add appends to the thread being spoken to, naming it from the first thing
// said: a list of threads called "New conversation" is not a list.
func (c *conversations) add(message ChatMessage) {
	thread := c.current()

	c.mu.Lock()
	defer c.mu.Unlock()

	thread.Messages = append(thread.Messages, message)
	if message.From == "you" && thread.Title == "New conversation" {
		thread.Title = title(message.Text)
	}
}

func title(text string) string {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\n", " "))
	if len(text) <= 42 {
		return text
	}
	return strings.TrimSpace(text[:42]) + "…"
}

// list returns the threads newest first, which is the order a switcher reads.
func (c *conversations) list() []ConversationSummary {
	c.mu.Lock()
	defer c.mu.Unlock()

	summaries := make([]ConversationSummary, 0, len(c.threads))
	for i := len(c.threads) - 1; i >= 0; i-- {
		thread := c.threads[i]
		summaries = append(summaries, ConversationSummary{
			ID:     thread.ID,
			Title:  thread.Title,
			Active: thread.ID == c.active,
		})
	}
	return summaries
}

// selectThread switches to a thread, returning it and what the agent
// remembered there.
func (c *conversations) selectThread(id string) (*Conversation, []exchange, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, thread := range c.threads {
		if thread.ID == id {
			c.active = id
			return thread, c.memory[id], true
		}
	}
	return nil, nil, false
}

// remember stores the agent's memory against the thread it belongs to.
func (c *conversations) remember(memory []exchange) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.memory[c.active] = memory
}
