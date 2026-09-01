# /agent — tools and orchestrator

Exposes the DAW command layer as tools an LLM can call. It adds the three
things that layer deliberately lacks: input schemas, validation of what a
model actually sent, and failures expressed as codes rather than prose.

## Built

`Tools` covers the two generic tools, `get_param` and `set_param`.
Two decisions worth knowing:

- **No parameter list lives here.** Names resolve against whatever the backend
  reports (`daw.ParametersOf`), and the model is told the valid names through
  the tool description. A copy here would drift, and would be wrong for any DAW
  with a different set.
- **`Call` returns no Go error.** A model calling a tool wrongly is an ordinary
  outcome it must read and retry from, not a transport fault, so every failure
  comes back as `{Code, Message}`. The code is for the agent's
  recovery, the message is safe to show a user.

`get_param` refreshes before reading, because a DAW that only announces changes
may never have mentioned the value and the agent cannot be expected to know
that.

`Orchestrator` runs the tool-calling loop against any OpenAI-compatible
endpoint, so a cloud key and a local runtime are one code path.
Decisions worth knowing:

- **A tool failure goes back to the model, it does not end the turn.** The
  structured codes exist so the model can pick another move; swallowing them
  would waste the design.
- **The loop is bounded.** A model that keeps calling tools is commanding a
  live DAW, so an unbounded loop is not slow, it is destructive.
- **HTTP failures are their own codes** (`llm_unauthorized`, `llm_rate_limited`,
  `llm_unavailable`, `llm_unreachable`, `llm_unreadable`), because a user can
  act on each differently and none of them mean the DAW is at fault.
- **Stateless.** MVP carries no memory between commands.

## Testing

`llmtest` is a real HTTP server speaking the documented wire format, built
from openai/openai-openapi rather than from memory. It covers what a model
does wrong as well as right: tool arguments that are not valid JSON (the spec
warns the model "does not always generate valid JSON"), 401, 429, 500, and a
body that is not JSON at all.

What it cannot answer is whether a real model chooses the right tool. Two
tagged suites cover that: `-tags llm` drives a configured endpoint, and
`-tags "llm reaper"` runs the MVP criterion itself, a free-text command
changing a real REAPER.

The first live run paid for itself immediately: the model answered a schema
saying `number` with the string `"0.5"`. It had understood the command
perfectly and formatted the type wrong, and strict decoding would have failed
a correct answer. Unambiguous numeric strings are now accepted; `"loud"` and
`"-6dB"` are still refused.

## Not built

Guardrails: dry-run and confirmation on destructive commands. Worth
designing alongside the tools rather than bolting on, since a live project is
one bad call away from real damage.
