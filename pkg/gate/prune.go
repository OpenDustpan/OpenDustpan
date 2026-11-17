package gate

import (
	"fmt"
	"time"
)

// MaxPruneBatch is the largest batch size of one PruneExpired call.
const MaxPruneBatch = 1000

// PruneRequest asks Records.PruneExpired to delete the state.write records
// that are older than a cutoff, up to a batch size. Only NewPruneRequest makes
// a valid value: the zero value is refused by the store.
type PruneRequest struct {
	cutoff time.Time
	batch  int
}

// NewPruneRequest returns the request for the cutoff and the batch size. It
// returns ErrInvalid for a zero cutoff and for a batch size below 1 or above
// MaxPruneBatch.
func NewPruneRequest(cutoff time.Time, batch int) (PruneRequest, error) {
	if cutoff.IsZero() {
		return PruneRequest{}, fmt.Errorf("%w: the prune cutoff is not set", ErrInvalid)
	}

	if batch < 1 || batch > MaxPruneBatch {
		return PruneRequest{}, fmt.Errorf("%w: prune batch %d, give 1 to %d", ErrInvalid, batch, MaxPruneBatch)
	}

	return PruneRequest{cutoff: cutoff, batch: batch}, nil
}

// Cutoff returns the time before which a record is expired.
func (r PruneRequest) Cutoff() time.Time { return r.cutoff }

// Batch returns the most records that one call deletes.
func (r PruneRequest) Batch() int { return r.batch }
