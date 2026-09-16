# daw

What can be done to a DAW, typed. `Client` is the seam a second backend
implements; REAPER is the first. Values are normalized 0.0-1.0 at this
boundary, so a DAW speaking dB or Hz converts here.

A backend describes itself through `Parameters()`; nothing above carries a
parameter list. REAPER's is static because OSC has no discovery. Plugin
parameters will come from asking the DAW at runtime: REAPER reports
`/fx/name` and `/fxparam/N/name` over the control surface, and a discoverer
may send `/device/*` only, so it cannot touch the project.

Reading is not a query. REAPER announces changes and answers nothing, so
`Refresh` provokes an announcement, `ReadParam` asks and falls back to the
last reading, and `ConfirmParam` accepts only a reading made after the call.
Silence usually means unchanged, not gone; `Probe` decides liveness.

One exit, allowlisted: addresses must match a pattern and `/action` ids are
named individually (undo only). `/action` reaches quit and close-without-
saving, which no undo takes back.

REAPER facts the code depends on, all measured: feedback arrives as bundles;
it never echoes the normalized value a device set, only `/volume/db` and the
like; toggles echo only on a transition; index 0 is the master track; an
idle REAPER sends nothing at all.

`SetTrackSendVolume` is verified live against a project with a send
(0.25 reads back as -30.0dB, the track volume curve); the tagged test skips
when track 1 has none.
