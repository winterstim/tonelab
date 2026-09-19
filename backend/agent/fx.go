package agent

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
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

// FXList and ParamMatches carry a note when the chain came from the store,
// so the model knows the indices are last session's until confirmed.
type FXList struct {
	FX   []FXSummary `json:"fx"`
	Note string      `json:"note,omitempty"`
}

type ParamMatches struct {
	Matches []ParamMatch `json:"matches"`
	Note    string       `json:"note,omitempty"`
}

type ParamMatch struct {
	FX     int    `json:"fx_id"`
	FXName string `json:"fx_name"`
	Param  int    `json:"param_id"`
	Name   string `json:"name"`
	// Kind tells the model what a value means here: absent for a
	// continuous control, "switch" for on/off, "list" with Steps for a
	// choice, "discrete" for stepped with the count unknown.
	Kind  string `json:"kind,omitempty"`
	Steps int    `json:"steps,omitempty"`
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

// staleNote goes with a chain that was read from the store rather than
// walked this run. Reads may use it; a write waits for a fresh walk first.
const staleNote = "From the last session; the DAW is being read now, and any change is checked against it first."

// chain answers reads. It never blocks on a known chain.
func (t *Tools) chain(track int) ([]daw.FX, bool, *Result) {
	source, ok := t.daw.(fxer)
	if !ok {
		return nil, false, ptr(failure("not_supported", "This DAW backend cannot enumerate effects."))
	}
	chain, stale, err := t.cache().get(source, track, false)
	if err != nil {
		return nil, false, ptr(t.domainFailureFor("", err))
	}
	return chain, stale, nil
}

// freshChain answers writes, and waits for the DAW's current account.
func (t *Tools) freshChain(track int) ([]daw.FX, *Result) {
	source, ok := t.daw.(fxer)
	if !ok {
		return nil, ptr(failure("not_supported", "This DAW backend cannot enumerate effects."))
	}
	chain, _, err := t.cache().get(source, track, true)
	if err != nil {
		return nil, ptr(t.domainFailureFor("", err))
	}
	return chain, nil
}

func (t *Tools) cache() *ChainCache {
	t.chainsMu.Lock()
	defer t.chainsMu.Unlock()
	if t.chains == nil {
		t.chains = NewChainCache(nil)
	}
	return t.chains
}

// ShareChains makes this Tools serve chains from a cache built by the app,
// usually one with a store behind it and shared with the preview tools.
func (t *Tools) ShareChains(c *ChainCache) {
	t.chainsMu.Lock()
	defer t.chainsMu.Unlock()
	t.chains = c
}

func ptr(r Result) *Result { return &r }

func (t *Tools) fxDefinitions() []Tool {
	track := map[string]any{"type": "integer", "minimum": 1}
	return []Tool{
		{
			Name:        "list_fx",
			Description: "List the effects on a track by number and name. For parameters use find_params.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"track_id": track},
				"required":   []string{"track_id"},
			},
		},
		{
			Name: "find_params",
			Description: "Find effect parameters on a track by words (\"reverb mix\"). Returns fx_id and param_id for " +
				"set_fx_param/get_fx_param. kind \"switch\" takes 0 or 1; \"list\" with N steps takes (position-1)/(N-1); " +
				"no kind is continuous 0.0 to 1.0.",
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
			Description: "Read an effect parameter found by find_params. 0.0 to 1.0.",
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
			Description: "Set an effect parameter found by find_params. 0.0 to 1.0, never dB, Hz or percent; " +
				"a switch takes 0 or 1. Returns what the DAW reports, which the plugin may round.",
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
	chain, stale, refused := t.chain(track)
	if refused != nil {
		return *refused
	}
	summary := make([]FXSummary, 0, len(chain))
	for _, fx := range chain {
		summary = append(summary, FXSummary{FX: fx.Number, Name: fx.Name, ParamCount: len(fx.Params)})
	}
	return Result{Value: FXList{FX: summary, Note: noteIf(stale)}}
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
	chain, stale, refused := t.chain(track)
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
				found = append(found, scored{ParamMatch{FX: fx.Number, FXName: fx.Name, Param: param.Number, Name: param.Name, Kind: param.Kind, Steps: param.Steps}, score})
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
	return Result{Value: ParamMatches{Matches: matches, Note: noteIf(stale)}}
}

func noteIf(stale bool) string {
	if stale {
		return staleNote
	}
	return ""
}

// locate resolves indices against the chain, because the DAW itself is
// silent about an effect that does not exist and silence must not become a
// guess. It also gives the report names a user recognises. It waits for a
// chain walked this run: an index from a stored chain may point at another
// plugin now, and both a read and a write by index would land there.
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
	chain, refused := t.freshChain(track)
	if refused != nil {
		return 0, daw.FX{}, daw.FXParam{}, decoded, refused
	}
	if fxID > len(chain) {
		return 0, daw.FX{}, daw.FXParam{}, decoded, ptr(failure("unknown_fx",
			fmt.Sprintf("Track %d has %d effects, so there is no fx_id %d. Use list_fx.", track, len(chain), fxID)))
	}
	fx := chain[fxID-1]
	// By number, not position: a backend may list only the parameters
	// worth naming and keep the DAW's own numbering for the rest.
	for _, param := range fx.Params {
		if param.Number == paramID {
			return track, fx, param, decoded, nil
		}
	}
	return 0, daw.FX{}, daw.FXParam{}, decoded, ptr(failure("unknown_param",
		fmt.Sprintf("%q has no param_id %d among its %d named parameters. Use find_params.", fx.Name, paramID, len(fx.Params))))
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
