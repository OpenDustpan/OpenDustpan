package p

import (
	"sync/atomic"
	"testing"
)

var _ atomic.Int64

func TestA(t *testing.T) {
	opened := false
	t.Run("sub", func(t *testing.T) {
		opened = true
	})
	if opened {
		t.Error("opened")
	}
}
