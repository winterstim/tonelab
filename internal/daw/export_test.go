package daw

import "testing"

// Reaching the guard directly, since no public method sends an arbitrary
// address: that is the point of it, and the test still has to prove it.
func SendActionForTest(r *REAPER, id int32) error {
	return r.send("/action", id)
}

func SendForTest(r *REAPER, address string) error {
	return r.send(address)
}

func MustASCII(t *testing.T, value any) []byte {
	body, err := asciiJSON(value)
	if err != nil {
		if t == nil {
			panic(err)
		}
		t.Fatal(err)
	}
	return body
}

func KindOfTextForTest(readout string) string { return kindOfText(readout) }
