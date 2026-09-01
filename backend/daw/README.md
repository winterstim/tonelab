# /daw — DAW command layer

Typed Go API for what can be done to a DAW, mapping domain commands onto one
DAW's OSC addresses. Knows nothing about the agent: this is deliberately the
layer you can drive by hand and test with no LLM anywhere near it.

REAPER is the only backend. `Client` is the interface a second one
would implement.

## Scope

Track-level parameters only — volume, pan, mute, solo, send volume. That is
the whole of MVP scope; FX parameters need a name-to-index bridge that does
not exist yet.

Values crossing this API are always normalized 0.0–1.0, never dB or
Hz. REAPER's OSC is normalized already, so `REAPER` passes them straight
through; a backend whose DAW speaks real units would convert here, which is
the point of putting the boundary at this layer.

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
