// Package agent exposes the DAW command layer as tools an LLM can call. It
// adds three things that layer deliberately lacks: input schemas, validation
// of what a model actually sent, and failures expressed as codes the model can
// branch on rather than prose it would have to interpret.
package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"tonelab/backend/daw"
)

// How long a read waits for the DAW to answer. Long enough for a local DAW to
// reply over UDP, short enough that an agent turn does not appear to hang.
const readTimeout = 2 * time.Second

// listTimeout is per track, since enumerating means looking at each in turn.
// Short, because a track that does not answer promptly is the end of the list
// rather than a slow one.
const listTimeout = 700 * time.Millisecond

// Tool is one callable definition, in the shape an OpenAI-compatible endpoint
// expects.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

// InputSchemaJSON is what actually goes on the wire, and what tests inspect.
func (t Tool) InputSchemaJSON() string {
	encoded, err := json.Marshal(t.InputSchema)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}

// Error is the structured failure every domain problem comes back as, never a
// Go error. Code is for the agent's own recovery, Message is safe
// to show a user directly.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Result carries either a value or an Error, so a caller never has to decide
// whether an empty value meant failure.
type Result struct {
	Value any    `json:"value,omitempty"`
	Error *Error `json:"error,omitempty"`
}

// lister is optional because a DAW that cannot name its tracks still works by
// number, and the tool is simply not offered rather than offered and broken.
type lister interface {
	Tracks(timeout time.Duration) ([]daw.Track, error)
}

// reader is the read half, kept separate because not every backend can answer
// and a caller must be told so rather than handed a silent zero. It is the
// waiting form: asking a DAW and reading the reply are one operation from
// here, since a caller has no way to know when the answer has arrived.
type reader interface {
	ReadParam(track int, name string, timeout time.Duration) (any, error)
}

// Tools wraps one DAW backend. It holds no parameter list of its own: names
// resolve against whatever the backend reports, so a DAW with a different set
// needs no change here.
type Tools struct {
	daw daw.Client
}

func NewTools(client daw.Client) *Tools {
	return &Tools{daw: client}
}

// Definitions describes the two generic tools. param_name stays a
// free string in the schema, since an enum there would be the hardcoding that
// decision exists to avoid; the currently valid names reach the model through
// the description instead, read from the backend.
func (t *Tools) Definitions() []Tool {
	known := t.parameterNames()

	definitions := []Tool{
		{
			Name: "get_param",
			Description: fmt.Sprintf(
				"Read the current value of a named track parameter. Valid names right now: %s.",
				known),
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"track_id":   map[string]any{"type": "integer", "minimum": 1},
					"param_name": map[string]any{"type": "string"},
				},
				"required": []string{"track_id", "param_name"},
			},
		},
		{
			Name: "set_param",
			Description: fmt.Sprintf(
				"Set a named track parameter. Numeric values are normalized 0.0 to 1.0, "+
					"never dB or Hz; on/off parameters take true or false. Valid names right now: %s.",
				known),
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"track_id":   map[string]any{"type": "integer", "minimum": 1},
					"param_name": map[string]any{"type": "string"},
					"value": map[string]any{
						"oneOf": []any{
							map[string]any{"type": "number", "minimum": 0, "maximum": 1},
							map[string]any{"type": "boolean"},
						},
					},
				},
				"required": []string{"track_id", "param_name", "value"},
			},
		},
	}

	// Offered only when the backend can answer it. The other tools address
	// tracks by number, so a user saying "the vocals" is unanswerable without
	// this, and a tool that always fails is worse than one that is absent.
	if _, ok := t.daw.(lister); ok {
		definitions = append(definitions, Tool{
			Name: "list_tracks",
			Description: "List the project's tracks with their numbers and names. " +
				"Use this to turn a track the user named, such as \"the vocals\", into a track number.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		})
	}
	return definitions
}

// parameterNames reads the backend rather than a constant, so the model is
// told what this DAW actually supports.
func (t *Tools) parameterNames() string {
	params, err := daw.ParametersOf(t.daw)
	if err != nil {
		return "unknown, this DAW cannot list them"
	}
	names := make([]string, 0, len(params))
	for _, param := range params {
		names = append(names, param.Name)
	}
	return strings.Join(names, ", ")
}

// Call dispatches by name. It returns no Go error at all: a model calling a
// tool wrongly is an ordinary outcome it must be able to read and retry from,
// not a transport fault.
func (t *Tools) Call(name string, args json.RawMessage) Result {
	switch name {
	case "get_param":
		return t.getParam(args)
	case "set_param":
		return t.setParam(args)
	case "list_tracks":
		return t.listTracks()
	default:
		return failure("unknown_tool", fmt.Sprintf("There is no tool called %q.", name))
	}
}

// track_id is decoded loosely because models quote numbers. Rejecting a
// correct answer over its quotes fails the user for the model's formatting.
// listTracks reads the project's shape. It takes no arguments: the DAW knows
// what it contains, and asking the model how many tracks to look for would be
// asking it to guess.
func (t *Tools) listTracks() Result {
	source, ok := t.daw.(lister)
	if !ok {
		return failure("not_supported", "This DAW backend cannot list tracks.")
	}

	tracks, err := source.Tracks(listTimeout)
	if err != nil {
		return failure("daw_command_failed", "Could not read the project's tracks from the DAW.")
	}
	return Result{Value: tracks}
}

// coerce turns what the model sent into what the parameter takes, or explains
// precisely what it should have sent. A parameter the backend does not have is
// left to the DAW layer to reject, which already words that failure well.
func (t *Tools) coerce(name string, value any) (any, *Result) {
	param, err := daw.FindParameter(t.daw, name)
	if err != nil {
		return value, nil
	}

	if param.Kind == daw.Toggle {
		on, ok := asBool(value)
		if !ok {
			failure := invalidArguments(fmt.Sprintf("%s is on or off, so value must be true or false.", name))
			return nil, &failure
		}
		return on, nil
	}

	number, ok := asNumber(value)
	if !ok {
		failure := invalidArguments(fmt.Sprintf("%s takes a number between 0.0 and 1.0, where 1.0 is the maximum.", name))
		return nil, &failure
	}
	return number, nil
}

type getArgs struct {
	TrackID   any     `json:"track_id"`
	ParamName *string `json:"param_name"`
}

type setArgs struct {
	TrackID   any     `json:"track_id"`
	ParamName *string `json:"param_name"`
	Value     any     `json:"value"`
}

// asBool accepts a JSON boolean, the words spelled as a string, or 0 and 1.
// Observed live: a model asked to mute a track sent "true" and then 1, and
// each refusal cost a full retry against a slow model. None of those readings
// is ambiguous, and refusing them serves nobody.
func asBool(value any) (bool, bool) {
	switch typed := value.(type) {
	case bool:
		return typed, true
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(typed))
		if err != nil {
			return false, false
		}
		return parsed, true
	case float64:
		if typed == 0 || typed == 1 {
			return typed == 1, true
		}
	}
	return false, false
}

// asNumber accepts a JSON number, or a string holding nothing but a number.
// Observed live: a model answered a schema saying "number" with "0.5", getting
// the command entirely right and the type wrong. Leniency stops at ambiguity,
// so "loud" and "-6dB" are still refusals rather than guesses.
func asNumber(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		if err != nil {
			return 0, false
		}
		return parsed, true
	}
	return 0, false
}

func asTrack(value any) (int, bool) {
	number, ok := asNumber(value)
	if !ok || number != float64(int(number)) {
		return 0, false
	}
	return int(number), true
}

// getParam asks the DAW and waits for the reply. A DAW that only announces
// changes may never have mentioned the value, and the reply travels back
// asynchronously, so an immediate read would find an empty cache.
func (t *Tools) getParam(args json.RawMessage) Result {
	var decoded getArgs
	if err := json.Unmarshal(args, &decoded); err != nil {
		return invalidArguments(err.Error())
	}
	if decoded.TrackID == nil || decoded.ParamName == nil {
		return invalidArguments("track_id and param_name are both required.")
	}
	track, ok := asTrack(decoded.TrackID)
	if !ok {
		return invalidArguments("track_id must be a whole number, counting from 1.")
	}

	source, ok := t.daw.(reader)
	if !ok {
		return failure("not_supported", "This DAW backend cannot read values back.")
	}

	value, err := source.ReadParam(track, *decoded.ParamName, readTimeout)
	if err != nil {
		return domainFailure(err)
	}
	return Result{Value: value}
}

func (t *Tools) setParam(args json.RawMessage) Result {
	var decoded setArgs
	if err := json.Unmarshal(args, &decoded); err != nil {
		return invalidArguments(err.Error())
	}
	if decoded.TrackID == nil || decoded.ParamName == nil || decoded.Value == nil {
		return invalidArguments("track_id, param_name and value are all required.")
	}
	track, ok := asTrack(decoded.TrackID)
	if !ok {
		return invalidArguments("track_id must be a whole number, counting from 1.")
	}

	// Coerced against the kind this particular parameter takes, so a refusal
	// can say what was wanted. A model reading "value must be a number or
	// true/false" has to guess which, and each guess is another slow round
	// trip against the DAW's user.
	value, failure := t.coerce(*decoded.ParamName, decoded.Value)
	if failure != nil {
		return *failure
	}

	if err := t.daw.SetParam(track, *decoded.ParamName, value); err != nil {
		return domainFailure(err)
	}
	return Result{}
}

// domainFailure maps the DAW layer's sentinels onto codes. Each is a different
// recovery for the agent: rename, pick another track, clamp, resend, or wait.
func domainFailure(err error) Result {
	switch {
	case errors.Is(err, daw.ErrUnknownParam):
		return failure("param_not_found", "This DAW has no parameter by that name.")
	case errors.Is(err, daw.ErrInvalidTrack):
		return failure("invalid_track", "Track numbers start at 1.")
	case errors.Is(err, daw.ErrInvalidSend):
		return failure("invalid_send", "Send numbers start at 1.")
	case errors.Is(err, daw.ErrValueOutOfRange):
		return failure("value_out_of_range", "Values must be between 0.0 and 1.0.")
	case errors.Is(err, daw.ErrParamKind):
		return failure("invalid_value_type", "That parameter takes a different kind of value.")
	case errors.Is(err, daw.ErrNotReadable):
		return failure("param_not_readable", "This DAW does not report that parameter's value.")
	case errors.Is(err, daw.ErrValueUnknown):
		return failure("value_unknown", "The DAW has not reported that value yet.")
	default:
		return failure("daw_command_failed", "The DAW did not accept the command.")
	}
}

func invalidArguments(detail string) Result {
	return failure("invalid_arguments", "The call did not match the tool's schema: "+detail)
}

func failure(code, message string) Result {
	return Result{Error: &Error{Code: code, Message: message}}
}
