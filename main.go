package main

import (
	"embed"

	"log"

	"github.com/wailsapp/wails/v3/pkg/application"

	"tonelab/backend/app"
	"tonelab/backend/config"
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

	// Everything above the DAW is assembled once, the same way the command
	// line assembles it, so the window is only a view on it.
	runtime, err := app.Assemble(settings, configPath)
	if err != nil {
		log.Fatal(err)
	}
	defer runtime.Close()
	agentService, settingsService := runtime.Agent, runtime.Settings

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
