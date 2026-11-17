package gate_test

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/gate"
)

func TestNewPruneRequest(t *testing.T) {
	t.Parallel()

	cutoff := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name    string
		cutoff  time.Time
		batch   int
		wantErr bool
	}{
		{"smallest batch", cutoff, 1, false},
		{"largest batch", cutoff, gate.MaxPruneBatch, false},
		{"zero cutoff", time.Time{}, 10, true},
		{"batch zero", cutoff, 0, true},
		{"batch negative", cutoff, -1, true},
		{"batch above the largest", cutoff, gate.MaxPruneBatch + 1, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			req, err := gate.NewPruneRequest(c.cutoff, c.batch)
			if c.wantErr {
				if !errors.Is(err, gate.ErrInvalid) {
					t.Fatalf("NewPruneRequest() = %v, want ErrInvalid", err)
				}

				return
			}

			if err != nil {
				t.Fatalf("NewPruneRequest() = %v", err)
			}

			if !req.Cutoff().Equal(c.cutoff) || req.Batch() != c.batch {
				t.Fatalf("request = %v and %d, want %v and %d", req.Cutoff(), req.Batch(), c.cutoff, c.batch)
			}
		})
	}
}
