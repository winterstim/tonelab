# frontend/src

The window, on React with Tailwind v4 and shadcn/ui components owned here
(`components/ui`). Three views (chat, history, settings) kept mounted and
shown one at a time; everything worth deciding lives in Go, reached through
`services.ts`, the one door to the Wails bindings.

Light by default, dark on the site's palette. Failure is weight and the
colour of text, never a hue on the chrome; green for what the DAW
confirmed, amber for an offer not yet taken.

- Tests swap `@/services` for `test/fake.ts`, a stand-in shaped like the
  backend: asynchronous, null where a Go slice is empty, refusing what the
  backend refuses.
- `markdown.ts` lays out the model's markdown from escaped text.
- `npm run ci` is typecheck, lint and tests; `wails3 build` regenerates
  the bindings with `-clean`, so run it for itself.
