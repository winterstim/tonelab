package main

import (
	"context"
	"embed"
	"log"
	"path/filepath"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/winterstim/tonelab/internal/app"
	"github.com/winterstim/tonelab/internal/config"
	"github.com/winterstim/tonelab/internal/mcplocal"
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
	var desktop *application.App
	// An MCP server a host started may hold the DAW; it lets go for the
	// window, which then holds it and serves that host from here.
	dir := filepath.Dir(configPath)
	if err := mcplocal.Release(context.Background(), dir); err != nil {
		log.Printf("[tonelab] %v", err)
	}
	runtime, err := app.Assemble(settings, configPath, func(url string) error { return desktop.Browser.OpenURL(url) })
	if err != nil {
		log.Fatal(err)
	}
	defer runtime.Close()
	if stop, err := mcplocal.Hold(runtime.MCPServer(), dir, nil); err != nil {
		log.Printf("[tonelab] MCP hosts cannot reach the DAW through the window: %v", err)
	} else {
		defer stop()
	}
	agentService, settingsService := runtime.Agent, runtime.Settings

	desktop = application.New(application.Options{
		Name:        "Tonelab",
		Description: "DAW companion with a natural-language, tool-calling agent layer",
		Services: []application.Service{
			application.NewService(agentService),
			application.NewService(settingsService),
			application.NewService(runtime.Hosted),
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
