package daw

// actionUndo is REAPER's "Edit: Undo" command id. Parameter changes sent over
// OSC land in the same undo history as ones made by hand, which is what makes
// an agent's work reversible at all. Verified against a running REAPER rather
// than assumed: a volume set to 0.9 returned to its previous value.
const actionUndo = 40029

// Undo reverses the DAW's last change.
//
// It is the guardrail that matters most for an agent driving someone's
// project. Asking permission before each change would make the product
// unusable, since a user saying "make the vocals brighter" expects it done;
// being able to take it back costs nothing until it is needed.
//
// It reverses the DAW's last change, which is not necessarily ours: a user who
// moved a fader by hand since has that undone instead. Callers that care must
// check what the values became, which is why the tools layer reports them
// rather than reporting success.
func (r *REAPER) Undo() error {
	return r.osc.Send("/action", int32(actionUndo))
}
