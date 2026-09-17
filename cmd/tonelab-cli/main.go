// Command tonelab-cli is Tonelab in a terminal: the same backend the
// desktop window stands on, with a prompt instead of a window. One-shot
// for scripts, interactive otherwise.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"tonelab/backend/app"
	"tonelab/backend/config"
)

func main() {
	preview := flag.Bool("preview", false, "show the plan instead of applying a one-shot command")
	asJSON := flag.Bool("json", false, "print the one-shot result as JSON")
	quiet := flag.Bool("quiet", false, "suppress the backend log")
	flag.Usage = usage
	flag.Parse()

	if *quiet || !*asJSON && flag.NArg() == 0 {
		// The backend logs OSC and tool traffic to stderr, which would
		// scribble over an interactive screen.
		log.SetOutput(io.Discard)
	}

	configPath, err := config.Path()
	if err != nil {
		fail(err)
	}
	settings, err := config.Load(configPath)
	if err != nil {
		fail(err)
	}
	runtime, err := app.Assemble(settings, configPath)
	if err != nil {
		fail(err)
	}
	defer runtime.Close()

	switch {
	case flag.NArg() == 0:
		program := tea.NewProgram(newModel(runtime, settings), tea.WithAltScreen())
		if _, err := program.Run(); err != nil {
			fail(err)
		}
	case flag.Arg(0) == "status":
		status, _ := runtime.Agent.GetDAWStatus()
		emit(*asJSON, status, func() string { return renderStatus(status) })
	case flag.Arg(0) == "undo":
		response, _ := runtime.Agent.Undo()
		emit(*asJSON, response, func() string { return renderResponse(response) })
	default:
		text := strings.Join(flag.Args(), " ")
		var response app.AgentResponse
		if *preview {
			response, _ = runtime.Agent.PreviewCommand(text)
		} else {
			response, _ = runtime.Agent.SendCommand(text)
		}
		emit(*asJSON, response, func() string { return renderResponse(response) })
		if response.Error != nil {
			os.Exit(1)
		}
	}
}

func emit(asJSON bool, value any, render func() string) {
	if asJSON {
		body, _ := json.MarshalIndent(value, "", "  ")
		fmt.Println(string(body))
		return
	}
	fmt.Println(render())
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, theme.Error.Render(err.Error()))
	os.Exit(1)
}

func usage() {
	fmt.Fprintln(os.Stderr, gradient("tonelab-cli"))
	fmt.Fprintln(os.Stderr, `
  tonelab-cli                         talk to the DAW interactively
  tonelab-cli <command in words>      run one command and exit
  tonelab-cli --preview <command>     show what it would do, change nothing
  tonelab-cli status                  is the DAW answering
  tonelab-cli undo                    ask the DAW to take back its last change

  --json    machine-readable output for one-shot commands
  --quiet   hide the backend log

Settings come from the same config file the desktop app uses; TONELAB_CONFIG
points at another one.`)
}
