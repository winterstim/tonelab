package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"tonelab/backend/app"
)

// settingFields is what /settings shows and sets, in the order the window
// shows them. Each knows how to read itself from the settings and write a
// typed value back; keys are never read back, only replaced.
type settingField struct {
	name, help string
	get        func(app.Settings) string
	set        func(*app.Settings, string) error
	secret     bool
}

var settingFields = []settingField{
	{name: "url", help: "model endpoint, OpenAI-compatible",
		get: func(s app.Settings) string { return s.BaseURL },
		set: func(s *app.Settings, v string) error { s.BaseURL = v; return nil }},
	{name: "model", help: "model name at that endpoint",
		get: func(s app.Settings) string { return s.Model },
		set: func(s *app.Settings, v string) error { s.Model = v; return nil }},
	{name: "key", help: "api key for the endpoint; off removes it", secret: true,
		get: func(s app.Settings) string { return setOrNot(s.APIKeySet) }},
	{name: "daw", help: "which DAW backend",
		get: func(s app.Settings) string { return s.DAWBackend },
		set: func(s *app.Settings, v string) error { s.DAWBackend = v; return nil }},
	{name: "host", help: "where the DAW listens for OSC",
		get: func(s app.Settings) string { return s.DAWHost },
		set: func(s *app.Settings, v string) error { s.DAWHost = v; return nil }},
	{name: "port", help: "the DAW's OSC port",
		get: func(s app.Settings) string { return strconv.Itoa(s.DAWPort) },
		set: func(s *app.Settings, v string) error { return setInt(&s.DAWPort, v) }},
	{name: "feedback", help: "the port the DAW answers on",
		get: func(s app.Settings) string { return strconv.Itoa(s.DAWFeedback) },
		set: func(s *app.Settings, v string) error { return setInt(&s.DAWFeedback, v) }},
	{name: "preview", help: "propose every command before running it, on or off",
		get: func(s app.Settings) string { return onOff(s.PreviewByDefault) },
		set: func(s *app.Settings, v string) error { return setBool(&s.PreviewByDefault, v) }},
	{name: "search", help: "web search provider, or off",
		get: func(s app.Settings) string { return orOff(s.SearchProvider) },
		set: func(s *app.Settings, v string) error {
			if v == "off" {
				v = ""
			}
			s.SearchProvider = v
			return nil
		}},
	{name: "search_url", help: "the search provider's address, for a local one",
		get: func(s app.Settings) string { return s.SearchURL },
		set: func(s *app.Settings, v string) error { s.SearchURL = v; return nil }},
	{name: "search_key", help: "api key for the search provider; off removes it and turns search off", secret: true,
		get: func(s app.Settings) string { return setOrNot(s.SearchKeySet) }},
}

func setOrNot(set bool) string {
	if set {
		return "set"
	}
	return "not set"
}

func onOff(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

func orOff(v string) string {
	if v == "" {
		return "off"
	}
	return v
}

func setInt(target *int, v string) error {
	n, err := strconv.Atoi(v)
	if err != nil {
		return fmt.Errorf("%q is not a number", v)
	}
	*target = n
	return nil
}

func setBool(target *bool, v string) error {
	switch strings.ToLower(v) {
	case "on", "true", "yes":
		*target = true
	case "off", "false", "no":
		*target = false
	default:
		return fmt.Errorf("%q is not on or off", v)
	}
	return nil
}

// renderSettings is the whole table, the way the window's settings screen
// lays it out, with keys shown as set or not and never as themselves.
func renderSettings(runtime *app.Runtime) string {
	current, err := runtime.Settings.Get()
	if err != nil {
		return theme.Error.Render(err.Error())
	}
	var lines []string
	for _, f := range settingFields {
		value := f.get(current)
		shown := theme.Text.Render(value)
		if value == "" {
			value, shown = "empty", theme.Muted.Render("empty")
		}
		pad := strings.Repeat(" ", max(34-len(value), 2))
		lines = append(lines, theme.Surface.Render(fmt.Sprintf("%-12s", f.name))+shown+pad+theme.Muted.Render(f.help))
	}
	lines = append(lines, "", theme.Muted.Render("/settings <name> <value> changes one. DAW backends: "+strings.Join(current.DAWAvailable, ", ")+"; search providers: "+strings.Join(current.SearchAvailable, ", ")))
	return strings.Join(lines, "\n")
}

// applySetting writes one field through the same service the window
// uses, so validation and what applies without a restart are decided in
// one place.
func applySetting(runtime *app.Runtime, name, value string) string {
	current, err := runtime.Settings.Get()
	if err != nil {
		return theme.Error.Render(err.Error())
	}
	var apiKey, searchKey string
	found := false
	for _, f := range settingFields {
		if f.name != name {
			continue
		}
		found = true
		// "off" is how a key is removed from a prompt: an empty value would
		// read as "keep", and the key itself is never shown to be cleared.
		switch {
		case f.name == "key" && value == "off":
			current.DropAPIKey = true
		case f.name == "key":
			apiKey = value
		case f.name == "search_key" && value == "off":
			current.DropSearchKey = true
		case f.name == "search_key":
			searchKey = value
		default:
			if err := f.set(&current, value); err != nil {
				return theme.Error.Render(err.Error())
			}
		}
	}
	if !found {
		return theme.Error.Render("no setting called " + name + "; /settings lists them")
	}
	result, _ := runtime.Settings.Save(current, apiKey, searchKey)
	if result.Error != nil {
		return theme.Error.Render(result.Error.Message)
	}
	if result.RestartNeeded {
		return theme.Warning.Render(result.Message)
	}
	return theme.Success.Render(result.Message)
}

func isSecret(name string) bool {
	for _, f := range settingFields {
		if f.name == name && f.secret {
			return true
		}
	}
	return false
}

// renderAccount is the subscription as /account shows it: who, which
// plan, and each window as a bar with when it resets.
func renderAccount(runtime *app.Runtime) string {
	status, _ := runtime.Hosted.Status()
	if !status.SignedIn {
		return theme.Muted.Render("not signed in; /login signs in with a Tonelab subscription")
	}
	plan := "no plan yet"
	if status.Plan != "" {
		plan = "on the " + status.Plan + " plan"
	}
	lines := []string{theme.Text.Render(status.Email) + " " + theme.Muted.Render(plan)}
	if status.Error != "" {
		return strings.Join(append(lines, theme.Error.Render(status.Error)), "\n")
	}
	if !status.Active {
		return strings.Join(append(lines, theme.Warning.Render(status.Reason)), "\n")
	}
	for _, w := range []struct {
		name        string
		used, limit int64
		at          time.Time
	}{{"this month", status.Month.Used, status.Month.Limit, status.Month.ResetsAt}, {"today", status.Day.Used, status.Day.Limit, status.Day.ResetsAt}, {"searches", status.Searches.Used, status.Searches.Limit, status.Searches.ResetsAt}} {
		share := 0.0
		if w.limit > 0 {
			share = float64(w.used) / float64(w.limit)
		}
		filled := min(20, int(share*20+0.5))
		bar := theme.Success
		switch {
		case share >= 1:
			bar = theme.Error
		case share >= 0.8:
			bar = theme.Warning
		}
		lines = append(lines, fmt.Sprintf("%-12s%s%s  %s", w.name, bar.Render(strings.Repeat("█", filled)), theme.Muted.Render(strings.Repeat("░", 20-filled)), theme.Muted.Render(fmt.Sprintf("%d%%, resets %s", int(share*100), untilReset(w.at)))))
	}
	return strings.Join(lines, "\n")
}

func untilReset(at time.Time) string {
	left := time.Until(at)
	switch {
	case left <= 0:
		return "now"
	case left < 48*time.Hour:
		return fmt.Sprintf("in %dh", max(1, int(left.Hours()+0.5)))
	}
	return fmt.Sprintf("in %dd", int(left.Hours()/24+0.5))
}
