package daw

import (
	"fmt"
	"sort"
)

// Factory builds a backend on a transport. Registering one is all it takes to
// make a DAW selectable — nothing outside this file names a specific DAW, so
// adding the next backend does not touch application startup or any layer
// above it.
type Factory func(Sender) Client

var backends = map[string]Factory{
	"reaper": func(sender Sender) Client { return NewREAPER(sender) },
}

// New builds the named backend. The name is data — it comes from
// configuration, not from a decision compiled into the caller.
func New(name string, sender Sender) (Client, error) {
	factory, ok := backends[name]
	if !ok {
		return nil, fmt.Errorf("daw: no backend named %q (available: %v)", name, Backends())
	}
	return factory(sender), nil
}

// Backends lists the registered backend names, for configuration and for
// telling a user what they can choose.
func Backends() []string {
	names := make([]string, 0, len(backends))
	for name := range backends {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
