package main

import (
	"embed"

	"log"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"tonelab/backend/daw"
	"tonelab/backend/osc"
)

// The one place in the application that names a DAW. Constants
// until settings are persisted, at which point they become config values and
// daw.Backends() is what a settings screen offers.
//
// The port must match the DAW's own OSC listen port (in REAPER: Preferences >
// Control/OSC/web -> Add -> OSC, "Local listen port").
const (
	dawBackend = "reaper"
	dawOSCHost = "127.0.0.1"
	dawOSCPort = 8000
)

// Embedded so the app ships as one binary with no external asset path.

//go:embed all:frontend/dist
var assets embed.FS

func init() {
	// Registered so the binding generator emits a typed TS API for it.
	application.RegisterEvent[string]("time")
}

func main() {

	// Services get the daw.Client interface rather than the transport, so
	// nothing above this line knows an OSC address or which DAW is behind
	// it.
	dawClient, err := daw.New(dawBackend, osc.NewTransport(dawOSCHost, dawOSCPort))
	if err != nil {
		log.Fatal(err)
	}

	app := application.New(application.Options{
		Name:        "Tonelab",
		Description: "DAW companion with a natural-language, tool-calling agent layer",
		Services: []application.Service{
			application.NewService(&GreetService{}),
			application.NewService(NewTransportService(dawClient)),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title: "Window 1",
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

	// Template leftover: proves the event path to the frontend still works.
	go func() {
		for {
			now := time.Now().Format(time.RFC1123)
			app.Event.Emit("time", now)
			time.Sleep(time.Second)
		}
	}()

	if err = app.Run(); err != nil {
		log.Fatal(err)
	}
}
