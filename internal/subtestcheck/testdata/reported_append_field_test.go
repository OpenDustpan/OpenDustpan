package p

import (
	"sync/atomic"
	"testing"
)

var _ atomic.Int64

type rec struct{ n int }

func TestA(t *testing.T) {
	var seen []string
	var r rec
	for _, name := range []string{"a", "b"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			seen = append(seen, name)
			r.n++
		})
	}
	if len(seen) != 2 {
		t.Errorf("seen %d", len(seen))
	}
	if r.n != 2 {
		t.Error("count")
	}
}
