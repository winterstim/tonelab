//go:build !(darwin && cgo)

package midi

// Open reports that virtual ports are not built for this platform. Windows
// has no virtual MIDI API without a driver and Linux needs ALSA sequencer
// bindings; both are real work, not a stub, and until done the DAWs that
// need a port are refused at startup with this error.
func Open(name string) (*Port, error) {
	return nil, ErrUnsupported
}

type Port struct{}

func (p *Port) Send(message []byte) error { return ErrUnsupported }
func (p *Port) Messages() <-chan []byte   { return nil }
func (p *Port) Close() error              { return nil }
