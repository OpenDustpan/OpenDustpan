package p

import (
	"sync/atomic"
	"testing"
)

var _ atomic.Int64

func TestA(t *testing.T) {
	opened := false
	other := 0
	t.Run("sub", func(t *testing.T) {
		t.Parallel()
		other++
		_ = opened
	})
	if opened {
		t.Error("opened")
	}
}
