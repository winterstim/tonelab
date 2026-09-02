package agent_test

import (
	"strconv"
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

// Some endpoints validate tool calls against our schema and reject the request
// before the call reaches Tonelab, so no tool result can carry the problem
// back. Measured on Groq: one model in six attempts emitted its tool call as
// untyped text and was rejected this way. Without a retry the turn is lost
// even though the model can fix it when told.
func TestEndpointRejectedToolCallIsRetried(t *testing.T) {
	backend := newFakeDAW()
	orchestrator, server := newOrchestrator(t, backend,
		llmtest.Turn{
			Status: 400,
			Body:   `{"error":{"message":"tool call validation failed: parameters for tool set_param did not match schema: errors: [` + "`/value`" + `: expected number or boolean, but got string]","type":"invalid_request_error","code":"tool_use_failed"}}`,
		},
		llmtest.Turn{ToolCalls: []llmtest.ToolCall{{
			ID: "call_1", Name: "set_param",
			Arguments: `{"track_id":2,"param_name":"mute","value":true}`,
		}}},
		llmtest.Turn{Content: "Muted."},
	)

	response := orchestrator.Send("mute track 2")

	if response.Error != nil {
		t.Fatalf("expected the loop to recover from the rejection, got %+v", response.Error)
	}
	if len(backend.setCalls) != 1 {
		t.Fatalf("expected the corrected command to reach the DAW, got %v", backend.setCalls)
	}

	// The model has to be told what was wrong, or the retry is a coin toss.
	body := ""
	for _, msg := range server.Requests()[1].Messages {
		body += string(msg)
	}
	if !strings.Contains(body, "expected number or boolean") {
		t.Fatalf("expected the rejection's reason to reach the model, got: %s", body)
	}
}

// A rejection the model cannot fix must not be retried into the step limit.
func TestUnfixableRejectionsAreNotRetried(t *testing.T) {
	orchestrator, server := newOrchestrator(t, newFakeDAW(),
		llmtest.Turn{Status: 400, Body: `{"error":{"message":"model not found","type":"invalid_request_error"}}`},
	)

	response := orchestrator.Send("anything")

	if response.Error == nil || response.Error.Code != "llm_rejected" {
		t.Fatalf("expected llm_rejected, got %+v", response.Error)
	}
	if len(server.Requests()) != 1 {
		t.Fatalf("expected no retry, got %d calls", len(server.Requests()))
	}
}

// A free tier's quota refills in seconds, and the endpoint says how long.
// Waiting is what makes a hosted endpoint behave like a local runtime from
// the user's side, which is the whole claim of one pluggable endpoint.
func TestRateLimitedRequestWaitsAndRetries(t *testing.T) {
	backend := newFakeDAW()
	orchestrator, server := newOrchestrator(t, backend,
		llmtest.Turn{
			Status: 429,
			Body:   `{"error":{"message":"Rate limit reached for model. Please try again in 0.2s.","type":"rate_limit_error"}}`,
		},
		llmtest.Turn{Content: "Done."},
	)

	response := orchestrator.Send("anything")

	if response.Error != nil {
		t.Fatalf("expected the wait to recover the turn, got %+v", response.Error)
	}
	if len(server.Requests()) != 2 {
		t.Fatalf("expected one retry, got %d calls", len(server.Requests()))
	}
}

// A limit that does not refill on the scale it claims must reach the user
// rather than keep them waiting on a turn that will not complete.
func TestRepeatedRateLimitsAreReported(t *testing.T) {
	orchestrator, _ := newOrchestrator(t, newFakeDAW(),
		llmtest.Turn{Status: 429, Body: `{"error":{"message":"Rate limit reached. Please try again in 0.1s."}}`},
		llmtest.Turn{Status: 429, Body: `{"error":{"message":"Rate limit reached. Please try again in 0.1s."}}`},
	)

	response := orchestrator.Send("anything")

	if response.Error == nil || response.Error.Code != "llm_rate_limited" {
		t.Fatalf("expected llm_rate_limited, got %+v", response.Error)
	}
}

// A limit measured in minutes is not something to sit through.
func TestLongRateLimitsAreNotWaitedOut(t *testing.T) {
	orchestrator, server := newOrchestrator(t, newFakeDAW(),
		llmtest.Turn{Status: 429, Body: `{"error":{"message":"Rate limit reached. Please try again in 3600s."}}`},
	)

	response := orchestrator.Send("anything")

	if response.Error == nil || response.Error.Code != "llm_rate_limited" {
		t.Fatalf("expected llm_rate_limited, got %+v", response.Error)
	}
	if len(server.Requests()) != 1 {
		t.Fatalf("expected no wait, got %d calls", len(server.Requests()))
	}
}

// A model whose tool-call format is wrong tends to stay wrong. Correcting it
// forever would spend the whole step budget and then report "too many steps",
// hiding the reason the turn actually failed.
func TestPersistentSchemaRejectionsReportTheirRealCause(t *testing.T) {
	rejection := llmtest.Turn{
		Status: 400,
		Body:   `{"error":{"message":"tool call validation failed: parameters for tool set_param did not match schema: errors: [expected boolean, but got string]"}}`,
	}
	turns := make([]llmtest.Turn, 0, 8)
	for i := 0; i < 8; i++ {
		turns = append(turns, rejection)
	}
	orchestrator, server := newOrchestrator(t, newFakeDAW(), turns...)

	response := orchestrator.Send("mute track 2")

	if response.Error == nil || response.Error.Code != "llm_tool_call_invalid" {
		t.Fatalf("expected the rejection's own code, got %+v", response.Error)
	}
	if !strings.Contains(response.Error.Message, "expected boolean") {
		t.Errorf("expected the endpoint's reason to survive, got %q", response.Error.Message)
	}
	// Bounded well below the step limit, so the loop cannot spend its whole
	// budget on one call it will never get right.
	if len(server.Requests()) > 6 {
		t.Errorf("expected the retries to be bounded, got %d calls", len(server.Requests()))
	}
}

// Choosing a tool and filling a schema is not writing, and endpoints default
// to sampling that produces malformed calls we then have to work around.
func TestRequestsAskForDeterministicToolCalls(t *testing.T) {
	orchestrator, server := newOrchestrator(t, newFakeDAW(), llmtest.Turn{Content: "ok"})

	orchestrator.Send("anything")

	if temperature := server.Requests()[0].Temperature; temperature != 0 {
		t.Fatalf("expected temperature 0, got %v", temperature)
	}
}

// A UI needs what the DAW confirmed, not the model's summary, since the model
// is the one account of the turn that cannot check itself.
func TestConfirmedChangesAreReported(t *testing.T) {
	backend := newFakeDAW()
	backend.values["volume"] = 0.3
	orchestrator, _ := newOrchestrator(t, backend,
		llmtest.Turn{ToolCalls: []llmtest.ToolCall{{
			ID: "call_1", Name: "set_param",
			Arguments: `{"track_id":4,"param_name":"volume","value":0.3}`,
		}}},
		llmtest.Turn{Content: "Done."},
	)

	response := orchestrator.Send("turn track 4 down")

	if len(response.Changed) != 1 {
		t.Fatalf("expected one reported change, got %v", response.Changed)
	}
	change := response.Changed[0]
	if change.Track != 4 || change.Param != "volume" || change.Confirmed != 0.3 {
		t.Fatalf("unexpected change %+v", change)
	}
}

// A turn that only answers a question must report nothing changed, or a UI
// would show a change list for a command that touched nothing.
func TestAnswersReportNoChanges(t *testing.T) {
	orchestrator, _ := newOrchestrator(t, newFakeDAW(), llmtest.Turn{Content: "Track 2 is called Vocals."})

	response := orchestrator.Send("what is track 2 called")

	if len(response.Changed) != 0 {
		t.Fatalf("expected no changes, got %v", response.Changed)
	}
}

// Preview runs the turn for its plan without touching the project, which is
// the guardrail: the agent shows what it means to do before doing
// it.
func TestPreviewPlansWithoutChangingAnything(t *testing.T) {
	backend := newFakeDAW()
	server := llmtest.New(t,
		llmtest.Turn{ToolCalls: []llmtest.ToolCall{{
			ID: "call_1", Name: "set_param",
			Arguments: `{"track_id":2,"param_name":"volume","value":0.3}`,
		}}},
		llmtest.Turn{Content: "I would turn track 2 down."},
	)
	orchestrator := agent.NewOrchestrator(
		agent.Config{BaseURL: server.BaseURL(), Model: "test"},
		agent.NewPreviewTools(backend),
	)

	response := orchestrator.Send("turn track 2 down")

	if response.Error != nil {
		t.Fatalf("expected success, got %+v", response.Error)
	}
	if len(backend.setCalls) != 0 {
		t.Fatalf("a preview reached the DAW: %v", backend.setCalls)
	}
	if len(response.Plan) != 1 {
		t.Fatalf("expected one planned call, got %v", response.Plan)
	}
	if !strings.Contains(response.Plan[0].Description, "track 2 volume") {
		t.Errorf("expected the plan to read plainly, got %q", response.Plan[0].Description)
	}
}

// The plan is held in the form the model produced, so applying it runs what
// the user approved. Asking a model to repeat itself can produce something
// else, and the user would have approved the first while getting the second.
func TestApplyingAPlanDoesNotAskTheModelAgain(t *testing.T) {
	backend := newFakeDAW()
	// One turn scripted: a second request would exhaust it and fail.
	server := llmtest.New(t, llmtest.Turn{Content: "unused"})
	orchestrator := agent.NewOrchestrator(
		agent.Config{BaseURL: server.BaseURL(), Model: "test"},
		agent.NewTools(backend),
	)

	response := orchestrator.Apply([]agent.PlannedCall{{
		Tool:      "set_param",
		Arguments: `{"track_id":2,"param_name":"volume","value":0.3}`,
	}})

	if response.Error != nil {
		t.Fatalf("expected success, got %+v", response.Error)
	}
	if len(server.Requests()) != 0 {
		t.Fatalf("applying consulted the model %d times", len(server.Requests()))
	}
	if len(backend.setCalls) != 1 || backend.setCalls[0].track != 2 {
		t.Fatalf("expected the approved command, got %v", backend.setCalls)
	}
	if len(response.Changed) != 1 {
		t.Errorf("expected the change to be reported, got %v", response.Changed)
	}
}

// A plan whose step fails stops there. The rest may depend on it, and guessing
// which is worse than saying where it stopped.
func TestApplyingStopsAtTheFirstFailure(t *testing.T) {
	backend := newFakeDAW()
	orchestrator := agent.NewOrchestrator(agent.Config{BaseURL: "http://unused", Model: "m"}, agent.NewTools(backend))

	response := orchestrator.Apply([]agent.PlannedCall{
		{Tool: "set_param", Arguments: `{"track_id":1,"param_name":"volume","value":0.3}`},
		{Tool: "set_param", Arguments: `{"track_id":1,"param_name":"nonsense","value":0.3}`},
		{Tool: "set_param", Arguments: `{"track_id":1,"param_name":"pan","value":0.3}`},
	})

	if response.Error == nil {
		t.Fatal("expected the failure to be reported")
	}
	if len(backend.setCalls) != 1 {
		t.Fatalf("expected only the first step to have run, got %v", backend.setCalls)
	}
	if len(response.Changed) != 1 {
		t.Errorf("expected what did happen to be reported, got %v", response.Changed)
	}
}

// Reads still run in preview, since a plan made without looking at the project
// would be a guess.
func TestPreviewStillReadsTheProject(t *testing.T) {
	backend := newFakeDAW()
	server := llmtest.New(t,
		llmtest.Turn{ToolCalls: []llmtest.ToolCall{{ID: "call_1", Name: "list_tracks", Arguments: `{}`}}},
		llmtest.Turn{Content: "Track 2 is the vocals."},
	)
	orchestrator := agent.NewOrchestrator(
		agent.Config{BaseURL: server.BaseURL(), Model: "test"},
		agent.NewPreviewTools(backend),
	)

	response := orchestrator.Send("which track is the vocals")

	if response.Error != nil {
		t.Fatalf("expected success, got %+v", response.Error)
	}
	body := ""
	for _, msg := range server.Requests()[1].Messages {
		body += string(msg)
	}
	if !strings.Contains(body, "Vocals") {
		t.Fatalf("expected the read to have run, got: %s", body)
	}
}

// A chat interface invites follow-ups, and without memory they fail in a way
// that reads as the agent being stupid rather than as the product having none.
func TestFollowUpsCarryTheConversation(t *testing.T) {
	backend := newFakeDAW()
	orchestrator, server := newOrchestrator(t, backend,
		llmtest.Turn{Content: "Track 2 is the vocals."},
		llmtest.Turn{Content: "Turned them down."},
	)

	orchestrator.Send("which track is the vocals")
	orchestrator.Send("turn them down a bit")

	second := server.Requests()[1]
	body := ""
	for _, msg := range second.Messages {
		body += string(msg)
	}
	if !strings.Contains(body, "which track is the vocals") {
		t.Errorf("the earlier question did not reach the model: %s", body)
	}
	if !strings.Contains(body, "Track 2 is the vocals.") {
		t.Errorf("the earlier answer did not reach the model: %s", body)
	}
}

// Memory that grows without limit spends a fortune in tokens and buries the
// current request under history.
func TestMemoryIsBounded(t *testing.T) {
	backend := newFakeDAW()
	turns := make([]llmtest.Turn, 0, 20)
	for i := 0; i < 20; i++ {
		turns = append(turns, llmtest.Turn{Content: "answer " + strconv.Itoa(i)})
	}
	orchestrator, server := newOrchestrator(t, backend, turns...)

	for i := 0; i < 19; i++ {
		orchestrator.Send("question " + strconv.Itoa(i))
	}

	last := server.Requests()[18]
	// A system message, the remembered exchanges, and the current question.
	if len(last.Messages) > 2+agent.MaxRememberedForTest*2 {
		t.Fatalf("expected the conversation to be trimmed, got %d messages", len(last.Messages))
	}
	body := ""
	for _, msg := range last.Messages {
		body += string(msg)
	}
	if strings.Contains(body, "question 0") {
		t.Error("the oldest exchange should have been dropped")
	}
	if !strings.Contains(body, "question 17") {
		t.Error("the most recent exchange should have been kept")
	}
}

// A user starting a new idea should not have to fight the last one, and a
// wrong turn left in context keeps being wrong.
func TestForgettingClearsTheConversation(t *testing.T) {
	backend := newFakeDAW()
	orchestrator, server := newOrchestrator(t, backend,
		llmtest.Turn{Content: "First answer."},
		llmtest.Turn{Content: "Second answer."},
	)

	orchestrator.Send("first question")
	orchestrator.Forget()
	orchestrator.Send("second question")

	body := ""
	for _, msg := range server.Requests()[1].Messages {
		body += string(msg)
	}
	if strings.Contains(body, "first question") {
		t.Errorf("the forgotten turn came back: %s", body)
	}
}

// A turn that failed leaves nothing to refer back to, so it must not be
// remembered as if it had happened.
func TestFailedTurnsAreNotRemembered(t *testing.T) {
	backend := newFakeDAW()
	orchestrator, server := newOrchestrator(t, backend,
		llmtest.Turn{Status: 500, Body: `{"error":{"message":"boom"}}`},
		llmtest.Turn{Content: "Fine now."},
	)

	orchestrator.Send("first question")
	orchestrator.Send("second question")

	body := ""
	for _, msg := range server.Requests()[1].Messages {
		body += string(msg)
	}
	if strings.Contains(body, "first question") {
		t.Errorf("a failed turn was remembered: %s", body)
	}
}
