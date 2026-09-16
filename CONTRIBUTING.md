# Contributing

## Setup

Go 1.25+, Node 22+ and the [Wails v3 CLI](https://v3.wails.io/getting-started/installation/). `wails3 dev` runs the app with hot reload; the config file is created on first launch at the path printed in the log. Build and test commands are listed in the README.

## Tests

`go test ./...` needs nothing installed and is what CI runs on all three platforms. The tagged suites (`reaper`, `ableton`, `llm`) run against a real DAW or model and are expected for any change that touches one. They bind the DAW's single feedback port, so run them with `-p 1`.

## Rules the code is held to

- Nothing above `backend/daw` names a DAW. Backends describe themselves through `Parameters()` and `FXChain()`.
- Code grows with the number of DAWs, never with the number of plugins or parameters. No plugin name appears in the source.
- A fake of an external system is held to the same contract as the real one (`dawtest.AssertClientContract`) and is asynchronous where the real one is.
- Outgoing OSC goes through the backend's allowlist. Adding an address means adding a pattern and a test.
- API keys never reach the frontend.
- Comments say why, not what. Short, English.

## Pull requests

Branch from `main`, one topic per PR, small commits that each build. Fill in the template. CI must be green on all three platforms before merge.
