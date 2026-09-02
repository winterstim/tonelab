package daw

import (
	"errors"
	"fmt"
	"regexp"
)

// ErrForbidden means a command was refused before reaching the DAW.
var ErrForbidden = errors.New("daw: command not permitted")

// REAPER's OSC accepts /action with any command id, which reaches every menu
// item it has: quit, close without saving, delete tracks, render. Some of that
// is outside its undo history, so an agent reaching it could destroy work in a
// way nothing here could take back.
//
// The permitted ids are listed rather than the forbidden ones. There are
// thousands of actions and the list grows with REAPER; naming the few we mean
// is the only form of this that stays correct.
var allowedActions = map[int32]string{
	actionUndo: "Edit: Undo",
}

// Every address this backend may send, as patterns. A denylist would have to
// anticipate what a future edit adds, which is exactly the situation this
// exists to survive: the guard has to fail closed for anything not written
// here on purpose.
var allowedAddresses = []*regexp.Regexp{
	regexp.MustCompile(`^/(play|stop)$`),
	regexp.MustCompile(`^/track/[0-9]+/(volume|pan|mute|solo)$`),
	regexp.MustCompile(`^/track/[0-9]+/send/[0-9]+/volume$`),

	// The control surface's own view, which reading depends on and which
	// cannot alter the project.
	regexp.MustCompile(`^/device/[a-z/]+$`),

	// Arguments are checked separately against allowedActions.
	regexp.MustCompile(`^/action$`),
}

// send is the single exit from this backend, so the guard cannot be bypassed
// by a future method that forgets it. Refusing here rather than in the tools
// layer keeps the limit true regardless of what calls it: a prompt can be
// argued with, and a tool list can be extended, but this cannot be reached
// around.
func (r *REAPER) send(address string, args ...any) error {
	if err := permitted(address, args); err != nil {
		return err
	}
	return r.osc.Send(address, args...)
}

func permitted(address string, args []any) error {
	matched := false
	for _, pattern := range allowedAddresses {
		if pattern.MatchString(address) {
			matched = true
			break
		}
	}
	if !matched {
		return fmt.Errorf("%w: %s", ErrForbidden, address)
	}

	if address != "/action" {
		return nil
	}
	if len(args) != 1 {
		return fmt.Errorf("%w: an action needs exactly one command id", ErrForbidden)
	}
	id, ok := args[0].(int32)
	if !ok {
		return fmt.Errorf("%w: an action id must be a whole number", ErrForbidden)
	}
	if _, allowed := allowedActions[id]; !allowed {
		return fmt.Errorf("%w: action %d is not one this backend may run", ErrForbidden, id)
	}
	return nil
}
