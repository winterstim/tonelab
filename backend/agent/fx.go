package agent

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"tonelab/backend/daw"
)

// fxer is optional, as lister is: a DAW that cannot enumerate effects still
// works for track parameters, and the effect tools are simply not offered.
type fxer interface {
	FXChain(track int, timeout time.Duration) ([]daw.FX, error)
	SetFXParam(track, fx, param int, value float64) error
	ConfirmFXParam(track, fx, param int, timeout time.Duration) (float64, error)
	ReadFXParam(track, fx, param int, timeout time.Duration) (float64, error)
}

// chainTimeout is generous because a chain arrives as one burst and then
// bank by bank; a slow DAW under load must not read as an empty track.
const chainTimeout = 3 * time.Second

// chainTTL is how long an enumerated chain is trusted. Long enough to cover
// a turn's tool calls, which each cost a walk otherwise; short enough that a
// plugin the user adds mid-session is seen next turn.
const chainTTL = 10 * time.Second

// maxMatches bounds what find_params hands back. A model reading more than
// this is reading a dump, which is what the tool exists to avoid.
const maxMatches = 12

// FXSummary is an effect without its parameters, which is what list_fx
// returns: the parameters are reached by search, never by listing.
type FXSummary struct {
	FX         int    `json:"fx_id"`
	Name       string `json:"name"`
	ParamCount int    `json:"param_count"`
}

type ParamMatch struct {
	FX     int    `json:"fx_id"`
	FXName string `json:"fx_name"`
	Param  int    `json:"param_id"`
	Name   string `json:"name"`
}

type AppliedFX struct {
	Track     int      `json:"track"`
	FX        int      `json:"fx_id"`
	FXName    string   `json:"fx_name"`
	Param     int      `json:"param_id"`
	Name      string   `json:"name"`
	Requested float64  `json:"requested"`
	Confirmed *float64 `json:"confirmed,omitempty"`
	Note      string   `json:"note,omitempty"`
}

type ReadFX struct {
	Track  int     `json:"track"`
	FX     int     `json:"fx_id"`
	FXName string  `json:"fx_name"`
	Param  int     `json:"param_id"`
	Name   string  `json:"name"`
	Value  float64 `json:"value"`
}

// chainCache keeps walks from repeating within a turn. Held on Tools rather
// than the backend because it is a policy about tool calls, not about the DAW.
type chainCache struct {
	mu     sync.Mutex
	chains map[int]cachedChain
}

type cachedChain struct {
	chain []daw.FX
	at    time.Time
}

func (t *Tools) chain(track int) ([]daw.FX, *Result) {
	source, ok := t.daw.(fxer)
	if !ok {
		return nil, ptr(failure("not_supported", "This DAW backend cannot enumerate effects."))
	}
	t.chains.mu.Lock()
	defer t.chains.mu.Unlock()
	if t.chains.chains == nil {
		t.chains.chains = make(map[int]cachedChain)
	}
	if cached, ok := t.chains.chains[track]; ok && time.Since(cached.at) < chainTTL {
		return cached.chain, nil
	}
	chain, err := source.FXChain(track, chainTimeout)
	if err != nil {
		return nil, ptr(t.domainFailureFor("", err))
	}
	t.chains.chains[track] = cachedChain{chain: chain, at: time.Now()}
	return chain, nil
}

func ptr(r Result) *Result { return &r }

func (t *Tools) fxDefinitions() []Tool {
	track := map[string]any{"type": "integer", "minimum": 1}
	return []Tool{
		{
			Name: "list_fx",
			Description: "List the effects (plugins) on a track by number and name, with how many " +
				"parameters each has. Does not list the parameters: use find_params for that.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"track_id": track},
				"required":   []string{"track_id"},
			},
		},
		{
			Name: "find_params",
			Description: "Search the parameters of every effect on a track by words, such as " +
				"\"reverb mix\" or \"gain\". Returns the best matches with their fx_id and param_id, " +
				"which set_fx_param and get_fx_param take. Use this instead of guessing indices.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"track_id": track,
					"query":    map[string]any{"type": "string"},
				},
				"required": []string{"track_id", "query"},
			},
		},
		{
			Name:        "get_fx_param",
			Description: "Read an effect parameter by position, as found by find_params. Values are normalized 0.0 to 1.0.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"track_id": track,
					"fx_id":    map[string]any{"type": "integer", "minimum": 1},
					"param_id": map[string]any{"type": "integer", "minimum": 1},
				},
				"required": []string{"track_id", "fx_id", "param_id"},
			},
		},
		{
			Name: "set_fx_param",
			Description: "Set an effect parameter by position, as found by find_params. " +
				"Values are normalized 0.0 to 1.0, never dB, Hz or percent. " +
				"Returns what the DAW reports the value became, which the plugin may have rounded.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"track_id": track,
					"fx_id":    map[string]any{"type": "integer", "minimum": 1},
					"param_id": map[string]any{"type": "integer", "minimum": 1},
					"value":    map[string]any{"type": "number", "minimum": 0, "maximum": 1},
				},
				"required": []string{"track_id", "fx_id", "param_id", "value"},
			},
		},
	}
}

type fxArgs struct {
	TrackID any     `json:"track_id"`
	FXID    any     `json:"fx_id"`
	ParamID any     `json:"param_id"`
	Query   *string `json:"query"`
	Value   any     `json:"value"`
}

func (t *Tools) listFX(args json.RawMessage) Result {
	var decoded fxArgs
	if err := json.Unmarshal(args, &decoded); err != nil {
		return invalidArguments("arguments must be a JSON object")
	}
	track, ok := asTrack(decoded.TrackID)
	if !ok {
		return invalidArguments("track_id must be a positive integer")
	}
	chain, refused := t.chain(track)
	if refused != nil {
		return *refused
	}
	summary := make([]FXSummary, 0, len(chain))
	for _, fx := range chain {
		summary = append(summary, FXSummary{FX: fx.Number, Name: fx.Name, ParamCount: len(fx.Params)})
	}
	return Result{Value: summary}
}

// findParams matches query words against parameter and effect names. It is
// deliberately plain: no synonyms, no dictionary, nothing that would have to
// grow with the plugins a user owns. A word matching the parameter counts
// more than one matching only the effect it sits in.
func (t *Tools) findParams(args json.RawMessage) Result {
	var decoded fxArgs
	if err := json.Unmarshal(args, &decoded); err != nil {
		return invalidArguments("arguments must be a JSON object")
	}
	track, ok := asTrack(decoded.TrackID)
	if !ok {
		return invalidArguments("track_id must be a positive integer")
	}
	if decoded.Query == nil || strings.TrimSpace(*decoded.Query) == "" {
		return invalidArguments("query must be one or more words")
	}
	chain, refused := t.chain(track)
	if refused != nil {
		return *refused
	}
	if len(chain) == 0 {
		return failure("no_fx", fmt.Sprintf("Track %d has no effects.", track))
	}

	words := strings.Fields(strings.ToLower(*decoded.Query))
	type scored struct {
		match ParamMatch
		score int
	}
	var found []scored
	for _, fx := range chain {
		fxName := strings.ToLower(fx.Name)
		for _, param := range fx.Params {
			name := strings.ToLower(param.Name)
			score := 0
			for _, word := range words {
				switch {
				case strings.Contains(name, word):
					score += 2
				case strings.Contains(fxName, word):
					score++
				}
			}
			if score > 0 {
				found = append(found, scored{ParamMatch{fx.Number, fx.Name, param.Number, param.Name}, score})
			}
		}
	}
	if len(found) == 0 {
		names := make([]string, 0, len(chain))
		for _, fx := range chain {
			names = append(names, fx.Name)
		}
		return failure("no_match", fmt.Sprintf("No parameter on track %d matches %q. The effects there are: %s. Try other words, or list_fx.",
			track, *decoded.Query, strings.Join(names, ", ")))
	}
	sort.SliceStable(found, func(i, j int) bool { return found[i].score > found[j].score })
	if len(found) > maxMatches {
		found = found[:maxMatches]
	}
	matches := make([]ParamMatch, 0, len(found))
	for _, one := range found {
		matches = append(matches, one.match)
	}
	return Result{Value: matches}
}

// locate resolves indices against the chain, because the DAW itself is
// silent about an effect that does not exist and silence must not become a
// guess. It also gives the report names a user recognises.
func (t *Tools) locate(args json.RawMessage) (int, daw.FX, daw.FXParam, fxArgs, *Result) {
	var decoded fxArgs
	if err := json.Unmarshal(args, &decoded); err != nil {
		return 0, daw.FX{}, daw.FXParam{}, decoded, ptr(invalidArguments("arguments must be a JSON object"))
	}
	track, ok := asTrack(decoded.TrackID)
	if !ok {
		return 0, daw.FX{}, daw.FXParam{}, decoded, ptr(invalidArguments("track_id must be a positive integer"))
	}
	fxID, ok := asTrack(decoded.FXID)
	if !ok {
		return 0, daw.FX{}, daw.FXParam{}, decoded, ptr(invalidArguments("fx_id must be a positive integer"))
	}
	paramID, ok := asTrack(decoded.ParamID)
	if !ok {
		return 0, daw.FX{}, daw.FXParam{}, decoded, ptr(invalidArguments("param_id must be a positive integer"))
	}
	chain, refused := t.chain(track)
	if refused != nil {
		return 0, daw.FX{}, daw.FXParam{}, decoded, refused
	}
	if fxID > len(chain) {
		return 0, daw.FX{}, daw.FXParam{}, decoded, ptr(failure("unknown_fx",
			fmt.Sprintf("Track %d has %d effects, so there is no fx_id %d. Use list_fx.", track, len(chain), fxID)))
	}
	fx := chain[fxID-1]
	if paramID > len(fx.Params) {
		return 0, daw.FX{}, daw.FXParam{}, decoded, ptr(failure("unknown_param",
			fmt.Sprintf("%q has %d parameters, so there is no param_id %d. Use find_params.", fx.Name, len(fx.Params), paramID)))
	}
	return track, fx, fx.Params[paramID-1], decoded, nil
}

func (t *Tools) getFXParam(args json.RawMessage) Result {
	track, fx, param, _, refused := t.locate(args)
	if refused != nil {
		return *refused
	}
	value, err := t.daw.(fxer).ReadFXParam(track, fx.Number, param.Number, readTimeout)
	if err != nil {
		return failure("value_unknown", fmt.Sprintf("The DAW has not reported %q on %q yet.", param.Name, fx.Name))
	}
	return Result{Value: ReadFX{track, fx.Number, fx.Name, param.Number, param.Name, value}}
}

func (t *Tools) setFXParam(args json.RawMessage) Result {
	track, fx, param, decoded, refused := t.locate(args)
	if refused != nil {
		return *refused
	}
	value, ok := asNumber(decoded.Value)
	if !ok {
		return invalidArguments("value must be a number from 0.0 to 1.0")
	}
	if t.dryRun {
		return Result{Value: Planned{
			Description: fmt.Sprintf("set %q on %q (track %d) to %v", param.Name, fx.Name, track, value),
		}}
	}
	source := t.daw.(fxer)
	if err := source.SetFXParam(track, fx.Number, param.Number, value); err != nil {
		return t.domainFailureFor(param.Name, err)
	}
	applied := AppliedFX{Track: track, FX: fx.Number, FXName: fx.Name, Param: param.Number, Name: param.Name, Requested: value}
	confirmed, err := source.ConfirmFXParam(track, fx.Number, param.Number, confirmTimeout)
	if err != nil {
		applied.Note = "The DAW did not report this parameter back, so the change is unverified."
		return Result{Value: applied}
	}
	applied.Confirmed = &confirmed
	return Result{Value: applied}
}
