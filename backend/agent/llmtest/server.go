// Package llmtest is a fake OpenAI-compatible chat completions endpoint. It
// stands to the agent loop as osctest does to the transport: a real HTTP
// server speaking the documented wire format, so tests cover our own encoding
// and error handling without a model, a key, or a network.
//
// Shapes follow openai/openai-openapi. Two of its statements drive the design
// here: tool call arguments are a *string* that "the model does not always
// generate valid JSON" for and "may hallucinate parameters not defined by your
// function schema", and finish_reason is one of stop, length, content_filter,
// tool_calls.
package llmtest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// ToolCall is one call the fake model asks for. Arguments is raw text on
// purpose, so a test can send the malformed JSON a real model sometimes does.
type ToolCall struct {
	ID        string
	Name      string
	Arguments string
}

// Turn is one scripted reply. A Server plays its turns in order, which is what
// makes a multi step tool loop testable: call a tool, see the result, answer.
type Turn struct {
	// Content is the assistant's text. Ignored when ToolCalls is set, since
	// the API reports content as null alongside tool calls.
	Content string

	ToolCalls []ToolCall

	// Status and Body override the reply entirely, for the failures a real
	// endpoint produces: 401, 429, 500, or a body that is not JSON at all.
	Status int
	Body   string

	// Delay stands in for a slow model, which is a local runtime's normal
	// state rather than an exceptional one.
	Delay time.Duration
}

// Server records what was sent to it, because the request is half the contract
// and a loop that sends malformed tools would otherwise pass unnoticed.
type Server struct {
	*httptest.Server

	mu       sync.Mutex
	turns    []Turn
	requests []Request
}

// Request is the decoded body of one call, in the fields this project cares
// about rather than the whole schema.
type Request struct {
	Model       string            `json:"model"`
	Temperature float64           `json:"temperature"`
	Messages    []json.RawMessage `json:"messages"`
	Tools       []struct {
		Type     string `json:"type"`
		Function struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			Parameters  json.RawMessage `json:"parameters"`
		} `json:"function"`
	} `json:"tools"`
}

// New starts a server that plays turns in order. Running out of turns is a
// test failure rather than a silent empty reply, since it means the loop asked
// for more than the test expected.
func New(t *testing.T, turns ...Turn) *Server {
	t.Helper()

	server := &Server{turns: turns}
	server.Server = httptest.NewServer(http.HandlerFunc(server.handle))
	t.Cleanup(server.Close)
	return server
}

// URL is the base a client should be pointed at, matching how a real endpoint
// is configured.
func (s *Server) BaseURL() string {
	return s.Server.URL + "/v1"
}

// Requests returns what the client actually sent.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.requests...)
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var decoded Request
	_ = json.NewDecoder(r.Body).Decode(&decoded)
	s.requests = append(s.requests, decoded)

	if len(s.turns) == 0 {
		http.Error(w, `{"error":{"message":"the test scripted no further turns","type":"test_error","param":null,"code":null}}`, http.StatusInternalServerError)
		return
	}
	turn := s.turns[0]
	s.turns = s.turns[1:]

	if turn.Delay > 0 {
		time.Sleep(turn.Delay)
	}

	if turn.Status != 0 {
		w.WriteHeader(turn.Status)
		w.Write([]byte(turn.Body))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(completion(turn)))
}

// completion builds the response body by hand rather than from Go structs, so
// the wire format under test is the documented one and not a mirror of our own
// types. A bug shared by encoder and decoder would otherwise cancel out.
func completion(turn Turn) string {
	type function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	}
	type toolCall struct {
		ID       string   `json:"id"`
		Type     string   `json:"type"`
		Function function `json:"function"`
	}
	message := map[string]any{"role": "assistant"}
	finish := "stop"

	if len(turn.ToolCalls) > 0 {
		calls := make([]toolCall, 0, len(turn.ToolCalls))
		for _, call := range turn.ToolCalls {
			calls = append(calls, toolCall{
				ID:       call.ID,
				Type:     "function",
				Function: function{Name: call.Name, Arguments: call.Arguments},
			})
		}
		message["content"] = nil
		message["tool_calls"] = calls
		finish = "tool_calls"
	} else {
		message["content"] = turn.Content
	}

	body, _ := json.Marshal(map[string]any{
		"id":      "chatcmpl-test",
		"object":  "chat.completion",
		"created": 0,
		"model":   "test-model",
		"choices": []any{map[string]any{
			"index":         0,
			"message":       message,
			"finish_reason": finish,
		}},
	})
	return string(body)
}
