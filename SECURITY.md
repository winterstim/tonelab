# Security

Tonelab holds API keys for your model endpoint and, optionally, a search
provider. They are stored in your user config file with owner-only
permissions and are never sent to the window or written to logs.

The agent can only reach the DAW through an allowlist inside each backend:
mixer parameters, plugin parameters, transport and undo. Nothing that closes
a project, deletes tracks or renders is reachable, whatever the model is
told.

To report a vulnerability, open a private security advisory on this
repository or email timohasmile@gmail.com. Please do not open a public
issue for security problems.
