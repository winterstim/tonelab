# /osc — OSC transport

`github.com/hypebeast/go-osc`. Connection management, reconnect logic (UDP = no delivery guarantee, design for it explicitly), timeouts, send/receive, basic traffic logging.

Purely mechanical once the `DAWClient` interface (see `/daw`) is fixed — do not start until that contract exists.
