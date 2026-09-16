# Tonelab

Natural-language control for your DAW. Type "turn the vocals down a bit" and
the agent resolves it to a real parameter change, sends it to the DAW, and
reports back what the DAW says the value is now.

Desktop app: Go, Wails v3, plain TypeScript. Backends: REAPER over its OSC
surface, Ableton Live over the AbletonOSC remote script. Any OpenAI-compatible endpoint works for the model, hosted or local.

## Running

Requires Go 1.24+, Node, and the Wails v3 CLI (`wails3`).

```
wails3 dev
wails3 build                 # this machine
wails3 task build:windows    # cross-compiled from any host, no cgo on Windows
wails3 task build:linux      # in Docker, needs GTK3 and WebKit2GTK 4.1 headers
```

Windows needs the WebView2 runtime, present on Windows 11 and on any
updated Windows 10; the app offers Microsoft's bootstrapper when it is
missing. Linux links against libwebkit2gtk-4.1.

First start writes `config.json` to the user config directory (on macOS,
`~/Library/Application Support/tonelab/`) with the DAW ports and the model
endpoint to fill in. The DAW must send OSC feedback to the listener port;
REAPER defaults that to off.

## Tests

```
go test ./...                          # no DAW or model needed
go test -tags reaper -count=1 -p 1 ./...   # against a running REAPER
go test -tags ableton -count=1 ./backend/daw   # against a running Live with AbletonOSC
go test -tags llm ./backend/agent/...      # against the configured model
go test -tags "llm reaper" -p 1 ./backend/app   # the whole thing, as the window uses it
```

`TONELAB_CONFIG` points the tagged suites at another config file.

## Layout

```
backend/osc      OSC transport and listener, DAW-neutral
backend/daw      DAW command layer: Client interface, REAPER backend, allowlist
backend/agent    tools an LLM can call, and the loop that calls them
backend/config   the user's settings file
backend/app      Wails services the window calls
frontend/src     the window
```

## Planned

- FX and plugin parameters, discovered from the DAW at runtime rather than
  listed in code, with a search tool so the model never sees the whole set.
- Further DAW backends behind the same `Client` interface; with two in
  place, the REAPER table becomes data.
