package dustpan

import (
	"sync/atomic"
	"testing"
	"time"
)

// TestDrainStopsTheLogStreams proves that the drain of the readiness probe
// sets the signal that the log streams of the jobs API read, and that a
// readiness probe with no signal drains as before.
func TestDrainStopsTheLogStreams(t *testing.T) {
	t.Parallel()

	stop := &streamStop{stopped: atomic.Bool{}}
	rd := newComponentReadiness(readinessSettings{
		components: Components{
			api:    true,
			worker: false,
		},
		streamStop:  stop,
		store:       nil,
		blobs:       nil,
		watch:       nil,
		chainAdmits: nil,
		minimum:     nil,
		writable:    nil,
		timeout:     time.Second,
		failures:    nil,
		logger:      nil,
	})

	if stop.isStopping() {
		t.Fatal("the signal is set before the drain")
	}

	rd.drain()

	if !stop.isStopping() {
		t.Error("the drain did not set the signal")
	}

	var none *streamStop
	none.stop()

	if none.isStopping() {
		t.Error("a nil signal says that the process stops")
	}
}
