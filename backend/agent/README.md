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

`TONELAB_CONFIG` points the live suites at a config file, so the same
scenarios can be run against a local runtime and a hosted endpoint in turn.
That comparison is the only real check on the claim that the two are
one code path: a suite only ever run against what it was developed on proves
the opposite of portability.

What it cannot answer is whether a real model chooses the right tool. Two
tagged suites cover that: `-tags llm` drives a configured endpoint, and
`-tags "llm reaper"` runs the MVP criterion itself, a free-text command
changing a real REAPER.

## One structure, different models

The wire protocol is genuinely uniform: the same code drives a local runtime
and a hosted endpoint with no branch anywhere for the provider. That much is
verified rather than assumed, by running the same live suites against both.

What is not uniform is model behaviour, and it cannot be made so from here.
Measured on one prompt, six samples each: `qwen/qwen3.6-27b` produced a usable
tool call 5 times in 6, failing by emitting its call in an XML-ish form where
every value is text; `openai/gpt-oss-20b` produced 6 in 6. Schema shape made no
difference outside that variance.

Measured against a real endpoint, five runs each: with two retries the flaky
model completed 2 in 5; with four it completed 2 in 5 again, but the failures
moved from schema rejections to rate limits, because each retry spends tokens
against a free tier's per-minute quota. Tuning further would be fitting the
bound to noise. The bound is four, and **model choice remains the lever that
actually decides this**: `openai/gpt-oss-20b` completed 8 scenarios in 8.

So the uniformity that can be built is in the response to failure, and that is
what the loop does:

- **A tool refuses** the call, and the code goes back to the model to act on.
- **The endpoint refuses** the call, validating our schema before it ever
  reaches us, and the reason goes back to the model the same way. Without this
  the turn would be lost on an endpoint that validates, while succeeding on one
  that does not.
- **The endpoint imposes a quota**, and a wait it says is seconds long is
  waited out once rather than reported. Endpoints differ in whether they limit
  at all, and the user should not have to know which they are using.

Each of those is a difference between providers that would otherwise be visible
to the user as "it works with one and not the other".

Three more things help every model rather than any one of them:

- **Temperature 0.** Choosing a tool and filling a schema is not writing, and
  endpoints default to sampling that produces the malformed calls we then work
  around. Five paced runs of the flaky model afterwards: four completed and the
  one failure was a quota, with no schema rejection seen, against one to three
  rejections in five before. A small sample, so this is an indication rather
  than a result.
- **Refusals that name the alternatives.** A model that wrote "muted" for
  "mute" had nothing to correct against; it is now told what the DAW does have.
  The list is already at hand, and a refusal that carries it turns a lost turn
  into a corrected one.
- **A set reports what the DAW says, not what we sent.** The command leaves
  over a socket that guarantees nothing, and the model answers a user on the
  strength of what the tool returns, so "sent" and "done" must not be the same
  word. Where a parameter cannot be read back the result says so plainly, which
  is neither a failure nor a confirmation.

## How a model may write a value

`TestValueRepresentationPolicy` is the policy, as a table: every accepted and
refused spelling in one place. Two of its rows were production failures found
by running a real model, which is why it is written down rather than reasoned
about. A new case belongs in that table before it reaches a user.

The rule behind the table: accept anything unambiguous (`"0.5"`, `"true"`,
`1`, `"on"`, `"50%"`), refuse anything needing a guess (`"-6dB"`, `"loud"`,
`0.5` for a switch). Units are the clearest refusal: converting decibels needs
the DAW's own curve, which this layer does not have.

Belt as well as braces. The schema itself was changed on measurement: given
`oneOf`, a live model answered `"true"` as a string and misnamed the
parameter; given `{"type": ["number", "boolean"]}` it answered correctly.
`strict` made no difference, so the union was the cause rather than the
absence of constraint.

The first live run paid for itself immediately: the model answered a schema
saying `number` with the string `"0.5"`. It had understood the command
perfectly and formatted the type wrong, and strict decoding would have failed
a correct answer. Unambiguous numeric strings are now accepted; `"loud"` and
`"-6dB"` are still refused.

## Not built

Guardrails: dry-run and confirmation on destructive commands. Worth
designing alongside the tools rather than bolting on, since a live project is
one bad call away from real damage.
