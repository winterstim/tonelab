package main

import (
	"embed"

	"log"

	goosc "github.com/hypebeast/go-osc/osc"
	"github.com/wailsapp/wails/v3/pkg/application"

	"tonelab/backend/agent"
	"tonelab/backend/config"
	"tonelab/backend/daw"
	"tonelab/backend/osc"
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
	// nothing above this line knows an OSC address or which DAW is behind it.
	dawClient, err := daw.New(settings.DAW.Backend, osc.NewTransport(settings.DAW.Host, settings.DAW.Port))
	if err != nil {
		log.Fatal(err)
	}

	// The read path only exists while something is listening, so the listener
	// is started here and handed to the backend rather than opened on demand.
	listener, err := osc.Listen(settings.DAW.Host, settings.DAW.FeedbackPort)
	if err != nil {
		log.Fatal(err)
	}
	defer listener.Close()

	// Observing is optional in the interface, so a backend that cannot read
	// its DAW simply never gets asked to.
	if observer, ok := dawClient.(interface {
		Observe(<-chan *goosc.Message)
	}); ok {
		observer.Observe(listener.Messages())
	}

	orchestrator := agent.NewOrchestrator(agent.Config{
		BaseURL: settings.LLM.BaseURL,
		APIKey:  settings.LLM.APIKey,
		Model:   settings.LLM.Model,
	}, agent.NewTools(dawClient))

	observer, _ := dawClient.(liveness)
	agentService := NewAgentService(orchestratorBrain{orchestrator: orchestrator}, observer)

	app := application.New(application.Options{
		Name:        "Tonelab",
		Description: "DAW companion with a natural-language, tool-calling agent layer",
		Services: []application.Service{
			application.NewService(NewTransportService(dawClient)),
			application.NewService(agentService),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title: "Tonelab",
		// Window sized to the golden ratio (1000 / 618 ≈ 1.618).
		Width:  1000,
		Height: 618,
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 50,
			Backdrop:                application.MacBackdropTranslucent,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
		BackgroundColour: application.NewRGB(6, 7, 15),
		URL:              "/",
	})

	if err = app.Run(); err != nil {
		log.Fatal(err)
	}
}
