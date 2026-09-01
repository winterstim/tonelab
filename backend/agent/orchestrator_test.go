package agent_test

import (
	"strings"
	"testing"

	"tonelab/backend/agent"
	"tonelab/backend/agent/llmtest"
)

func newOrchestrator(t *testing.T, backend *fakeDAW, turns ...llmtest.Turn) (*agent.Orchestrator, *llmtest.Server) {
	t.Helper()

	server := llmtest.New(t, turns...)
	config := agent.Config{BaseURL: server.BaseURL(), APIKey: "test-key", Model: "test-model"}
	return agent.NewOrchestrator(config, agent.NewTools(backend)), server
}

// The point of the whole layer: free text in, a real DAW command out.
func TestCommandReachesTheDAW(t *testing.T) {
	backend := newFakeDAW()
	orchestrator, _ := newOrchestrator(t, backend,
		llmtest.Turn{ToolCalls: []llmtest.ToolCall{{
			ID: "call_1", Name: "set_param",
			Arguments: `{"track_id":2,"param_name":"volume","value":0.3}`,
		}}},
		llmtest.Turn{Content: "Turned track 2 down."},
	)

	response := orchestrator.Send("turn track 2 down")

	if response.Error != nil {
		t.Fatalf("expected success, got %+v", response.Error)
	}
	if len(backend.setCalls) != 1 || backend.setCalls[0].track != 2 {
		t.Fatalf("expected one command on track 2, got %v", backend.setCalls)
	}
	if !strings.Contains(response.Message, "track 2") {
		t.Errorf("expected the model's summary to reach the user, got %q", response.Message)
	}
}

// The tools we advertise must match what the model is actually given, since a
// mismatch is invisible until a model calls something that does not exist.
func TestToolsAreSentToTheModel(t *testing.T) {
	backend := newFakeDAW()
	orchestrator, server := newOrchestrator(t, backend, llmtest.Turn{Content: "ok"})

	orchestrator.Send("hello")

	sent := server.Requests()
	if len(sent) != 1 {
		t.Fatalf("expected one call to the endpoint, got %d", len(sent))
	}
	names := map[string]bool{}
	for _, tool := range sent[0].Tools {
		if tool.Type != "function" {
			t.Errorf("expected type \"function\", got %q", tool.Type)
		}
		names[tool.Function.Name] = true
		if tool.Function.Parameters == nil {
			t.Errorf("%s was sent without a schema", tool.Function.Name)
		}
	}
	for _, want := range []string{"get_param", "set_param"} {
		if !names[want] {
			t.Errorf("the model was never told about %s: %v", want, names)
		}
	}
}

// A tool failure must go back to the model as a result it can act on, not end
// the turn. This is the difference between an agent that recovers and one that
// gives up.
func TestToolFailureIsFedBackToTheModel(t *testing.T) {
	backend := newFakeDAW()
	orchestrator, server := newOrchestrator(t, backend,
		llmtest.Turn{ToolCalls: []llmtest.ToolCall{{
			ID: "call_1", Name: "set_param",
			Arguments: `{"track_id":1,"param_name":"reverb","value":0.5}`,
		}}},
		llmtest.Turn{ToolCalls: []llmtest.ToolCall{{
			ID: "call_2", Name: "set_param",
			Arguments: `{"track_id":1,"param_name":"volume","value":0.5}`,
		}}},
		llmtest.Turn{Content: "Done."},
	)
	response := orchestrator.Send("add reverb")

	if response.Error != nil {
		t.Fatalf("expected the loop to recover, got %+v", response.Error)
	}
	second := server.Requests()[1]
	body := ""
	for _, msg := range second.Messages {
		body += string(msg)
	}
	if !strings.Contains(body, "param_not_found") {
		t.Fatalf("expected the failure code to reach the model, got: %s", body)
	}
	if !strings.Contains(body, "call_1") {
		t.Error("expected the result to be tied to the tool call it answers")
	}
}

// The spec warns that a model "does not always generate valid JSON" for tool
// arguments, so this is documented behaviour rather than a hypothetical.
func TestMalformedToolArgumentsAreReportedToTheModel(t *testing.T) {
	backend := newFakeDAW()
	orchestrator, server := newOrchestrator(t, backend,
		llmtest.Turn{ToolCalls: []llmtest.ToolCall{{
			ID: "call_1", Name: "set_param", Arguments: `{"track_id":`,
		}}},
		llmtest.Turn{Content: "Sorry, retrying."},
	)

	response := orchestrator.Send("turn it down")

	if response.Error != nil {
		t.Fatalf("expected the loop to continue, got %+v", response.Error)
	}
	body := ""
	for _, msg := range server.Requests()[1].Messages {
		body += string(msg)
	}
	if !strings.Contains(body, "invalid_arguments") {
		t.Fatalf("expected the model to be told its arguments were invalid, got: %s", body)
	}
}

// HTTP failures are the endpoint's, not the DAW's, and the user needs to be
// told which, so they come back as their own code.
func TestEndpointFailuresBecomeStructuredErrors(t *testing.T) {
	for _, tc := range []struct {
		name     string
		turn     llmtest.Turn
		wantCode string
	}{
		{"unauthorized", llmtest.Turn{Status: 401, Body: `{"error":{"message":"bad key","type":"invalid_request_error","param":null,"code":"invalid_api_key"}}`}, "llm_unauthorized"},
		{"rate limited", llmtest.Turn{Status: 429, Body: `{"error":{"message":"slow down","type":"rate_limit_error","param":null,"code":null}}`}, "llm_rate_limited"},
		{"server error", llmtest.Turn{Status: 500, Body: `{"error":{"message":"boom","type":"server_error","param":null,"code":null}}`}, "llm_unavailable"},
		{"not json", llmtest.Turn{Status: 200, Body: `<html>proxy error</html>`}, "llm_unreadable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			orchestrator, _ := newOrchestrator(t, newFakeDAW(), tc.turn)

			response := orchestrator.Send("anything")

			if response.Error == nil {
				t.Fatal("expected a structured error, got success")
			}
			if response.Error.Code != tc.wantCode {
				t.Fatalf("expected %q, got %q (%s)", tc.wantCode, response.Error.Code, response.Error.Message)
			}
		})
	}
}

// An unreachable endpoint is the most likely failure of all, since the user
// configures the URL themselves and a local runtime may simply not be running.
func TestUnreachableEndpointIsReported(t *testing.T) {
	config := agent.Config{BaseURL: "http://127.0.0.1:1/v1", APIKey: "k", Model: "m"}
	orchestrator := agent.NewOrchestrator(config, agent.NewTools(newFakeDAW()))

	response := orchestrator.Send("anything")

	if response.Error == nil || response.Error.Code != "llm_unreachable" {
		t.Fatalf("expected llm_unreachable, got %+v", response.Error)
	}
}

// A model that keeps calling tools must not loop forever on a live DAW.
func TestToolLoopIsBounded(t *testing.T) {
	backend := newFakeDAW()
	turns := make([]llmtest.Turn, 0, 20)
	for i := 0; i < 20; i++ {
		turns = append(turns, llmtest.Turn{ToolCalls: []llmtest.ToolCall{{
			ID: "call", Name: "set_param",
			Arguments: `{"track_id":1,"param_name":"volume","value":0.5}`,
		}}})
	}
	orchestrator, server := newOrchestrator(t, backend, turns...)

	response := orchestrator.Send("keep going")

	if response.Error == nil || response.Error.Code != "step_limit_reached" {
		t.Fatalf("expected step_limit_reached, got %+v", response.Error)
	}
	if len(server.Requests()) >= 20 {
		t.Fatalf("the loop was not bounded: %d calls", len(server.Requests()))
	}
}
