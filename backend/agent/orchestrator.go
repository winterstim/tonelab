package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

// maxSteps bounds the tool loop. A model that keeps calling tools is driving a
// live DAW, so an unbounded loop is not slow, it is destructive.
const maxSteps = 8

// requestTimeout covers a local model on a slow machine without letting an
// unreachable endpoint hang the UI indefinitely.
const requestTimeout = 120 * time.Second

// Config points at any OpenAI-compatible endpoint, which is what makes a cloud
// key and a local runtime the same code path.
type Config struct {
	BaseURL string
	APIKey  string
	Model   string
}

// Response is what the UI receives. Domain failures arrive as Error rather
// than a Go error, matching the boundary the UI contract defines.
type Response struct {
	Message string
	Error   *Error
}

// Orchestrator runs the tool-calling loop: send the conversation, execute what
// the model asks for, feed the result back, repeat until it answers in prose.
type Orchestrator struct {
	config Config
	tools  *Tools
	http   *http.Client
}

func NewOrchestrator(config Config, tools *Tools) *Orchestrator {
	return &Orchestrator{
		config: config,
		tools:  tools,
		http:   &http.Client{Timeout: requestTimeout},
	}
}

// Wire types, named for the fields the API actually uses. Kept private because
// nothing outside this package should think in the endpoint's vocabulary.
type message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []toolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type toolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name string `json:"name"`
		// A string, not an object: the API generates it as text and warns it
		// may be invalid JSON or carry parameters no schema defined.
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type completionRequest struct {
	Model    string    `json:"model"`
	Messages []message `json:"messages"`
	Tools    []apiTool `json:"tools,omitempty"`
}

type apiTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		Parameters  map[string]any `json:"parameters"`
	} `json:"function"`
}

type completionResponse struct {
	Choices []struct {
		Message      message `json:"message"`
		FinishReason string  `json:"finish_reason"`
	} `json:"choices"`
}

type apiError struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    any    `json:"code"`
	} `json:"error"`
}

// Send runs one user command to completion. Nothing is remembered between
// calls: MVP is stateless by scope, so every command carries its own context.
func (o *Orchestrator) Send(text string) Response {
	conversation := []message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: text},
	}

	for step := 0; step < maxSteps; step++ {
		reply, failure := o.complete(conversation)
		if failure != nil {
			return Response{Error: failure}
		}
		if len(reply.ToolCalls) == 0 {
			return Response{Message: reply.Content}
		}

		conversation = append(conversation, reply)
		for _, call := range reply.ToolCalls {
			conversation = append(conversation, o.execute(call))
		}
	}

	// Reached only by a model that will not stop calling tools. Ending the
	// turn is safer than continuing to command a live DAW.
	return Response{Error: &Error{
		Code:    "step_limit_reached",
		Message: "The assistant kept calling tools without finishing. Try a simpler command.",
	}}
}

// execute runs one tool call and phrases the outcome as a tool message. A
// failure is reported to the model rather than ending the turn, because the
// codes exist precisely so it can choose a different move.
func (o *Orchestrator) execute(call toolCall) message {
	result := o.tools.Call(call.Function.Name, json.RawMessage(call.Function.Arguments))

	body, err := json.Marshal(result)
	if err != nil {
		body = []byte(`{"error":{"code":"internal","message":"the tool result could not be encoded"}}`)
	}
	log.Printf("[agent] tool %s -> %s", call.Function.Name, body)

	return message{Role: "tool", ToolCallID: call.ID, Content: string(body)}
}

// complete performs one request and returns the assistant's reply.
func (o *Orchestrator) complete(conversation []message) (message, *Error) {
	body, err := json.Marshal(completionRequest{
		Model:    o.config.Model,
		Messages: conversation,
		Tools:    o.apiTools(),
	})
	if err != nil {
		return message{}, &Error{Code: "internal", Message: "The request could not be encoded."}
	}

	request, err := http.NewRequest(http.MethodPost, o.config.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return message{}, &Error{Code: "llm_unreachable", Message: "The configured endpoint URL is not usable."}
	}
	request.Header.Set("Content-Type", "application/json")
	if o.config.APIKey != "" {
		request.Header.Set("Authorization", "Bearer "+o.config.APIKey)
	}

	response, err := o.http.Do(request)
	if err != nil {
		// The likeliest failure of all: the user configures this URL, and a
		// local runtime may simply not be running.
		return message{}, &Error{Code: "llm_unreachable", Message: "Could not reach the language model endpoint."}
	}
	defer response.Body.Close()

	payload, err := io.ReadAll(response.Body)
	if err != nil {
		return message{}, &Error{Code: "llm_unreadable", Message: "The endpoint's reply could not be read."}
	}
	if response.StatusCode != http.StatusOK {
		return message{}, httpFailure(response.StatusCode, payload)
	}

	var decoded completionResponse
	if err := json.Unmarshal(payload, &decoded); err != nil {
		// A proxy or a misconfigured URL answering with HTML looks like this.
		return message{}, &Error{Code: "llm_unreadable", Message: "The endpoint did not return a valid response."}
	}
	if len(decoded.Choices) == 0 {
		return message{}, &Error{Code: "llm_unreadable", Message: "The endpoint returned no reply."}
	}
	return decoded.Choices[0].Message, nil
}

// httpFailure separates what the user can act on: a key to fix, a wait to
// take, or an endpoint that is simply down.
func httpFailure(status int, payload []byte) *Error {
	var decoded apiError
	_ = json.Unmarshal(payload, &decoded)
	detail := decoded.Error.Message

	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return &Error{Code: "llm_unauthorized", Message: withDetail("The endpoint rejected the API key.", detail)}
	case status == http.StatusTooManyRequests:
		return &Error{Code: "llm_rate_limited", Message: withDetail("The endpoint is rate limiting requests.", detail)}
	case status >= 500:
		return &Error{Code: "llm_unavailable", Message: withDetail("The endpoint reported an error.", detail)}
	default:
		return &Error{Code: "llm_rejected", Message: withDetail(fmt.Sprintf("The endpoint rejected the request (HTTP %d).", status), detail)}
	}
}

func withDetail(message, detail string) string {
	if detail == "" {
		return message
	}
	return message + " " + detail
}

// apiTools converts the tool definitions into the endpoint's shape at the last
// possible moment, so the rest of the package keeps its own vocabulary.
func (o *Orchestrator) apiTools() []apiTool {
	definitions := o.tools.Definitions()
	converted := make([]apiTool, 0, len(definitions))

	for _, definition := range definitions {
		var tool apiTool
		tool.Type = "function"
		tool.Function.Name = definition.Name
		tool.Function.Description = definition.Description
		tool.Function.Parameters = definition.InputSchema
		converted = append(converted, tool)
	}
	return converted
}

// systemPrompt states the rules the tool schemas cannot: which units values
// use, and that guessing is worse than asking, since a wrong command on a live
// project is real damage.
const systemPrompt = `You control a digital audio workstation through the tools provided.

Numeric values are always normalized between 0.0 and 1.0, never decibels or hertz. Track numbers start at 1.

Use get_param before set_param when a request is relative, such as "a bit quieter".

If a request is ambiguous, or names something the tools do not offer, say so instead of guessing. A wrong command changes a real project.`
