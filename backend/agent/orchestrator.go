package agent

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// maxSchemaRetries bounds how often a model is asked to correct a tool call
// the endpoint refused. Set by measurement rather than taste: against a model
// that emits roughly half its calls as untyped text, two retries recovered 2
// runs in 5 and four recovered 4 in 5, while the cost of a retry is one fast
// request. Bounded all the same, because a model that cannot produce the
// format will not learn to, and the user is owed the real reason.
const maxSchemaRetries = 4

// maxWaitForRateLimit caps how long a turn will sit waiting for a quota to
// refill. Free tiers refill in seconds, so a longer wait means the limit is
// not the kind waiting fixes, and the user should hear about it instead.
const maxWaitForRateLimit = 20 * time.Second

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

	// Timeout is how long one request may take. Configurable because a local
	// model on a busy machine is far slower than a hosted one, and because a
	// test cannot spend the default waiting.
	Timeout time.Duration
}

// Response is what the UI receives. Domain failures arrive as Error rather
// than a Go error, matching the boundary the UI contract defines.
type Response struct {
	Message string
	Error   *Error

	// Changed is what the turn did to the DAW, for a UI that shows more than
	// the model's own account of it. Nil when nothing was changed, including
	// when the model only answered a question.
	Changed []Change
}

// Change is one parameter a turn altered, as the DAW reported it afterwards.
type Change struct {
	Track     int
	Param     string
	Requested any
	Confirmed any
	Note      string
}

// Orchestrator runs the tool-calling loop: send the conversation, execute what
// the model asks for, feed the result back, repeat until it answers in prose.
type Orchestrator struct {
	config Config
	tools  *Tools
	http   *http.Client
}

func NewOrchestrator(config Config, tools *Tools) *Orchestrator {
	timeout := config.Timeout
	if timeout == 0 {
		timeout = requestTimeout
	}
	return &Orchestrator{
		config: config,
		tools:  tools,
		http:   &http.Client{Timeout: timeout},
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

	// Zero because this is not writing: the model is choosing a tool and
	// filling in a schema, and sampling variety there buys nothing while
	// costing malformed calls. Endpoints default to 0.7 or higher, so
	// leaving it unset means paying for randomness we then work around.
	Temperature float64 `json:"temperature"`
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

	// Once only: a second wait means the quota is not refilling on the scale
	// the endpoint claimed, and the user is better told than kept waiting.
	waited := false

	rejections := 0
	var changed []Change

	for step := 0; step < maxSteps; step++ {
		reply, failure := o.complete(conversation)
		if failure != nil {
			// A tool call the endpoint itself refused is the same situation
			// as one our tools refused: the model can fix it if told. Some
			// endpoints validate against our schema and reject before the
			// call ever reaches us, so without this the model never learns
			// what was wrong and a correctable turn is lost.
			// A quota that refills in seconds is a pause, not a failure, and
			// endpoints differ in whether they impose one at all. Waiting
			// here is what keeps a hosted endpoint behaving like a local
			// runtime from the user's side.
			if failure.Code == "llm_rate_limited" && !waited {
				if pause, ok := retryAfter(failure.Message); ok {
					waited = true
					time.Sleep(pause)
					continue
				}
			}
			if failure.Code == "llm_tool_call_invalid" && rejections < maxSchemaRetries {
				rejections++
				conversation = append(conversation, message{Role: "user", Content: correctionFor(failure.Message)})
				continue
			}
			return Response{Error: failure}
		}
		if len(reply.ToolCalls) == 0 {
			return Response{Message: reply.Content, Changed: changed}
		}

		conversation = append(conversation, reply)
		for _, call := range reply.ToolCalls {
			result, applied := o.execute(call)
			conversation = append(conversation, result)
			if applied != nil {
				changed = append(changed, *applied)
			}
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
func (o *Orchestrator) execute(call toolCall) (message, *Change) {
	result := o.tools.Call(call.Function.Name, json.RawMessage(call.Function.Arguments))

	body, err := json.Marshal(result)
	if err != nil {
		body = []byte(`{"error":{"code":"internal","message":"the tool result could not be encoded"}}`)
	}
	log.Printf("[agent] tool %s -> %s", call.Function.Name, body)

	// A change is reported to the UI from what the tool confirmed, not from
	// the model's summary, so a display cannot show something the DAW never
	// did.
	var changed *Change
	if applied, ok := result.Value.(Applied); ok {
		changed = &Change{
			Track:     applied.Track,
			Param:     applied.Param,
			Requested: applied.Requested,
			Confirmed: applied.Confirmed,
			Note:      applied.Note,
		}
	}

	return message{Role: "tool", ToolCallID: call.ID, Content: string(body)}, changed
}

// complete performs one request and returns the assistant's reply.
func (o *Orchestrator) complete(conversation []message) (message, *Error) {
	body, err := json.Marshal(completionRequest{
		Model:       o.config.Model,
		Messages:    conversation,
		Tools:       o.apiTools(),
		Temperature: 0,
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
		// A local model can be slow enough to hit the deadline while being
		// perfectly reachable, and calling that "unreachable" sends the user
		// to check a URL that is fine.
		var timeout interface{ Timeout() bool }
		if errors.As(err, &timeout) && timeout.Timeout() {
			return message{}, &Error{
				Code:    "llm_timeout",
				Message: fmt.Sprintf("The model did not answer within %s. A local model may need a smaller context or a smaller model.", o.http.Timeout),
			}
		}
		// Otherwise the likeliest failure of all: the user configures this
		// URL, and a local runtime may simply not be running.
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

	// Told apart from other rejections because the model can correct it,
	// which nothing else in this list can be.
	if status == http.StatusBadRequest && strings.Contains(detail, "did not match schema") {
		return &Error{Code: "llm_tool_call_invalid", Message: detail}
	}

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

// correctionFor tells the model what to send rather than only what was wrong.
// The failure it addresses is a call serialized as text, where "true" and 0.5
// arrive quoted, so an example of the intended shape is more use than a
// restatement of the rule it already had.
func correctionFor(reason string) string {
	return "Your last tool call was rejected by the API: " + reason +
		" Send the call again as JSON with real types, not quoted text. " +
		`For example {"track_id": 2, "param_name": "mute", "value": true}, ` +
		`not {"track_id": "2", "param_name": "mute", "value": "true"}.`
}

// retryAfter reads the wait an endpoint suggests out of its own message.
// Taken from the text because the wait is stated there even when no
// Retry-After header is sent, and a suggested wait is more accurate than a
// number we would invent.
func retryAfter(detail string) (time.Duration, bool) {
	match := retryPattern.FindStringSubmatch(detail)
	if match == nil {
		return 0, false
	}
	seconds, err := strconv.ParseFloat(match[1], 64)
	if err != nil {
		return 0, false
	}

	pause := time.Duration(seconds*float64(time.Second)) + 250*time.Millisecond
	if pause > maxWaitForRateLimit {
		return 0, false
	}
	return pause, true
}

var retryPattern = regexp.MustCompile(`try again in ([0-9.]+)\s*s`)

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

set_param returns what the DAW reports after the change. If it comes back with a note saying the change is unverified, say so rather than claiming the change was confirmed.

When the user names a track instead of numbering it, call list_tracks and match the name yourself. Never guess a track number.

If a request is ambiguous, or names something the tools do not offer, say so instead of guessing. A wrong command changes a real project.`
