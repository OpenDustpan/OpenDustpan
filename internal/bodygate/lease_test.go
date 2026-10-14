package bodygate_test

import (
	"sync"
	"testing"

	"opendustpan.org/opendustpan/internal/bodygate"
)

func TestLeaseGrowFits(t *testing.T) {
	t.Parallel()

	gate := bodygate.New(10 << 20)

	lease, ok := bodygate.NewLease(gate, 1<<20)
	if !ok {
		t.Fatal("NewLease refused 1 MiB in a 10 MiB gate")
	}

	defer lease.Release()

	if !lease.Grow(2 << 20) {
		t.Fatal("Grow refused 2 MiB in a 10 MiB gate")
	}

	if got := lease.Held(); got != 3<<20 {
		t.Fatalf("Held() = %d, want %d", got, 3<<20)
	}

	if got := gate.InFlight(); got != 3<<20 {
		t.Fatalf("InFlight() = %d, want %d", got, 3<<20)
	}
}

func TestLeaseGrowWithoutRoom(t *testing.T) {
	t.Parallel()

	gate := bodygate.New(2 << 20)

	lease, ok := bodygate.NewLease(gate, 1<<20)
	if !ok {
		t.Fatal("NewLease refused 1 MiB in a 2 MiB gate")
	}

	defer lease.Release()

	if lease.Grow(2 << 20) {
		t.Fatal("Grow accepted 2 MiB in a gate with 1 MiB left")
	}

	if got := lease.Held(); got != 1<<20 {
		t.Fatalf("Held() = %d, want %d after a refused Grow", got, 1<<20)
	}

	if got := gate.InFlight(); got != 1<<20 {
		t.Fatalf("InFlight() = %d, want %d after a refused Grow", got, 1<<20)
	}
}

func TestLeaseReleaseFreesAll(t *testing.T) {
	t.Parallel()

	gate := bodygate.New(10 << 20)

	lease, ok := bodygate.NewLease(gate, 1<<20)
	if !ok {
		t.Fatal("NewLease refused 1 MiB in a 10 MiB gate")
	}

	if !lease.Grow(2 << 20) {
		t.Fatal("Grow refused 2 MiB in a 10 MiB gate")
	}

	lease.Release()

	if got := gate.InFlight(); got != 0 {
		t.Fatalf("InFlight() = %d after Release, want 0", got)
	}

	lease.Release()

	if got := gate.InFlight(); got != 0 {
		t.Fatalf("InFlight() = %d after a second Release, want 0", got)
	}
}

func TestLeaseGrowAfterRelease(t *testing.T) {
	t.Parallel()

	gate := bodygate.New(10 << 20)

	lease, ok := bodygate.NewLease(gate, 1<<20)
	if !ok {
		t.Fatal("NewLease refused 1 MiB in a 10 MiB gate")
	}

	lease.Release()

	if lease.Grow(1) {
		t.Fatal("Grow after Release accepted, want false")
	}

	if got := gate.InFlight(); got != 0 {
		t.Fatalf("InFlight() = %d, want 0", got)
	}
}

func TestLeaseGrowNegative(t *testing.T) {
	t.Parallel()

	gate := bodygate.New(10 << 20)

	lease, ok := bodygate.NewLease(gate, 1<<20)
	if !ok {
		t.Fatal("NewLease refused 1 MiB in a 10 MiB gate")
	}

	defer lease.Release()

	if lease.Grow(-1) {
		t.Fatal("Grow(-1) accepted, want false")
	}

	if got := lease.Held(); got != 1<<20 {
		t.Fatalf("Held() = %d, want %d after Grow(-1)", got, 1<<20)
	}
}

func TestLeaseConcurrentGrowRelease(t *testing.T) {
	t.Parallel()

	gate := bodygate.New(64 << 20)

	var wg sync.WaitGroup

	for range 32 {
		wg.Go(func() {
			lease, ok := bodygate.NewLease(gate, 1<<20)
			if !ok {
				t.Error("NewLease refused 1 MiB in a 64 MiB gate")

				return
			}

			if !lease.Grow(1 << 20) {
				t.Error("Grow refused 1 MiB in a 64 MiB gate")
				lease.Release()

				return
			}

			lease.Release()
		})
	}

	wg.Wait()

	if got := gate.InFlight(); got != 0 {
		t.Fatalf("InFlight() = %d after 32 growers, want 0", got)
	}
}
