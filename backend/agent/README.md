# agent

The DAW layer as tools a model can call, and the loop that calls them.

`Tools` holds no parameter list; names resolve against the backend. Every
failure is `{Code, Message}`, never a Go error: a wrong call is an outcome the
model reads and retries from. Refusals name what the DAW does have.
`set_param` reads the value back and reports the DAW's account, or says
plainly that it could not.

Values are coerced by the parameter's kind, from what models actually send
(`"0.5"`, `"true"`, `"50%"`), and refused where a guess would be needed
(`"-6dB"`, `"loud"`). `TestValueRepresentationPolicy` is the table.

`Orchestrator` drives any OpenAI-compatible endpoint at temperature 0, bounded
in steps and in schema retries, waiting once for a quota that refills in
seconds. Memory is per conversation and bounded; failed turns are not
remembered. Preview runs a separate orchestrator with the changing tools
disarmed and keeps the plan executable, so `Apply` replays what the user saw
rather than asking the model again.

`llmtest` is a fake endpoint built from the published OpenAPI spec. `-tags llm`
runs against a configured model; model choice dominates every knob here.
