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

## Not built

The orchestrator: the loop that takes free text, decides which
tools to call in what order, and feeds failures back into its own decisions
rather than swallowing them. Until it exists these tools have no caller.

Guardrails: dry-run and confirmation on destructive commands. Worth
designing alongside the tools rather than bolting on, since a live project is
one bad call away from real damage.
