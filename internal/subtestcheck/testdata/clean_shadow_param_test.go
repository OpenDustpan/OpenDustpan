package p

import (
	"sync/atomic"
	"testing"
)

var _ atomic.Int64

func TestA(t *testing.T) {
	opened := false
	t.Run("sub", func(t *testing.T) {
		t.Parallel()
		check := func(opened bool) {
			opened = !opened
		}
		check(false)
	})
	if opened {
		t.Error("opened")
	}
}
