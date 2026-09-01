// Package agent exposes the DAW command layer as tools an LLM can call. It
// adds three things that layer deliberately lacks: input schemas, validation
// of what a model actually sent, and failures expressed as codes the model can
// branch on rather than prose it would have to interpret.
package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"tonelab/backend/daw"
)

// How long a read waits for the DAW to answer. Long enough for a local DAW to
// reply over UDP, short enough that an agent turn does not appear to hang.
const readTimeout = 2 * time.Second

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

	return []Tool{
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
	default:
		return failure("unknown_tool", fmt.Sprintf("There is no tool called %q.", name))
	}
}

type getArgs struct {
	TrackID   *int    `json:"track_id"`
	ParamName *string `json:"param_name"`
}

type setArgs struct {
	TrackID   *int    `json:"track_id"`
	ParamName *string `json:"param_name"`
	Value     any     `json:"value"`
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

	source, ok := t.daw.(reader)
	if !ok {
		return failure("not_supported", "This DAW backend cannot read values back.")
	}

	value, err := source.ReadParam(*decoded.TrackID, *decoded.ParamName, readTimeout)
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

	// The schema's oneOf, enforced here because the schema only advises the
	// model while this is what actually runs.
	switch decoded.Value.(type) {
	case float64, bool:
	default:
		return invalidArguments("value must be a number between 0.0 and 1.0, or true/false.")
	}

	if err := t.daw.SetParam(*decoded.TrackID, *decoded.ParamName, decoded.Value); err != nil {
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
