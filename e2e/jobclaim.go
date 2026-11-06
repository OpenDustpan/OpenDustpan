//go:build e2e

package e2e

import (
	"context"
	"errors"
	"sync"
	"time"

	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/store/postgres"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// errNoClaimer reports a step that claims a phase on a server whose claimer
// the test process does not hold: the binary server, or a store that keeps no
// jobs.
var errNoClaimer = errors.New("the in-process server has no job claimer")

// claimLease is the lease of a claim that a step takes.
const claimLease = time.Minute

// claimerSlot keeps the job claimer that the in-process server built with
// the guard of its own gate store, so that a step claims a phase as the poll
// of a runner does.
type claimerSlot struct {
	mu      sync.Mutex
	claimer *job.DurableClaimer
}

// newClaimerSlot returns an empty slot.
func newClaimerSlot() *claimerSlot {
	return &claimerSlot{mu: sync.Mutex{}, claimer: nil}
}

// get returns the claimer of the slot, or nil.
func (s *claimerSlot) get() *job.DurableClaimer {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.claimer
}

// keep stores c in the slot and returns it.
func (s *claimerSlot) keep(c *job.DurableClaimer) *job.DurableClaimer {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.claimer = c

	return c
}

// claimerOptions returns the option that gives the in-process server the job
// claimer of st, and keeps the claimer in slot. It returns nil for a store
// that keeps no jobs or a nil slot.
func claimerOptions(st serverStore, slot *claimerSlot) []dustpan.Option {
	if slot == nil {
		return nil
	}

	var build dustpan.JobClaimerFunc

	switch s := any(st).(type) {
	case *sqlite.Store:
		build = func(g job.GateGuard) job.Claimer {
			return slot.keep(job.NewClaimer(s.Jobs(), s.DurableOperationsPort(), s, g))
		}
	case *postgres.Store:
		build = func(g job.GateGuard) job.Claimer {
			return slot.keep(job.NewClaimer(s.Jobs(), s.DurableOperationsPort(), s, g))
		}
	default:
		return nil
	}

	return []dustpan.Option{dustpan.WithJobClaimer(build)}
}

// ClaimJobPhase claims one phase of the runner pool poolID for owner with the
// job claimer of the in-process server, as the poll of a runner of the pool
// does. It returns durable.ErrNoWork when no phase of the pool waits for a
// claim.
func (e *Env) ClaimJobPhase(ctx context.Context, owner, poolID string) (job.Assignment, error) {
	if e.claimers == nil {
		return job.Assignment{}, errNoClaimer
	}

	c := e.claimers.get()
	if c == nil {
		return job.Assignment{}, errNoClaimer
	}

	return c.Claim(ctx, job.ClaimRequest{
		Owner: owner, Kinds: []durable.Kind{job.RunnerKind(poolID)}, MaxVersions: nil, Lease: claimLease,
	})
}
