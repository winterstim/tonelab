package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"tonelab/backend/app"
)

// describeStep turns one tool call into a sentence, as the window does,
// with a short account of what came back. The tool names and JSON are
// ours, not the user's, and a transcript that reads like a log is one
// nobody reads.
func describeStep(step app.JournalStep) (call, result string) {
	args := map[string]any{}
	json.Unmarshal([]byte(step.Arguments), &args)
	track := fmt.Sprint(args["track_id"])
	name := fmt.Sprint(args["param_name"])
	switch step.Tool {
	case "list_tracks":
		call = "Looked up the tracks"
	case "get_param":
		call = fmt.Sprintf("Read %s on track %s", name, track)
	case "set_param":
		call = fmt.Sprintf("Set %s on track %s to %s", name, track, format(args["value"]))
	case "undo":
		call = "Asked the DAW to undo"
	case "list_fx":
		call = fmt.Sprintf("Looked up the effects on track %s", track)
	case "find_params":
		call = fmt.Sprintf("Searched track %s for %q", track, fmt.Sprint(args["query"]))
	case "get_fx_param":
		call = fmt.Sprintf("Read %s on track %s", fxTarget(step.Outcome, args), track)
	case "set_fx_param":
		call = fmt.Sprintf("Set %s on track %s to %s", fxTarget(step.Outcome, args), track, format(args["value"]))
	case "search":
		call = fmt.Sprintf("Searched the web for %q", fmt.Sprint(args["query"]))
	case "fetch_page":
		call = fmt.Sprintf("Read %s", fmt.Sprint(args["url"]))
	default:
		call = step.Tool
	}
	return call, summarize(step)
}

// fxTarget names the effect and parameter from the outcome, where the
// backend put the names a user recognises; the arguments carry only ids.
func fxTarget(outcome string, args map[string]any) string {
	var reply struct {
		Value struct {
			FXName string `json:"fx_name"`
			Name   string `json:"name"`
		} `json:"value"`
	}
	if json.Unmarshal([]byte(outcome), &reply) == nil && reply.Value.Name != "" {
		return reply.Value.FXName + " / " + reply.Value.Name
	}
	return fmt.Sprintf("effect %v parameter %v", args["fx_id"], args["param_id"])
}

// summarize is the one line under a call: a count, a value, or the
// refusal. The full outcome stays in the history for anyone who wants it.
func summarize(step app.JournalStep) string {
	var outcome map[string]any
	if json.Unmarshal([]byte(step.Outcome), &outcome) != nil {
		return ""
	}
	if failure, ok := outcome["error"].(map[string]any); ok {
		return fmt.Sprint(failure["message"])
	}
	value := outcome["value"]
	switch v := value.(type) {
	case float64, bool:
		return format(v)
	case []any:
		return plural(len(v), "result")
	case map[string]any:
		if matches, ok := v["matches"].([]any); ok {
			return plural(len(matches), "match")
		}
		if fx, ok := v["fx"].([]any); ok {
			return plural(len(fx), "effect")
		}
		if confirmed, ok := v["confirmed"]; ok {
			return "confirmed " + format(confirmed)
		}
		if would, ok := v["would"].(string); ok {
			return "would " + would
		}
		if note, ok := v["note"].(string); ok {
			return note
		}
		if reading, ok := v["value"]; ok {
			return format(reading)
		}
		if text, ok := v["text"].(string); ok {
			return fmt.Sprintf("%d characters", len(text))
		}
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	if strings.HasSuffix(noun, "ch") {
		return fmt.Sprintf("%d %ses", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
