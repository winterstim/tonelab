# frontend/src

Three views: a chat, the agent's history, and settings. Switched by showing
and hiding sections rather than by a router, which is enough for three and is
the thing to revisit if there is ever a fourth.

Everything worth deciding lives in Go. This layer calls the generated bindings
directly, because an abstraction over a typed, generated client would only add
a place for the two to drift.

Light by default, because the app sits beside a DAW that is already dark and
dense, and a bright panel reads as a different kind of surface rather than more
of the same. Dark and system are offered and the choice is stored with the
other settings, since a preference that does not survive a restart is not one.

Colour is reserved for meaning: failure, and whether the DAW answered.

Notes on what it does that is not obvious:

- **The history reads as sentences, with the raw calls behind a disclosure.**
  Someone reading their history wants to know what happened; someone debugging
  wants the call. Showing the second to everyone is what made the first
  version unreadable.
- **Stop replaces Send while a turn runs**, so the button under the cursor is
  always the one that applies.
- **Enter sends and Shift+Enter breaks a line**, which is what a text box in a
  chat is expected to do.
- **Icons come from one inline sprite**, referenced rather than repeated, so
  adding one costs a symbol and the set keeps a single stroke weight. Nothing
  is fetched, which also keeps it working under a strict content policy.
- **The two toggles are switches, not checkboxes.** Both are a mode the app is
  in rather than an item being ticked, and a switch says which is on from
  across a desk.
- **The API key is never received from the backend**, only replaced. A key
  that never crosses cannot be read off a screen or out of a screenshot.

- **Connection is polled, not pushed.** The backend judges what counts as
  connected (it infers it from DAW feedback); this only decides how stale the
  display may be.
- **Send is disabled while a command runs.** A second command sent mid-flight
  would reach a DAW whose state the first has already changed.
- **A failed command must not look like a completed one**, hence the separate
  tone on the answer panel rather than plain text.
- **`catch` means the call itself broke.** Domain failures arrive inside the
  response, so reaching `catch` is a different problem and says so.

`npm run typecheck` is part of `npm run build`. The template shipped with
TypeScript 4.9 pinned against a tsconfig needing 5.x, so nothing was ever
type-checked until that was corrected.
