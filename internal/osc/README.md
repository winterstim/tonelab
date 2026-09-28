# osc

OSC over UDP, with no DAW knowledge. `Transport` sends; `Listener` binds a
port, unwraps bundles, and streams messages, dropping rather than blocking
when a reader stalls (stale feedback is already superseded). `Dropped()`
tells "never arrived" from "too slow to take it".

`osctest.Receiver` is a real UDP socket for asserting exact traffic without a
DAW. The `reaper` tag runs against a live REAPER; every such test binds the
one feedback port, hence `-p 1`.

Not built: write timeouts, delivery confirmation. Nothing above needs them.
