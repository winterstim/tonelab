package main

import (
	"strings"
	"time"

	"tonelab/backend/agent"
)

// dawSilenceLimit is how long without feedback counts as disconnected. A DAW
// streams position and state continuously, so silence is the only evidence a
// fire-and-forget transport can offer, and this is the line between "quiet"
// and "gone".
const dawSilenceLimit = 5 * time.Second

// AgentResponse and friends are the frontend's contract. They live in
// package main because Wails generates the frontend's types from what a
// service actually returns.
type AgentResponse struct {
	Message      string
	ParamChanged *ParamChange
	Error        *AgentError
}

type ParamChange struct {
	Track    int
	Param    string
	OldValue any
	NewValue any
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
	Send(text string) AgentResponse
}

// liveness is optional: a backend that cannot observe its DAW must be able to
// say so rather than have a connection assumed for it.
type liveness interface {
	LastSeen() time.Time
}

// AgentService is the frontend's entire view of the backend. Every domain
// failure comes back inside AgentResponse; the Go error is reserved for the
// RPC itself breaking.
type AgentService struct {
	agent    brain
	liveness liveness
}

func NewAgentService(agent brain, observer liveness) *AgentService {
	return &AgentService{agent: agent, liveness: observer}
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
	return a.agent.Send(text), nil
}

// GetDAWStatus infers connection from recent feedback. Nothing about sending
// OSC reveals whether anything received it, so hearing from the DAW is the
// only positive evidence available.
func (a *AgentService) GetDAWStatus() (DAWStatus, error) {
	if a.liveness == nil {
		return DAWStatus{Detail: "This DAW backend cannot report whether it is connected."}, nil
	}

	lastSeen := a.liveness.LastSeen()
	if lastSeen.IsZero() {
		return DAWStatus{Detail: "No feedback received yet. Check the DAW is running and configured to send OSC back."}, nil
	}
	if time.Since(lastSeen) > dawSilenceLimit {
		return DAWStatus{Detail: "The DAW has gone quiet."}, nil
	}
	return DAWStatus{Connected: true, Detail: "Receiving feedback from the DAW."}, nil
}

// orchestratorBrain adapts the agent package to this boundary, keeping the
// contract's types out of the agent and the agent's out of the frontend.
type orchestratorBrain struct {
	orchestrator *agent.Orchestrator
}

func (o orchestratorBrain) Send(text string) AgentResponse {
	response := o.orchestrator.Send(text)

	converted := AgentResponse{Message: response.Message}
	if response.Error != nil {
		converted.Error = &AgentError{Code: response.Error.Code, Message: response.Error.Message}
	}
	return converted
}
