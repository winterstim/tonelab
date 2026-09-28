package dawtest_test

import (
	"testing"

	"github.com/winterstim/tonelab/internal/daw/dawtest"
)

func TestFakeMeetsTheClientContract(t *testing.T) {
	dawtest.AssertClientContract(t, dawtest.NewFake())
}
