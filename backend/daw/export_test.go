package daw

// Reaching the guard directly, since no public method sends an arbitrary
// address: that is the point of it, and the test still has to prove it.
func SendActionForTest(r *REAPER, id int32) error {
	return r.send("/action", id)
}

func SendForTest(r *REAPER, address string) error {
	return r.send(address)
}
