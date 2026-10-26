package p

import (
	"sync/atomic"
	"testing"
)

var _ atomic.Int64

func TestA(t *testing.T) {
	count := 0
	t.Run("sub", func(t *testing.T) {
		t.Parallel()
		var count int
		count++
	})
	if count != 0 {
		t.Error("count")
	}
}
