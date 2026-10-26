package p

import (
	"sync/atomic"
	"testing"
)

var _ atomic.Int64

func TestA(t *testing.T) {
	var calls atomic.Int64
	var legacy int64
	t.Run("sub", func(t *testing.T) {
		t.Parallel()
		calls.Add(1)
		atomic.StoreInt64(&legacy, 1)
	})
	if calls.Load() != 1 {
		t.Error("calls")
	}
	if atomic.LoadInt64(&legacy) != 1 {
		t.Error("legacy")
	}
}
