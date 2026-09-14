// Package agent exposes the DAW command layer as tools an LLM can call. It
// adds three things that layer deliberately lacks: input schemas, validation
// of what a model actually sent, and failures expressed as codes the model can
// branch on rather than prose it would have to interpret.
package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"tonelab/backend/daw"
)

// How long a read waits for the DAW to answer. Long enough for a local DAW to
// reply over UDP, short enough that an agent turn does not appear to hang.
const readTimeout = 2 * time.Second

// confirmTimeout is shorter than a read the user asked for: this one is a
// check on our own work, and a DAW that stays quiet leaves the command
// accepted rather than failed.
const confirmTimeout = 700 * time.Millisecond

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

// reverser is optional because not every DAW can be asked to take something
// back, and a tool that cannot work is worse than one that is absent.
type reverser interface {
	Undo() error
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
//
// GetParam is the same reading without the wait, which confirmation needs: a
// DAW reporting only changes says nothing about a value that was already
// right, and waiting for an announcement that will never come would call a
// correct command unverified.
type reader interface {
	ReadParam(track int, name string, timeout time.Duration) (any, error)
	ConfirmParam(track int, name string, timeout time.Duration) (any, error)
	GetParam(track int, name string) (any, error)
}

// Tools wraps one DAW backend. It holds no parameter list of its own: names
// resolve against whatever the backend reports, so a DAW with a different set
// needs no change here.
type Tools struct {
	daw daw.Client

	// dryRun makes the changing tools describe themselves instead of acting.
	// Reads still run: seeing the plan is worth nothing if the agent could
	// not look at the project to make one.
	dryRun bool
}

func NewTools(client daw.Client) *Tools {
	return &Tools{daw: client}
}

// NewPreviewTools builds the same tools with the changing ones disarmed, so a
// turn can be run for its plan without touching the project. Set once at
// construction rather than toggled, since a flag flipped on a shared object is
// a race with someone else's command.
func NewPreviewTools(client daw.Client) *Tools {
	return &Tools{daw: client, dryRun: true}
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
					// A type union rather than oneOf, which was measured
					// against a live model: given oneOf it answered "true"
					// as a string and misnamed the parameter, and given
					// this it answered correctly. Coercion still stands
					// behind it, since a schema only advises.
					"value": map[string]any{
						"type":        []string{"number", "boolean"},
						"minimum":     0,
						"maximum":     1,
						"description": "A number from 0.0 to 1.0 for continuous parameters, or true/false for on-off parameters.",
					},
				},
				"required": []string{"track_id", "param_name", "value"},
			},
		},
	}

	// Offered only when the backend can answer it. The other tools address
	// tracks by number, so a user saying "the vocals" is unanswerable without
	// this, and a tool that always fails is worse than one that is absent.
	if _, ok := t.daw.(reverser); ok {
		definitions = append(definitions, Tool{
			Name: "undo",
			Description: "Reverse the DAW's last change. Use this when the user asks to undo, " +
				"take something back, or revert what was just done.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		})
	}

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
	case "undo":
		return t.undo()
	default:
		return failure("unknown_tool", fmt.Sprintf("There is no tool called %q.", name))
	}
}

// Planned is what a changing tool returns in preview: what it would do, said
// plainly enough for a user to accept or reject before anything happens.
type Planned struct {
	Description string `json:"would"`
}

// Reverted deliberately reports what was asked of the DAW rather than a bare
// success. Undo reverses the DAW's last change, which is not necessarily ours:
// a user who moved a fader by hand since has that taken back instead, so
// claiming "your command was undone" would be a claim we cannot make.
type Reverted struct {
	Note string `json:"note"`
}

// undo asks the DAW to take back its last change. Reading the result back is
// left to the caller: which parameters to check depends on what was done, and
// guessing here would mean keeping state this layer has no other reason to
// hold.
func (t *Tools) undo() Result {
	source, ok := t.daw.(reverser)
	if !ok {
		return failure("not_supported", "This DAW backend cannot undo.")
	}
	if t.dryRun {
		return Result{Value: Planned{Description: "undo the DAW's last change"}}
	}
	if err := source.Undo(); err != nil {
		return failure("daw_command_failed", "The DAW did not accept the undo.")
	}

	return Result{Value: Reverted{
		Note: "The DAW reversed its most recent change. That is the DAW's last change, " +
			"which may not be the one just made if anything else has happened since. " +
			"Use get_param to state what a value is now rather than assuming.",
	}}
}

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

// Applied is what a set reports back: what the DAW says the value is now,
// where it can be checked, and an honest note where it cannot.
type Applied struct {
	Track     int    `json:"track"`
	Param     string `json:"param"`
	Requested any    `json:"requested"`

	// Confirmed is what the DAW reported after the change. Absent when the
	// DAW does not report this parameter, which is not a failure but is
	// something the model must not present as confirmation.
	Confirmed any    `json:"confirmed,omitempty"`
	Note      string `json:"note,omitempty"`
}

// confirm reads the value back so the model's answer rests on the DAW's
// account rather than ours. A parameter the DAW never reports still succeeds:
// the command was accepted, and saying so plainly beats both a false
// confirmation and a false failure.
func (t *Tools) confirm(track int, name string, requested any) Applied {
	applied := Applied{Track: track, Param: name, Requested: requested}

	source, ok := t.daw.(reader)
	if !ok {
		applied.Note = "This DAW cannot report values back, so the change is unverified."
		return applied
	}

	// A DAW reporting only transitions is silent when the value was already
	// what was asked for, so setting mute on an already muted track would
	// otherwise time out and be called unverified.
	if current, err := source.GetParam(track, name); err == nil && current == requested {
		applied.Confirmed = current
		return applied
	}

	// Nor is there anything to wait for on a parameter the backend says it
	// never reports, and spending the timeout to learn that helps nobody.
	if param, err := daw.FindParameter(t.daw, name); err == nil && !param.Readable {
		applied.Note = "This DAW does not report " + name + " back, so the change is unverified."
		return applied
	}

	// Confirming, not asking: only a reading from after the change counts,
	// since the cached one is exactly the value we are trying to disprove.
	value, err := source.ConfirmParam(track, name, confirmTimeout)
	if err != nil {
		applied.Note = "The DAW did not report this parameter back, so the change is unverified."
		return applied
	}
	applied.Confirmed = value
	return applied
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

// What counts as on and off when a model spells a switch as a word. Listed
// rather than inferred, so widening the policy is a deliberate edit with a
// test beside it instead of a guess made at runtime.
var (
	spelledTrue  = []string{"true", "yes", "on", "1"}
	spelledFalse = []string{"false", "no", "off", "0"}
)

// asBool reads a switch from what models actually send. Observed live: asked
// to mute a track, a model sent "true" and then 1, and each refusal cost a
// full retry against a slow model while the user waited.
//
// The set is deliberately closed. Anything outside it is the model being wrong
// rather than informal, and it needs to hear that.
func asBool(value any) (bool, bool) {
	switch typed := value.(type) {
	case bool:
		return typed, true
	case float64:
		if typed == 0 || typed == 1 {
			return typed == 1, true
		}
	case string:
		word := strings.ToLower(strings.TrimSpace(typed))
		for _, yes := range spelledTrue {
			if word == yes {
				return true, true
			}
		}
		for _, no := range spelledFalse {
			if word == no {
				return false, true
			}
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
		text := strings.TrimSpace(typed)

		// A percentage is unambiguous against a 0.0 to 1.0 contract, and it
		// is a natural way to write "half". A unit such as dB or Hz is not:
		// converting it needs the DAW's own curve, which this layer does not
		// have and must not guess at.
		if percent := strings.TrimSuffix(text, "%"); percent != text {
			parsed, err := strconv.ParseFloat(strings.TrimSpace(percent), 64)
			if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
				return 0, false
			}
			return parsed / 100, true
		}

		parsed, err := strconv.ParseFloat(text, 64)
		if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
			// ParseFloat accepts "NaN" and "Inf", which name no value a user
			// asked for, and NaN in particular survives every range check
			// because comparisons against it are false.
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
		return t.domainFailureFor(*decoded.ParamName, err)
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

	if t.dryRun {
		// Validated as far as possible without acting, so a preview shows a
		// command that would fail as failing rather than as planned.
		if _, err := daw.FindParameter(t.daw, *decoded.ParamName); err != nil {
			return t.domainFailureFor(*decoded.ParamName, err)
		}
		return Result{Value: Planned{
			Description: fmt.Sprintf("set track %d %s to %v", track, *decoded.ParamName, value),
		}}
	}

	if err := t.daw.SetParam(track, *decoded.ParamName, value); err != nil {
		return t.domainFailureFor(*decoded.ParamName, err)
	}

	// Confirmed rather than assumed. The command left over a socket that
	// guarantees nothing, and the model reports to a user on the strength of
	// what we return here, so "sent" and "done" must not be the same word.
	return Result{Value: t.confirm(track, *decoded.ParamName, value)}
}

// domainFailureFor maps the DAW layer's sentinels onto codes, each a different
// recovery for the agent. An unknown name gets the list of names that exist:
// a refusal that names the alternatives turns a lost turn into a corrected one.
func (t *Tools) domainFailureFor(name string, err error) Result {
	if errors.Is(err, daw.ErrUnknownParam) {
		return failure("param_not_found", fmt.Sprintf(
			"This DAW has no parameter called %q. It has: %s.", name, t.parameterNames()))
	}
	return domainFailure(err)
}

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
