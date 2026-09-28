package daw

// actionUndo is REAPER's "Edit: Undo" command id. Parameter changes sent over
// OSC land in the same undo history as ones made by hand, which is what makes
// an agent's work reversible at all. Verified against a running REAPER rather
// than assumed: a volume set to 0.9 returned to its previous value.
const actionUndo = 40029

// Undo reverses the DAW's last change, which is not necessarily ours: a user
// who moved a fader by hand since has that undone instead, so callers report
// what the values became rather than success. Reversibility is the guardrail
// chosen over per-change permission, which would make the product unusable.
func (r *REAPER) Undo() error {
	return r.send("/action", int32(actionUndo))
}
