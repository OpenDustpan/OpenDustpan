package p

import (
	"sync/atomic"
	"testing"
)

var _ atomic.Int64

func TestA(t *testing.T) {
	opened := false
	t.Cleanup(func() {
		if opened {
			t.Error("opened")
		}
	})
	t.Run("sub", func(t *testing.T) {
		t.Parallel()
		opened = true
	})
}
