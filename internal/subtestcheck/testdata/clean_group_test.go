package p

import (
	"sync/atomic"
	"testing"
)

var _ atomic.Int64

func TestA(t *testing.T) {
	opened := false
	t.Run("group", func(t *testing.T) {
		t.Run("sub", func(t *testing.T) {
			t.Parallel()
			opened = true
		})
	})
	if opened {
		t.Error("opened")
	}
}
