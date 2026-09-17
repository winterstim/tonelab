# daw

What can be done to a DAW, typed, behind `Client`. Two backends: REAPER
over its OSC control surface, and Ableton Live over the AbletonOSC remote
script (ports 11000 in, 11001 back). Values are normalized 0.0-1.0 and
tracks count from one at this boundary; each backend folds its own shape
away (Live counts from zero, pans -1..1, and reports device parameters in
their own units with ranges on request).

REAPER announces and never answers; Live answers, and announces only what
it was asked to listen to. Measured on Live 12.4: mixer volume set over OSC
enters its undo history, mute does not, so undo there is partial and
reported as the DAW's own. Reading, liveness and effect discovery are
therefore built differently in each, and identically above.

Live listener facts, measured: a query costs 64 to 100 ms; `start_listen`
pushes the current value at once and every change about 100 ms later, on
the query's reply address, and nothing for a set that changed nothing.
The backend subscribes the mix properties of each track it touches, waits
for that first push before anything else so it cannot count as the answer
to a set, and reads from what Live pushed after that (2.5 µs). A probe
that succeeds subscribes again, since a probe runs when Live has gone
quiet, which is when it may have restarted and dropped the listeners.

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
