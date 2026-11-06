package dustpan

import (
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/manuallock"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// newManualLockService builds the manual lock service on the namespace store
// when that store also holds manual locks. A custom store without them gets no
// service: the lock routes are not served and the tools take their own locks
// as before.
//
// Defense in depth: the lock routes check the same actions with the same
// chain, and this service checks them again with the authorizer that it gets.
// It does not rely on the route check, so a caller that skips the route still
// meets the IP allowlist gate and the namespace service rule.
func newManualLockService(
	namespaces namespace.Store, jobs job.Store, now func() time.Time, authz identity.Authorizer,
) (*manuallock.Service, error) {
	store, ok := namespaces.(manuallock.Store)
	if !ok {
		return nil, nil //nolint:nilnil // No manual lock service: the store holds no manual locks.
	}

	// The clock of the job store lets a tool lock request with the job token of
	// a phase that started before a manual lock pass that lock. Without a job
	// store the clock stays nil, and no job token passes any lock.
	var phases manuallock.PhaseClock
	if jobs != nil {
		phases = job.PhaseClock{Jobs: jobs}
	}

	svc, err := manuallock.NewService(manuallock.Config{
		Store:      store,
		Namespaces: namespaces,
		Authorizer: authz,
		Now:        now,
		Phases:     phases,
	})
	if err != nil {
		return nil, fmt.Errorf("manual lock service: %w", err)
	}

	return svc, nil
}
