package app

import (
	"log"
	"path/filepath"

	"tonelab/backend/agent"
	"tonelab/backend/config"
	"tonelab/backend/daw"
	"tonelab/backend/search"
)

// Runtime is everything above the DAW that an interface talks to, built
// once from the settings. The desktop window and the command line both
// stand on it, so neither has wiring of its own to drift.
type Runtime struct {
	Agent    *AgentService
	Settings *SettingsService
	Hosted   *HostedService
	DAW      daw.Client
	release  func()
}

// Assemble opens the DAW, builds the live and preview agents over one
// chain cache and one search provider, and the services above them.
// openURL is how the interface opens a browser for sign-in; nil leaves
// the person the address to open themselves.
func Assemble(settings config.Config, configPath string, openURL func(string) error) (*Runtime, error) {
	client, release, err := OpenDAW(settings)
	if err != nil {
		return nil, err
	}
	// A backend with a script of its own to put inside the DAW installs it
	// now, and says where, since the user has to point the DAW at it once.
	if installer, ok := client.(interface{ Install() (string, error) }); ok {
		if path, err := installer.Install(); err != nil {
			log.Printf("[tonelab] could not install the DAW script: %v", err)
		} else {
			log.Printf("[tonelab] DAW script at %s", path)
		}
	}

	base, key, model, device := settings.AgentConfig()
	llm := agent.Config{BaseURL: base, APIKey: key, Model: model, DeviceID: device}
	tools := agent.NewTools(client)
	live := agent.NewOrchestrator(llm, tools)
	// A separate agent whose changing tools are disarmed, so a preview
	// cannot reach the project even if something above it goes wrong.
	previewTools := agent.NewPreviewTools(client)
	previews := agent.NewOrchestrator(llm, previewTools)

	// Both look at the same project, so both read from one chain cache,
	// which remembers the last session's chains beside the conversations.
	dir := filepath.Dir(configPath)
	chains := agent.NewChainCache(NewChainStore(filepath.Join(dir, "chains.json"), settings.DAW.Backend))
	tools.ShareChains(chains)
	previewTools.ShareChains(chains)

	// Web search is optional and reads only, so both agents share it. A
	// bad search setting is logged, not fatal: the DAW works without it.
	applySearch := func(provider search.Provider) {
		tools.EnableSearch(provider)
		previewTools.EnableSearch(provider)
	}
	if provider, err := search.New(settings.SearchConfig()); err != nil {
		log.Printf("[tonelab] search disabled: %v", err)
	} else {
		applySearch(provider)
	}

	return &Runtime{
		Agent:    BuildAgentService(live, previews, client, filepath.Join(dir, "conversations.json")),
		Settings: NewSettingsService(configPath, live, previews, applySearch),
		Hosted:   NewHostedService(configPath, live, previews, applySearch, openURL),
		DAW:      client,
		release:  release,
	}, nil
}

// Close lets the DAW backend unsubscribe and releases the transport.
func (r *Runtime) Close() {
	if closer, ok := r.DAW.(interface{ Close() error }); ok {
		closer.Close()
	}
	r.release()
}
