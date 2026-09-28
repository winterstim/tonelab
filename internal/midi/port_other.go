//go:build !(darwin && cgo) && !windows && !(linux && cgo)

package midi

// Open reports that no port is built for this platform, such as a
// CGO-free build on macOS or Linux, where the system MIDI libraries cannot
// be reached. The DAWs that need a port are refused at startup with this
// error rather than pretending.
func Open(name string) (*Port, error) {
	return nil, ErrUnsupported
}

type Port struct{}

func (p *Port) Send(message []byte) error { return ErrUnsupported }
func (p *Port) Messages() <-chan []byte   { return nil }
func (p *Port) Close() error              { return nil }
