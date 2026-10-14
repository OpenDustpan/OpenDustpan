package accesstoken

import (
	"context"
	"sync"
	"time"
)

// policyTTL is how long AuthenticateToken uses a policy that it read. A
// change on another instance takes effect on this one after this time at
// most. A change through this Service takes effect at once.
const policyTTL = 5 * time.Second

// policyCache keeps the policy that the last read returned, for policyTTL.
// It is safe for concurrent use.
type policyCache struct {
	readAt time.Time
	policy Policy
	mu     sync.Mutex
	valid  bool
}

// get returns the cached policy when it is younger than policyTTL at now,
// else reads it with read and keeps it. It does not hold the lock during
// the read, so a slow read does not stop the requests that a cached policy
// can answer. A failed read returns its error and keeps nothing, so the
// caller fails closed.
func (c *policyCache) get(
	ctx context.Context,
	now time.Time,
	read func(context.Context) (Policy, error),
) (Policy, error) {
	if p, ok := c.fresh(now); ok {
		return p, nil
	}

	p, err := read(ctx)
	if err != nil {
		return Policy{}, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.valid || !now.Before(c.readAt) {
		c.policy, c.readAt, c.valid = p, now, true
	}

	return p, nil
}

// fresh returns the cached policy when it is younger than policyTTL at now.
func (c *policyCache) fresh(now time.Time) (Policy, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if age := now.Sub(c.readAt); c.valid && age >= 0 && age < policyTTL {
		return c.policy, true
	}

	return DefaultPolicy(), false
}

// forget drops the cached policy, so the next get reads it.
func (c *policyCache) forget() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.valid = false
}
