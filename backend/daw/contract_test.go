package daw_test

import (
	"testing"

	"tonelab/backend/daw/dawtest"
)

// The real backend defines the contract; the fakes in other packages are held
// to this same suite so they cannot drift into being kinder than it.
func TestREAPERMeetsTheClientContract(t *testing.T) {
	reaper, _ := newREAPER(t)

	dawtest.AssertClientContract(t, reaper)
}
