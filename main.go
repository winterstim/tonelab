package main

import (
	"embed"

	"log"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"tonelab/backend/daw"
	"tonelab/backend/osc"
)

// Which DAW backend to drive and where it listens. This is the one place in
// the application that names a DAW: everything above backend/daw works
// through daw.Client and does not know which backend is behind it. Fixed here
// until the app persists connection settings, at which point these become
// config values rather than constants — daw.Backends() is what a settings
// screen would offer.
//
// The port must match the DAW's own OSC listen port (in REAPER: Preferences >
// Control/OSC/web -> Add -> OSC, "Local listen port").
const (
	dawBackend = "reaper"
	dawOSCHost = "127.0.0.1"
	dawOSCPort = 8000
)

// Wails uses Go's `embed` package to embed the frontend files into the binary.
// Any files in the frontend/dist folder will be embedded into the binary and
// made available to the frontend.
// See https://pkg.go.dev/embed for more information.

//go:embed all:frontend/dist
var assets embed.FS

func init() {
	// Register a custom event whose associated data type is string.
	// This is not required, but the binding generator will pick up registered events
	// and provide a strongly typed JS/TS API for them.
	application.RegisterEvent[string]("time")
}

// main function serves as the application's entry point. It initializes the application, creates a window,
// and starts a goroutine that emits a time-based event every second. It subsequently runs the application and
// logs any error that might occur.
func main() {

	// One transport, one DAW client, shared by every service that talks to
	// the DAW. Services get the daw.Client interface, not the transport, so
	// nothing above this line knows an OSC address or which DAW is connected.
	dawClient, err := daw.New(dawBackend, osc.NewTransport(dawOSCHost, dawOSCPort))
	if err != nil {
		log.Fatal(err)
	}

	// Create a new Wails application by providing the necessary options.
	// Variables 'Name' and 'Description' are for application metadata.
	// 'Assets' configures the asset server with the 'FS' variable pointing to the frontend files.
	// 'Bind' is a list of Go struct instances. The frontend has access to the methods of these instances.
	// 'Mac' options tailor the application when running an macOS.
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

	// Create a new window with the necessary options.
	// 'Title' is the title of the window.
	// 'Mac' options tailor the window when running on macOS.
	// 'BackgroundColour' is the background colour of the window.
	// 'URL' is the URL that will be loaded into the webview.
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

	// Create a goroutine that emits an event containing the current time every second.
	// The frontend can listen to this event and update the UI accordingly.
	go func() {
		for {
			now := time.Now().Format(time.RFC1123)
			app.Event.Emit("time", now)
			time.Sleep(time.Second)
		}
	}()

	// Run the application. This blocks until the application has been exited.
	err = app.Run()

	// If an error occurred while running the application, log it and exit.
	if err != nil {
		log.Fatal(err)
	}
}
