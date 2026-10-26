package p

import (
	"sync/atomic"
	"testing"
)

var _ atomic.Int64

func TestA(t *testing.T) {
	got := map[string]int{}
	t.Run("sub", func(t *testing.T) {
		t.Parallel()
		got["a"] = 1
	})
	t.Fatalf("got %v", got)
}
