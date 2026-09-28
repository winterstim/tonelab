# app

The Wails services the window calls, and the types Wails generates the
frontend's bindings from. Every domain failure comes back inside the
response; a Go error means the call itself broke.

`AgentService` holds the conversations (persisted, each with its own agent
memory), the journal of recent turns, and the pending preview plan. `Stop`
cancels between tool calls, never mid-call. `Undo` goes to the DAW directly.

`SettingsService` never sends the API key out; an empty key on save keeps
the existing one.

`app_e2e_test.go` (`-tags "llm reaper"`) drives these methods over the same
wiring the application builds, against a real DAW and model.
