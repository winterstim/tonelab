# /daw — DAW command layer

Typed Go API for what can be done to a DAW, mapping domain commands onto one
DAW's OSC addresses. Knows nothing about the agent: this is deliberately the
layer you can drive by hand and test with no LLM anywhere near it.

REAPER is the only backend. `Client` is the interface a second one
would implement.

## How a backend describes itself

Nothing above this package carries a list of parameters. A backend reports its
own via `Parameters()`, and `SetParam(track, name, value)` resolves a name to
a command inside the backend — because what a parameter is called, and which
command sets it, is DAW-specific knowledge.

This is the seam that lets backends differ in **capability**, not just in
addresses. OSC has no discovery, so REAPER returns what its pattern config
supports, statically. A DAW whose surface can enumerate itself (AbletonOSC
exposes parameter names, min and max) would build the same list by asking the
DAW at runtime. Callers cannot tell the difference, which is the point:
`Parameter.Readable` exists for the same reason — a surface may accept a
parameter it never reports back, and a caller has to know that before
promising a user it can answer "what is it now?".

## Where this is heading

`reaper.go` holds two separable things: knowledge (which addresses, which
parameters, which value shapes) and mechanism (validate, dispatch, send). The
mechanism is the same for any OSC-controlled DAW; only the knowledge differs.

So the intended evolution is to turn the knowledge into data — a descriptor
per DAW mapping name to address pattern, kind and readability — leaving one
generic OSC backend that executes a descriptor. A new DAW then costs a data
file, not Go code, and `reaper.go` itself goes away. Further along, REAPER's
descriptor need not even be ours: REAPER ships `Default.ReaperOSC`, its own
name-to-address pattern config, which could be parsed at runtime.

**Do not do this until a second backend exists.** A descriptor format designed
against one DAW will encode REAPER's shape in a different syntax and prove
wrong for the DAW it was meant to generalize to. The second backend is what
makes the real variation visible. Waiting costs nothing: `daw.Client` means
the change cannot reach any layer above this package.

What no descriptor removes: a DAW that does not speak OSC needs different
mechanism, not a different table; and behavioural quirks (REAPER reports
transitions only, and never echoes back a value the device itself set) are
receive-path logic rather than data.

## Scope

Track-level parameters only — volume, pan, mute, solo, send volume. That is
the whole of MVP scope; FX parameters need a name-to-index bridge that does
not exist yet.

Values crossing this API are always normalized 0.0–1.0, never dB or
Hz. REAPER's OSC is normalized already, so `REAPER` passes them straight
through; a backend whose DAW speaks real units would convert here, which is
the point of putting the boundary at this layer.

## Verification gap

`SetTrackSendVolume` is the one command covered only against the fake
receiver, never against a running REAPER — every other command is verified
both ways. A send has to exist before its volume can be set, and REAPER
exposes no OSC action that creates one, so the round-trip test would need a
prepared project rather than the track it makes for itself. Its address
mapping is therefore inferred from REAPER's pattern config
(`n/track/@/send/@/volume`) and not confirmed by REAPER's own feedback the way
volume, pan, mute and solo are. Worth confirming by hand the first time a send
is actually used.

## What REAPER's feedback actually does

Learned from the round-trip tests, and load-bearing for the receive path that
`get_param` will need:

- Feedback arrives as OSC **bundles**, not bare messages.
- REAPER **never echoes back the normalized value a device just set** — no
  `/track/N/volume`, no `/track/N/pan`. It sends the derived readouts instead:
  `/track/N/volume/db`, `/track/N/volume/str`, `/track/N/pan/str`.
- Toggles (`mute`, `solo`) do echo directly, but only on a **transition** —
  setting mute on an already-muted track reports nothing.
- Track and send indices are 1-based, matching REAPER's own UI.
