# frontend/src

One screen: a command field, its answer, and whether the DAW is connected.
No message history.

Everything worth deciding lives in Go. This layer calls the generated bindings
directly, because an abstraction over a typed, generated client would only add
a place for the two to drift.

Notes on what it does that is not obvious:

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
