package app

import (
	"strings"

	"tonelab/backend/agent"
	"tonelab/backend/config"
	"tonelab/backend/daw"
	"tonelab/backend/search"
)

// Settings is what the settings screen shows and sends back.
//
// The API key is deliberately not among the fields sent out: a key that never
// leaves the backend cannot be read off a screen, copied out of a screenshot,
// or logged by accident. The screen is told whether one is set, and may
// replace it, which is everything a user needs to do with a key they own.
type Settings struct {
	BaseURL   string
	Model     string
	APIKeySet bool

	DAWBackend   string
	DAWHost      string
	DAWPort      int
	DAWFeedback  int
	DAWAvailable []string

	// PreviewByDefault decides whether commands are proposed before they run.
	// A safety choice that belongs to the user rather than to us: some people
	// want to see every change first, and some want the tool to get on with
	// it.
	PreviewByDefault bool

	// Theme is light, dark, or system.
	Theme string

	// Web search is optional; empty provider means off. The key stays in
	// the backend for the same reason the model's does.
	SearchProvider  string
	SearchURL       string
	SearchKeySet    bool
	SearchAvailable []string
}

// SettingsResult reports what a save did, including what it could not do
// without a restart, rather than silently applying half of it.
type SettingsResult struct {
	Saved         bool
	RestartNeeded bool
	Message       string
	Error         *AgentError
}

// SettingsService is the settings screen's whole view of the backend.
type SettingsService struct {
	path     string
	agent    *agent.Orchestrator
	previews *agent.Orchestrator

	// Called with the new provider (nil for none) after a save, so a key
	// corrected in Settings works on the next turn rather than the next launch.
	applySearch func(search.Provider)
}

// theme falls back rather than refusing, since a preference is not worth
// failing a save over, and an unset one is the commonest case.
func theme(name string) string {
	switch name {
	case "light", "dark", "system":
		return name
	default:
		return "system"
	}
}

func NewSettingsService(path string, live, previews *agent.Orchestrator, applySearch func(search.Provider)) *SettingsService {
	return &SettingsService{path: path, agent: live, previews: previews, applySearch: applySearch}
}

// Get reads the file rather than remembering what was loaded at startup, so a
// user who edited it by hand sees the truth.
func (s *SettingsService) Get() (Settings, error) {
	settings, err := config.Load(s.path)
	if err != nil {
		return Settings{DAWAvailable: daw.Backends(), SearchAvailable: search.Providers(), Theme: "system"}, nil
	}

	return Settings{
		BaseURL:          settings.LLM.BaseURL,
		Model:            settings.LLM.Model,
		APIKeySet:        settings.LLM.APIKey != "",
		DAWBackend:       settings.DAW.Backend,
		DAWHost:          settings.DAW.Host,
		DAWPort:          settings.DAW.Port,
		DAWFeedback:      settings.DAW.FeedbackPort,
		DAWAvailable:     daw.Backends(),
		PreviewByDefault: settings.UI.PreviewByDefault,
		Theme:            theme(settings.UI.Theme),
		SearchProvider:   settings.Search.Provider,
		SearchURL:        settings.Search.BaseURL,
		SearchKeySet:     settings.Search.APIKey != "",
		SearchAvailable:  search.Providers(),
	}, nil
}

// Save writes the settings and applies what can be applied without a restart.
// An empty key means "keep the one already there", so a user editing the model
// name does not have to retype a secret they cannot see.
func (s *SettingsService) Save(incoming Settings, apiKey, searchKey string) (SettingsResult, error) {
	existing, err := config.Load(s.path)
	if err != nil {
		// A file that does not load yet is not a reason to refuse the save
		// that would fix it.
		existing = config.Config{}
	}

	updated := config.Config{
		LLM: config.LLM{
			BaseURL: strings.TrimSpace(incoming.BaseURL),
			Model:   strings.TrimSpace(incoming.Model),
			APIKey:  existing.LLM.APIKey,
		},
		DAW: config.DAW{
			Backend:      incoming.DAWBackend,
			Host:         strings.TrimSpace(incoming.DAWHost),
			Port:         incoming.DAWPort,
			FeedbackPort: incoming.DAWFeedback,
		},
		UI: config.UI{
			Theme:            theme(incoming.Theme),
			PreviewByDefault: incoming.PreviewByDefault,
		},
		Search: config.Search{
			Provider: strings.TrimSpace(incoming.SearchProvider),
			BaseURL:  strings.TrimSpace(incoming.SearchURL),
			APIKey:   existing.Search.APIKey,
		},
		// Not on the screen, so carried over: the install's name and the
		// subscription, which the sign-in service edits, not this form.
		Hosted:   existing.Hosted,
		DeviceID: existing.DeviceID,
	}
	if key := strings.TrimSpace(apiKey); key != "" {
		updated.LLM.APIKey = key
	}
	if key := strings.TrimSpace(searchKey); key != "" {
		updated.Search.APIKey = key
	}
	if updated.Search.Provider == "" {
		updated.Search = config.Search{}
	}

	if err := config.Save(s.path, updated); err != nil {
		return SettingsResult{Error: &AgentError{
			Code:    "settings_invalid",
			Message: err.Error(),
		}}, nil
	}

	// The endpoint applies immediately, because a user correcting a key
	// should find out at once whether that was the problem.
	base, key, model, device := updated.AgentConfig()
	llm := agent.Config{BaseURL: base, APIKey: key, Model: model, DeviceID: device}
	s.agent.Reconfigure(llm)
	s.previews.Reconfigure(llm)
	if s.applySearch != nil {
		// Validated by config.Save already, so this cannot fail here.
		provider, _ := search.New(updated.SearchConfig())
		s.applySearch(provider)
	}

	// The DAW connection does not: its transport and listener are opened once
	// at startup, and pretending otherwise would leave the app talking to the
	// old port while the screen showed the new one.
	restart := existing.DAW != updated.DAW
	message := "Saved."
	if restart {
		message = "Saved. The DAW connection changes when the app restarts."
	}
	return SettingsResult{Saved: true, RestartNeeded: restart, Message: message}, nil
}
