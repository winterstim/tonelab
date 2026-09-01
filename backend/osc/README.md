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
