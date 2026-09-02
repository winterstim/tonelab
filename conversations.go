package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

// conversations holds the threads and keeps them on disk. Persisting a
// musician's requests is a decision rather than a default, and it was taken
// on purpose: a window closed at the end of a session should be openable at
// the start of the next one with the work still in it.
type conversations struct {
	mu      sync.Mutex
	threads []*Conversation
	active  string

	// What the agent remembered in each thread, kept beside it so switching
	// back restores the subject rather than an empty head.
	memory map[string][]exchange

	// Where they are written. Empty disables saving, which is what tests
	// want and what a machine with no writable config directory gets.
	path string
}

// stored is the shape on disk, named separately so the file format is a
// decision rather than a side effect of how the type happens to look today.
type stored struct {
	Active  string                `json:"active"`
	Threads []*Conversation       `json:"threads"`
	Memory  map[string][]exchange `json:"memory"`
}

// exchange mirrors the agent's own, kept local so this file does not depend on
// the agent package for a pair of strings.
type exchange struct {
	Question string `json:"question"`
	Answer   string `json:"answer"`
}

func newConversations(path string) *conversations {
	c := &conversations{memory: map[string][]exchange{}, path: path}
	if c.load() && len(c.threads) > 0 {
		return c
	}
	c.start()
	return c
}

// load reads what was saved. A file that cannot be read is not worth failing
// the app over: the worst case is starting with an empty list, which is where
// everyone starts anyway.
func (c *conversations) load() bool {
	if c.path == "" {
		return false
	}
	body, err := os.ReadFile(c.path)
	if err != nil {
		return false
	}

	var saved stored
	if err := json.Unmarshal(body, &saved); err != nil {
		return false
	}

	c.threads = saved.Threads
	c.active = saved.Active
	if saved.Memory != nil {
		c.memory = saved.Memory
	}
	return true
}

// save writes the whole list, since it is small and a partial write is how a
// file ends up in a state nobody wrote on purpose. Called with the lock held.
func (c *conversations) saveLocked() {
	if c.path == "" {
		return
	}

	body, err := json.MarshalIndent(stored{Active: c.active, Threads: c.threads, Memory: c.memory}, "", "  ")
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return
	}

	// Beside and renamed, so an interrupted write leaves the previous file
	// rather than half of this one.
	temporary := c.path + ".new"
	if err := os.WriteFile(temporary, body, 0o600); err != nil {
		return
	}
	os.Rename(temporary, c.path)
}

// rename replaces a thread's title with one the user chose. A generated title
// is a guess from the first thing said, and a guess should be correctable.
func (c *conversations) rename(id, name string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	for _, thread := range c.threads {
		if thread.ID == id {
			thread.Title = title(name)
			c.saveLocked()
			return true
		}
	}
	return false
}

// remove drops a thread. If it was the one being spoken to, the newest of
// what remains takes over, since leaving no thread selected would leave the
// window with nothing to draw.
func (c *conversations) remove(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	for i, thread := range c.threads {
		if thread.ID != id {
			continue
		}
		c.threads = append(c.threads[:i], c.threads[i+1:]...)
		delete(c.memory, id)

		if c.active == id {
			c.active = ""
			if len(c.threads) > 0 {
				c.active = c.threads[len(c.threads)-1].ID
			}
		}
		c.saveLocked()
		return true
	}
	return false
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
	c.saveLocked()
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
func (c *conversations) add(message ChatMessage) string {
	thread := c.current()
	c.addTo(thread.ID, message)
	return thread.ID
}

// addTo appends to a named thread, which is what a turn in flight needs: a
// question asked in one conversation must have its answer land there, and by
// the time an answer arrives the user may be reading another.
func (c *conversations) addTo(id string, message ChatMessage) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, thread := range c.threads {
		if thread.ID != id {
			continue
		}
		thread.Messages = append(thread.Messages, message)
		if message.From == "you" && thread.Title == "New conversation" {
			thread.Title = title(message.Text)
		}
		c.saveLocked()
		return
	}
	// The thread was deleted while the turn ran, so there is nowhere for the
	// answer to go. Dropping it is right: putting it somewhere else would put
	// it in a conversation it was not part of.
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
			c.saveLocked()
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
	c.saveLocked()
}
