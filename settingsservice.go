package main

import (
	"strings"

	"tonelab/backend/agent"
	"tonelab/backend/config"
	"tonelab/backend/daw"
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

	// Accent is colour or mono.
	Accent string
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

func accent(name string) string {
	if name == "mono" {
		return "mono"
	}
	return "colour"
}

func NewSettingsService(path string, live, previews *agent.Orchestrator) *SettingsService {
	return &SettingsService{path: path, agent: live, previews: previews}
}

// Get reads the file rather than remembering what was loaded at startup, so a
// user who edited it by hand sees the truth.
func (s *SettingsService) Get() (Settings, error) {
	settings, err := config.Load(s.path)
	if err != nil {
		return Settings{DAWAvailable: daw.Backends(), Theme: "system", Accent: "colour"}, nil
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
		Accent:           accent(settings.UI.Accent),
	}, nil
}

// Save writes the settings and applies what can be applied without a restart.
// An empty key means "keep the one already there", so a user editing the model
// name does not have to retype a secret they cannot see.
func (s *SettingsService) Save(incoming Settings, apiKey string) (SettingsResult, error) {
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
			Accent:           accent(incoming.Accent),
			PreviewByDefault: incoming.PreviewByDefault,
		},
	}
	if key := strings.TrimSpace(apiKey); key != "" {
		updated.LLM.APIKey = key
	}

	if err := config.Save(s.path, updated); err != nil {
		return SettingsResult{Error: &AgentError{
			Code:    "settings_invalid",
			Message: err.Error(),
		}}, nil
	}

	// The endpoint applies immediately, because a user correcting a key
	// should find out at once whether that was the problem.
	llm := agent.Config{BaseURL: updated.LLM.BaseURL, APIKey: updated.LLM.APIKey, Model: updated.LLM.Model}
	s.agent.Reconfigure(llm)
	s.previews.Reconfigure(llm)

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
