# frontend/src

Three views (chat, history, settings), shown and hidden rather than routed.
Plain TypeScript calling the generated bindings directly; everything worth
deciding lives in Go.

Monochrome, light by default, dark and system offered and stored with the
settings. Failure is weight and a heavier edge, not a colour.

- A turn belongs to the conversation that asked it; the reply is drawn only
  if that thread is still on screen.
- Stop replaces Send while a turn runs. Enter sends, Shift+Enter breaks.
- Settings are not reloaded over half-typed edits.
- The API key never comes from the backend, only goes to it.
- Undo goes straight to the DAW, not through the model.

`npm run typecheck` is part of `npm run build`; `wails3 build` regenerates
the bindings with `-clean`, so run it for itself.
