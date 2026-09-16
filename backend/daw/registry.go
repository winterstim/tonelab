package daw

import (
	"fmt"
	"sort"
)

// Factory keeps DAW names out of application startup: registering one here is
// all it takes to make a backend selectable.
type Factory func(Sender) Client

var backends = map[string]Factory{
	"reaper":  func(sender Sender) Client { return NewREAPER(sender) },
	"ableton": func(sender Sender) Client { return NewAbleton(sender) },
}

// New takes a name so the choice of DAW is configuration rather than a
// decision compiled into the caller.
func New(name string, sender Sender) (Client, error) {
	factory, ok := backends[name]
	if !ok {
		return nil, fmt.Errorf("daw: no backend named %q (available: %v)", name, Backends())
	}
	return factory(sender), nil
}

// Backends is what a settings screen offers, so the list cannot drift from
// what is actually registered.
func Backends() []string {
	names := make([]string, 0, len(backends))
	for name := range backends {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
