package main

import (
	"embed"

	"log"
	"path/filepath"

	"github.com/wailsapp/wails/v3/pkg/application"

	"tonelab/backend/agent"
	"tonelab/backend/app"
	"tonelab/backend/config"
	"tonelab/backend/search"
)

// Embedded so the app ships as one binary with no external asset path.

//go:embed all:frontend/dist
var assets embed.FS

func main() {

	// Settings come from a file the user edits, so no DAW name, address or
	// endpoint is compiled in.
	configPath, err := config.Path()
	if err != nil {
		log.Fatal(err)
	}
	settings, err := config.Load(configPath)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("[tonelab] %s", settings)

	// Services get the daw.Client interface rather than the transport, so
	// nothing above this line knows an address or which DAW is behind it.
	// The transport is the backend's choice: OSC over UDP, or a virtual
	// MIDI port for a DAW whose scripting has nothing else.
	dawClient, release, err := app.OpenDAW(settings)
	if err != nil {
		log.Fatal(err)
	}
	defer release()
	// A backend that subscribed to the DAW unsubscribes on the way out, so
	// the DAW is not left pushing to a port nobody reads.
	if closer, ok := dawClient.(interface{ Close() error }); ok {
		defer closer.Close()
	}
	// A backend with a script of its own to put inside the DAW installs it
	// now, and says where, since the user has to point the DAW at it once.
	if installer, ok := dawClient.(interface{ Install() (string, error) }); ok {
		if path, err := installer.Install(); err != nil {
			log.Printf("[tonelab] could not install the DAW script: %v", err)
		} else {
			log.Printf("[tonelab] DAW script at %s", path)
		}
	}

	llm := agent.Config{
		BaseURL: settings.LLM.BaseURL,
		APIKey:  settings.LLM.APIKey,
		Model:   settings.LLM.Model,
	}
	tools := agent.NewTools(dawClient)
	orchestrator := agent.NewOrchestrator(llm, tools)

	// A separate agent whose changing tools are disarmed, so a preview cannot
	// reach the project even if something above it goes wrong.
	previewTools := agent.NewPreviewTools(dawClient)
	previews := agent.NewOrchestrator(llm, previewTools)

	// Both look at the same project, so both read from one chain cache,
	// which remembers the last session's chains beside the conversations.
	chains := agent.NewChainCache(app.NewChainStore(filepath.Join(filepath.Dir(configPath), "chains.json"), settings.DAW.Backend))
	tools.ShareChains(chains)
	previewTools.ShareChains(chains)

	// Web search is optional and reads only, so both agents share it. A bad
	// search setting is logged, not fatal: the DAW still works without it.
	applySearch := func(provider search.Provider) {
		tools.EnableSearch(provider)
		previewTools.EnableSearch(provider)
	}
	if provider, err := search.New(search.Config{Provider: settings.Search.Provider, APIKey: settings.Search.APIKey, BaseURL: settings.Search.BaseURL}); err != nil {
		log.Printf("[tonelab] search disabled: %v", err)
	} else {
		applySearch(provider)
	}

	agentService := app.BuildAgentService(orchestrator, previews, dawClient,
		filepath.Join(filepath.Dir(configPath), "conversations.json"))
	settingsService := app.NewSettingsService(configPath, orchestrator, previews, applySearch)

	desktop := application.New(application.Options{
		Name:        "Tonelab",
		Description: "DAW companion with a natural-language, tool-calling agent layer",
		Services: []application.Service{
			application.NewService(agentService),
			application.NewService(settingsService),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})

	desktop.Window.NewWithOptions(application.WebviewWindowOptions{
		Title: "Tonelab",
		// Window sized to the golden ratio (1000 / 618 ≈ 1.618).
		Width:  1000,
		Height: 618,
		// Below this the composer and the bar have nowhere left to go, and a
		// window that can be dragged into uselessness is a window that will
		// be.
		MinWidth:  420,
		MinHeight: 380,
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 50,
			Backdrop:                application.MacBackdropTranslucent,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
		BackgroundColour: application.NewRGB(6, 7, 15),
		URL:              "/",
	})

	if err = desktop.Run(); err != nil {
		log.Fatal(err)
	}
}
