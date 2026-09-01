# /osc — OSC transport

`github.com/hypebeast/go-osc`. Sends OSC messages over UDP to a configured
host:port, and logs the traffic. Knows nothing about REAPER, DAWs or the
agent — address mapping belongs to `/daw`, which sits on top of this.

`Listener` is the read half: it binds a local port, unwraps bundles, and
streams messages to a caller. A DAW has to be configured to send there —
nothing about sending sets up a return path.

Its one deliberate behaviour worth knowing: the stream is buffered and
**drops** rather than blocking when a caller stops reading. DAW feedback is
mostly continuous position updates, and a message that has waited too long has
already been superseded by a newer one; a stalled receive loop would lose
everything instead of the stale part. `Dropped()` reports the count, so a
caller missing an expected message can tell "it never arrived" from "I was too
slow to take it".

Not built yet, in rough order of when they will be needed:

- **Timeouts.** `go-osc`'s client owns its socket and exposes no deadline;
  adding one means holding our own `net.Conn` here.
- **Reconnect / delivery confirmation.** UDP gives no delivery guarantee, and
  nothing above this layer needs a guarantee yet. Commands that must not be
  silently lost will need confirmation built on top — deliberately deferred
  until there is one, rather than guessed at now.

## Testing

Two levels, and the split matters — the first proves Tonelab sends what it
thinks it sends, only the second proves a DAW acted on it.

`go test ./...` needs no DAW: `osctest.Receiver` is a real UDP socket that
records what arrives, so every layer above this one can assert on exact OSC
traffic. This is what CI runs.

`go test -tags reaper ./...` drives a real REAPER and asserts on REAPER's own
feedback (`/play 1`, `/stop 1`) rather than on anything Tonelab believes.
REAPER must be running with an OSC control surface (Preferences >
Control/OSC/web > Add > OSC) that both listens on 8000 **and sends feedback
to 127.0.0.1:9000** — REAPER defaults the device port to 0, which disables
feedback and makes an automated round-trip impossible.

Known flake: the tagged suite binds REAPER's single feedback port in more
than one package, and REAPER can briefly stop sending after a socket it was
writing to closes. Running `task test:reaper` end to end may fail one package
spuriously; rerunning that package alone passes. Living with it beats papering
over it with retries that would also hide a real regression.

Note that REAPER sends its feedback as OSC *bundles*, not bare messages; a
receive path that only handles `*osc.Message` silently sees nothing.
