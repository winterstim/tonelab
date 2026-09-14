package main

import (
	"context"
	"strings"
	"sync"
	"time"

	"tonelab/backend/agent"
)

// dawSilenceLimit is how recent feedback must be to count as proof of life
// without probing. Older silence is not evidence of absence; it triggers a probe.
const dawSilenceLimit = 5 * time.Second

// probeTimeout is how long to wait for a DAW to answer a poke. Local and
// fast, so a DAW that has not replied by now is not there.
const probeTimeout = 500 * time.Millisecond

// The types below are the frontend's contract. They live in package main
// because Wails generates the frontend's types from what a service returns.

// PlannedCall is held in the form the model produced, so applying it runs
// what the user approved rather than whatever a second question would produce.
type PlannedCall struct {
	Tool        string
	Arguments   string
	Description string
}

type AgentResponse struct {
	Message string
	// Conversation is the thread this answers, which a window showing
	// another one needs in order not to draw it there.
	Conversation string
	// Steps is what the turn did, tool by tool, for the history view.
	Steps []JournalStep
	// Plan is what a preview would do. Empty on an ordinary command.
	Plan []PlannedCall
	// Changed is plural because one command can move several parameters, and
	// a UI showing only the first would be quietly wrong.
	Changed []ParamChange
	Error   *AgentError
}

// ParamChange is what the DAW confirmed, not what the model claims. NewValue
// is empty when the DAW does not report the parameter, in which case Note says
// so rather than leaving a blank to be read as zero.
type ParamChange struct {
	Track     int
	Param     string
	Requested any
	NewValue  any
	Note      string
}

type AgentError struct {
	Code    string
	Message string
}

type DAWStatus struct {
	Connected bool
	// Detail says why, since "not connected" alone leaves a user with
	// nothing to act on.
	Detail string
}

// brain is the agent seen from the UI boundary, narrow enough that testing
// this layer does not mean driving a language model.
type brain interface {
	SendContext(ctx context.Context, text string) AgentResponse
	Forget()

	// Recall and Restore let a thread be put away and brought back with what
	// the agent knew while it was open. Without them, switching threads would
	// leave the new one inheriting the last one's subject.
	Recall() []Exchange
	Restore([]Exchange)
}

// Exchange is one question and its answer, which is all a later turn needs of
// an earlier one.
type Exchange struct {
	Question string
	Answer   string
}

// planner previews a command and carries out what it proposed. Separate from
// brain because previewing needs its own agent, one whose changing tools are
// disarmed, rather than a flag on a shared one.
type planner interface {
	Preview(text string) AgentResponse
	Apply(plan []PlannedCall) AgentResponse
}

// reverser is optional: not every DAW can be asked to take something back, and
// a button that cannot work should not be offered.
type reverser interface {
	Undo() error
}

// liveness is optional: a backend that cannot observe its DAW must be able to
// say so rather than have a connection assumed for it.
//
// Probe exists because silence proves nothing: an idle DAW sends nothing at
// all, so a status read from quiet alone would show disconnected for as long
// as the musician was thinking.
type liveness interface {
	LastSeen() time.Time
	Probe(timeout time.Duration) bool
}

// AgentService is the frontend's entire view of the backend. Every domain
// failure comes back inside AgentResponse; the Go error is reserved for the
// RPC itself breaking.
type AgentService struct {
	agent    brain
	planner  planner
	liveness liveness
	daw      any

	journal journal
	threads *conversations

	// Cancels the turn in flight, if there is one.
	stopTurn context.CancelFunc

	// The last plan a preview produced. Held so applying it runs exactly what
	// was shown; a plan the user did not see must never be what runs.
	mu      sync.Mutex
	pending []PlannedCall
}

// NewAgentService takes where to keep conversations. An empty path keeps them
// in memory, which is what tests want and what a machine with nowhere to write
// gets rather than a crash.
func NewAgentService(agent brain, previews planner, observer liveness, client any, threadsPath string) *AgentService {
	return &AgentService{
		agent:    agent,
		planner:  previews,
		liveness: observer,
		daw:      client,
		threads:  newConversations(threadsPath),
	}
}

// RenameConversation replaces a title. The generated one is a guess from the
// first thing said, and a guess should be correctable.
func (a *AgentService) RenameConversation(id, name string) (AgentResponse, error) {
	if !a.threads.rename(id, name) {
		return AgentResponse{Error: &AgentError{
			Code:    "not_found",
			Message: "That conversation is gone.",
		}}, nil
	}
	return AgentResponse{Message: "Renamed."}, nil
}

// DeleteConversation drops a thread and everything said in it.
func (a *AgentService) DeleteConversation(id string) (Conversation, error) {
	wasActive := a.threads.current().ID == id
	if !a.threads.remove(id) {
		return Conversation{}, nil
	}

	// Deleting what was being spoken to leaves the agent remembering a
	// conversation that no longer exists, so it is given the one that
	// replaced it.
	if wasActive {
		current := a.threads.current()
		_, memory, _ := a.threads.selectThread(current.ID)
		restore(a.agent, memory)
		return *current, nil
	}
	return *a.threads.current(), nil
}

// Conversations lists the threads of this session, newest first.
func (a *AgentService) Conversations() ([]ConversationSummary, error) {
	return a.threads.list(), nil
}

// StartConversation opens a new thread and leaves the old one where it is.
// The button that does this says "new conversation", and a button that
// destroyed the previous one would be lying about the word.
func (a *AgentService) StartConversation() (Conversation, error) {
	a.threads.remember(recall(a.agent))
	a.agent.Forget()
	return *a.threads.start(), nil
}

// OpenConversation switches to a thread and gives back everything said in it,
// so the window can show a conversation it did not keep.
func (a *AgentService) OpenConversation(id string) (Conversation, error) {
	a.threads.remember(recall(a.agent))

	thread, memory, found := a.threads.selectThread(id)
	if !found {
		return Conversation{}, nil
	}

	restore(a.agent, memory)
	return *thread, nil
}

// CurrentConversation is what the window draws on opening, since the thread
// lives here rather than in the page.
func (a *AgentService) CurrentConversation() (Conversation, error) {
	return *a.threads.current(), nil
}

// recall and restore translate between the service's exchange type and the
// agent's, keeping each package's vocabulary its own.
func recall(agent brain) []exchange {
	remembered := agent.Recall()
	kept := make([]exchange, 0, len(remembered))
	for _, one := range remembered {
		kept = append(kept, exchange{Question: one.Question, Answer: one.Answer})
	}
	return kept
}

func restore(agent brain, memory []exchange) {
	exchanges := make([]Exchange, 0, len(memory))
	for _, one := range memory {
		exchanges = append(exchanges, Exchange{Question: one.Question, Answer: one.Answer})
	}
	agent.Restore(exchanges)
}

// PreviewCommand says what a command would do without doing it, and holds the
// steps so the user can accept exactly those.
func (a *AgentService) PreviewCommand(text string) (AgentResponse, error) {
	if strings.TrimSpace(text) == "" {
		return AgentResponse{Error: &AgentError{Code: "empty_command", Message: "Type a command first."}}, nil
	}
	if a.planner == nil {
		return AgentResponse{Error: &AgentError{Code: "not_supported", Message: "Previewing is not available."}}, nil
	}

	asked := a.threads.add(ChatMessage{From: "you", Text: text})

	response := a.planner.Preview(text)
	a.threads.addTo(asked, chatMessage(response))
	response.Conversation = asked
	a.journal.record(JournalEntry{
		Command: text,
		Answer:  response.Message,
		Steps:   response.Steps,
		Error:   response.Error,
		Preview: true,
	})

	a.mu.Lock()
	a.pending = response.Plan
	a.mu.Unlock()

	return response, nil
}

// ApplyPlan carries out the plan the last preview showed. It refuses when
// there is nothing pending rather than falling back to asking the model, since
// the user is accepting something specific.
func (a *AgentService) ApplyPlan() (AgentResponse, error) {
	a.mu.Lock()
	plan := a.pending
	a.pending = nil
	a.mu.Unlock()

	if len(plan) == 0 {
		return AgentResponse{Error: &AgentError{
			Code:    "nothing_to_apply",
			Message: "There is no previewed plan to apply.",
		}}, nil
	}
	if a.planner == nil {
		return AgentResponse{Error: &AgentError{Code: "not_supported", Message: "Previewing is not available."}}, nil
	}
	applied := a.threads.current().ID
	response := a.planner.Apply(plan)
	a.threads.addTo(applied, chatMessage(response))
	response.Conversation = applied
	a.journal.record(JournalEntry{
		Command: "(applied the previewed plan)",
		Answer:  response.Message,
		Steps:   response.Steps,
		Error:   response.Error,
	})
	return response, nil
}

// Undo exists beside the agent rather than only through it. When a command
// went wrong, asking the model to fix it means trusting the thing that just
// erred, and a user reaching for undo wants it to happen, not to be
// interpreted.
func (a *AgentService) Undo() (AgentResponse, error) {
	source, ok := a.daw.(reverser)
	if !ok {
		return AgentResponse{Error: &AgentError{
			Code:    "not_supported",
			Message: "This DAW cannot undo.",
		}}, nil
	}

	if err := source.Undo(); err != nil {
		failure := &AgentError{Code: "daw_command_failed", Message: "The DAW did not accept the undo."}
		a.journal.record(JournalEntry{Command: "(undo button)", Error: failure})
		return AgentResponse{Error: failure}, nil
	}

	answer := "Asked the DAW to undo its last change."
	a.journal.record(JournalEntry{Command: "(undo button)", Answer: answer})
	return AgentResponse{Message: answer}, nil
}

// SendCommand runs one natural-language command. Blank input is refused here
// rather than spent on a model call that could only fail.
func (a *AgentService) SendCommand(text string) (AgentResponse, error) {
	if strings.TrimSpace(text) == "" {
		return AgentResponse{Error: &AgentError{
			Code:    "empty_command",
			Message: "Type a command first.",
		}}, nil
	}
	// A turn can take most of a minute against a local model, and a user who
	// changed their mind should not have to watch it finish.
	ctx, stop := context.WithCancel(context.Background())
	a.mu.Lock()
	a.stopTurn = stop
	a.mu.Unlock()

	defer func() {
		a.mu.Lock()
		a.stopTurn = nil
		a.mu.Unlock()
		stop()
	}()

	// Bound to the thread the question was asked in. A turn can take most of
	// a minute, and by the time it finishes the user may be reading another
	// conversation; the answer belongs to the one that asked.
	asked := a.threads.add(ChatMessage{From: "you", Text: text})

	response := a.agent.SendContext(ctx, text)
	a.threads.addTo(asked, chatMessage(response))
	a.journal.record(JournalEntry{
		Command: text,
		Answer:  response.Message,
		Steps:   response.Steps,
		Error:   response.Error,
	})

	// Told which conversation this answers, so a window showing a different
	// one does not draw it.
	response.Conversation = asked
	return response, nil
}

// chatMessage turns a response into the line the thread keeps, which is what
// makes a reopened conversation look like the one that was left.
func chatMessage(response AgentResponse) ChatMessage {
	return ChatMessage{
		From:    "tonelab",
		Text:    response.Message,
		Changed: response.Changed,
		Plan:    response.Plan,
		Error:   response.Error,
	}
}

// Forget drops the conversation. A user starting a new idea should not have
// to fight the last one, and a turn that went wrong keeps being wrong while it
// stays in context.
func (a *AgentService) Forget() (AgentResponse, error) {
	a.agent.Forget()
	return AgentResponse{Message: "Started a new conversation."}, nil
}

// Stop ends the turn in flight. Commands already sent to the DAW stay sent,
// which is what undo is for; what stops is the agent deciding to send more.
func (a *AgentService) Stop() (AgentResponse, error) {
	a.mu.Lock()
	stop := a.stopTurn
	a.mu.Unlock()

	if stop == nil {
		return AgentResponse{Error: &AgentError{
			Code:    "nothing_running",
			Message: "Nothing is running.",
		}}, nil
	}
	stop()
	return AgentResponse{Message: "Stopping."}, nil
}

// History is what the agent has done, newest first. Read by the UI rather than
// only written to a terminal, since the person who needs it is the one whose
// project changed.
func (a *AgentService) History() ([]JournalEntry, error) {
	return a.journal.list(), nil
}

// GetDAWStatus infers connection from recent feedback. Nothing about sending
// OSC reveals whether anything received it, so hearing from the DAW is the
// only positive evidence available.
func (a *AgentService) GetDAWStatus() (DAWStatus, error) {
	if a.liveness == nil {
		return DAWStatus{Detail: "This DAW backend cannot report whether it is connected."}, nil
	}

	// Recent feedback is proof enough, and costs nothing.
	if lastSeen := a.liveness.LastSeen(); !lastSeen.IsZero() && time.Since(lastSeen) <= dawSilenceLimit {
		return DAWStatus{Connected: true, Detail: "Receiving feedback from the DAW."}, nil
	}

	// Otherwise ask, rather than reading quiet as absence.
	if a.liveness.Probe(probeTimeout) {
		return DAWStatus{Connected: true, Detail: "The DAW answered."}, nil
	}
	return DAWStatus{Detail: "The DAW did not answer. Check it is running and configured to send OSC feedback."}, nil
}

// orchestratorBrain adapts the agent package to this boundary, keeping the
// contract's types out of the agent and the agent's out of the frontend.
type orchestratorBrain struct {
	orchestrator *agent.Orchestrator
}

// previewBrain wraps the preview orchestrator, whose changing tools are
// disarmed, so a preview cannot reach the project even by mistake.
type previewBrain struct {
	orchestrator *agent.Orchestrator
	live         *agent.Orchestrator
}

func (p previewBrain) Preview(text string) AgentResponse {
	return convert(p.orchestrator.Send(text))
}

func (p previewBrain) Apply(plan []PlannedCall) AgentResponse {
	calls := make([]agent.PlannedCall, 0, len(plan))
	for _, step := range plan {
		calls = append(calls, agent.PlannedCall{Tool: step.Tool, Arguments: step.Arguments})
	}
	// Applied through the live agent's tools, since the preview's are
	// deliberately incapable of changing anything.
	return convert(p.live.Apply(calls))
}

func (o orchestratorBrain) SendContext(ctx context.Context, text string) AgentResponse {
	return convert(o.orchestrator.SendContext(ctx, text))
}

func (o orchestratorBrain) Forget() {
	o.orchestrator.Forget()
}

func (o orchestratorBrain) Recall() []Exchange {
	remembered := o.orchestrator.Recall()
	exchanges := make([]Exchange, 0, len(remembered))
	for _, one := range remembered {
		exchanges = append(exchanges, Exchange{Question: one.Question, Answer: one.Answer})
	}
	return exchanges
}

func (o orchestratorBrain) Restore(exchanges []Exchange) {
	remembered := make([]agent.Exchange, 0, len(exchanges))
	for _, one := range exchanges {
		remembered = append(remembered, agent.Exchange{Question: one.Question, Answer: one.Answer})
	}
	o.orchestrator.Restore(remembered)
}

// convert moves an agent response across the UI boundary, keeping the
// contract's types out of the agent and the agent's out of the frontend.
func convert(response agent.Response) AgentResponse {

	converted := AgentResponse{Message: response.Message}
	if response.Error != nil {
		converted.Error = &AgentError{Code: response.Error.Code, Message: response.Error.Message}
	}

	// Taken from what the tools confirmed rather than from the model's
	// summary, so the UI cannot show a change the DAW never made.
	for _, change := range response.Changed {
		converted.Changed = append(converted.Changed, ParamChange{
			Track:     change.Track,
			Param:     change.Param,
			NewValue:  change.Confirmed,
			Requested: change.Requested,
			Note:      change.Note,
		})
	}
	for _, step := range response.Steps {
		converted.Steps = append(converted.Steps, JournalStep{
			Tool:      step.Tool,
			Arguments: step.Arguments,
			Outcome:   step.Outcome,
			Failed:    step.Failed,
		})
	}
	for _, step := range response.Plan {
		converted.Plan = append(converted.Plan, PlannedCall{
			Tool:        step.Tool,
			Arguments:   step.Arguments,
			Description: step.Description,
		})
	}
	return converted
}
