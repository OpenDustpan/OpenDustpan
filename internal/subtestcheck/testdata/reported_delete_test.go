package p

import (
	"sync/atomic"
	"testing"
)

var _ atomic.Int64

func TestA(t *testing.T) {
	m := map[string]bool{"a": true}
	t.Run("sub", func(t *testing.T) {
		t.Parallel()
		delete(m, "a")
	})
	if len(m) != 0 {
		t.Error("left")
	}
}
