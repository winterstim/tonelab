package app

import (
	"tonelab/backend/config"
	"tonelab/backend/daw"
	"tonelab/backend/midi"
	"tonelab/backend/osc"

	goosc "github.com/hypebeast/go-osc/osc"
)

// OpenDAW builds the configured backend over the transport it asks for:
// OSC over UDP, or a virtual MIDI port for a DAW whose scripting has
// nothing else. Nothing here names a DAW. The returned func releases the
// transport.
func OpenDAW(settings config.Config) (daw.Client, func(), error) {
	transport, err := daw.TransportOf(settings.DAW.Backend)
	if err != nil {
		return nil, nil, err
	}
	switch transport {
	case daw.MIDI:
		port, err := midi.Open("Tonelab")
		if err != nil {
			return nil, nil, err
		}
		client, err := daw.NewMIDI(settings.DAW.Backend, port)
		if err != nil {
			port.Close()
			return nil, nil, err
		}
		return client, func() { port.Close() }, nil
	default:
		client, err := daw.New(settings.DAW.Backend, osc.NewTransport(settings.DAW.Host, settings.DAW.Port))
		if err != nil {
			return nil, nil, err
		}
		// The read path only exists while something is listening, so the
		// listener is opened here and handed to the backend.
		listener, err := osc.Listen(settings.DAW.Host, settings.DAW.FeedbackPort)
		if err != nil {
			return nil, nil, err
		}
		if observer, ok := client.(interface {
			Observe(<-chan *goosc.Message)
		}); ok {
			observer.Observe(listener.Messages())
		}
		return client, func() { listener.Close() }, nil
	}
}
