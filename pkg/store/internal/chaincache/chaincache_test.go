package chaincache_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/store/internal/chaincache"
)

var errRead = errors.New("read failed")

func TestCache(t *testing.T) {
	t.Parallel()

	reads := 0
	reader := func(state audit.ChainState, found bool) chaincache.Reader {
		return func() (audit.ChainState, bool, error) {
			reads++

			return state, found, nil
		}
	}

	var c chaincache.Cache

	on, err := c.On(reader("", false))
	if err != nil || !on || reads != 1 {
		t.Fatalf("no record: got %v, %v after %d reads, want on after 1 read", on, err, reads)
	}

	if on, err := c.On(reader(audit.ChainStateOff, true)); err != nil || !on || reads != 1 {
		t.Fatalf("within the lag: got %v after %d reads, want the kept state and no read", on, reads)
	}

	c.Set(false)

	if on, err := c.On(reader(audit.ChainStateOn, true)); err != nil || on || reads != 1 {
		t.Fatalf("after Set(false): got %v after %d reads, want off and no read", on, reads)
	}

	var fresh chaincache.Cache

	failing := func() (audit.ChainState, bool, error) { return "", false, errRead }
	if _, err := fresh.On(failing); !errors.Is(err, errRead) {
		t.Fatalf("failed read: got %v, want errRead", err)
	}
}
