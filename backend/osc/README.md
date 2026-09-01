# /osc — OSC transport

`github.com/hypebeast/go-osc`. Sends OSC messages over UDP to a configured
host:port, and logs the traffic. Knows nothing about REAPER, DAWs or the
agent — address mapping belongs to `/daw`, which sits on top of this.

Not built yet, in rough order of when they will be needed:

- **Receive.** Send-only today. Reading parameter values back (`get_param`)
  needs an inbound path, so this is the next thing this package grows.
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

Note that REAPER sends its feedback as OSC *bundles*, not bare messages; a
receive path that only handles `*osc.Message` silently sees nothing.
