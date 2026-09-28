package main

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/winterstim/tonelab/internal/app"
	"github.com/winterstim/tonelab/internal/browser"
	"github.com/winterstim/tonelab/internal/config"
)

// field is one setting this binary changes: the DAW and web search, which
// is all a server whose host brings the model needs.
type field struct {
	name, help string
	get        func(app.Settings) string
	set        func(*app.Settings, string) error
	// secret is read from the terminal without echo when no value is given,
	// so a key does not land in the shell's history.
	secret bool
}

var fields = []field{
	{name: "daw", help: "which DAW",
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
	// Search is set with what the provider needs in one go, since a
	// provider saved without its key or address is saved as off.
	{name: "search", help: "brave (asks for its key), searxng <url>, tonelab (see login), or off",
		get: func(s app.Settings) string { return orOff(s.SearchProvider) }},
	{name: "search_key", help: "the search provider's key; off removes it and turns search off", secret: true,
		get: func(s app.Settings) string {
			if s.SearchKeySet {
				return "set"
			}
			return "not set"
		}},
}

func settingNames() string {
	names := make([]string, len(fields))
	for i, f := range fields {
		names[i] = f.name
	}
	return joined(names)
}

func joined(names []string) string { return strings.Join(names, ", ") }

// settingsService reads and writes the file only: no agent runs here, so
// there is nothing to reconfigure, and the next start reads the change.
func settingsService(path string) *app.SettingsService {
	if _, _, err := config.LoadOrTemplate(path); err != nil {
		fail(err)
	}
	return app.NewSettingsService(path, nil, nil, nil)
}

func showSettings(path string) {
	current, err := settingsService(path).Get()
	if err != nil {
		fail(err)
	}
	for _, f := range fields {
		fmt.Printf("%-11s %-14s %s\n", f.name, f.get(current), f.help)
	}
	fmt.Printf("\nDAWs: %s. Search: %s.\n%s\n", joined(current.DAWAvailable), joined(current.SearchAvailable), path)
}

func setSetting(path, name string, values []string) {
	var chosen *field
	for i := range fields {
		if fields[i].name == name {
			chosen = &fields[i]
		}
	}
	if chosen == nil {
		fail(fmt.Errorf("no setting %q; one of %s", name, settingNames()))
	}
	value := strings.TrimSpace(strings.Join(values, " "))
	if value == "" && chosen.secret {
		value = readSecret(chosen.name)
	}
	if value == "" {
		fail(fmt.Errorf("%s needs a value", name))
	}

	service := settingsService(path)
	current, err := service.Get()
	if err != nil {
		fail(err)
	}
	var searchKey string
	switch {
	case name == "search":
		searchKey = setSearch(&current, values)
	case !chosen.secret:
		if err := chosen.set(&current, value); err != nil {
			fail(fmt.Errorf("%s: %w", name, err))
		}
	case value == "off":
		current.DropSearchKey = true
	default:
		searchKey = value
	}
	result, err := service.Save(current, "", searchKey)
	if err != nil {
		fail(err)
	}
	if result.Error != nil {
		fail(errors.New(result.Error.Message))
	}
	fmt.Println("Saved. A host picks it up the next time it starts Tonelab.")
}

// setSearch sets the provider with what it needs, and returns a key to
// save with it, if one was asked for.
func setSearch(current *app.Settings, values []string) string {
	provider := values[0]
	switch provider {
	case "off":
		current.SearchProvider, current.SearchURL = "", ""
		return ""
	case "tonelab":
		fail(errors.New("search through Tonelab comes with signing in: tonelab-mcp login"))
	case "searxng":
		if len(values) < 2 {
			fail(errors.New("searxng needs its address: tonelab-mcp set search searxng http://localhost:8080"))
		}
		current.SearchProvider, current.SearchURL = provider, values[1]
		return ""
	}
	if !slices.Contains(current.SearchAvailable, provider) {
		fail(fmt.Errorf("no search provider %q; one of %s", provider, joined(current.SearchAvailable)))
	}
	current.SearchProvider, current.SearchURL = provider, ""
	if current.SearchKeySet {
		return ""
	}
	key := readSecret(provider + " key")
	if key == "" {
		fail(fmt.Errorf("%s needs its key; run this in a terminal to be asked for it", provider))
	}
	return key
}

func readSecret(name string) string {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return ""
	}
	fmt.Fprintf(os.Stderr, "%s: ", name)
	secret, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		fail(err)
	}
	return strings.TrimSpace(string(secret))
}

func setInt(target *int, value string) error {
	n, err := strconv.Atoi(value)
	if err != nil || n < 1 || n > 65535 {
		return errors.New("a port is a number from 1 to 65535")
	}
	*target = n
	return nil
}

func orOff(value string) string {
	if value == "" {
		return "off"
	}
	return value
}

// login signs in to Tonelab for web search on a plan. The DAW tools need
// no account; this is only for search without a key of one's own.
func login(path string) {
	if _, _, err := config.LoadOrTemplate(path); err != nil {
		fail(err)
	}
	hosted := app.NewHostedService(path, nil, nil, nil, browser.Open)
	state, _ := hosted.SignIn("")
	if state.Error != "" {
		fail(errors.New(state.Error))
	}
	fmt.Printf("Open %s\nand check the code is %s.\n", state.VerifyURL, state.UserCode)
	for state.Running {
		time.Sleep(2 * time.Second)
		state, _ = hosted.State()
	}
	if !state.Done {
		fail(errors.New(orOff(state.Error)))
	}
	fmt.Println("Signed in. Web search goes through your Tonelab plan from the next start.")
}

func logout(path string) {
	if _, err := app.NewHostedService(path, nil, nil, nil, nil).SignOut(); err != nil {
		fail(err)
	}
	fmt.Println("Signed out.")
}
