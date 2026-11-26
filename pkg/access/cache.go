package access

import (
	"context"
	"slices"
	"sync"
)

// requestCache keeps what the decisions of one request read: the subjects
// of each caller and the custom roles. It lives as long as the context of
// the request, never longer, so a change takes effect on the next request.
type requestCache struct {
	subjects map[string][]Subject
	roles    map[string]Role
	units    map[string]string
	// reach is the value of the setting that decides whether a grant on the
	// installation reaches each namespace. It is nil until a decision reads it.
	reach *bool
	mu    sync.Mutex
}

// requestCacheKey is the context key of the request cache.
type requestCacheKey struct{}

// WithRequestCache returns a context that keeps the reads of the decisions
// made with it: the subjects of a caller are read once, and each custom
// role is read once. The HTTP adapter calls it once for each request. A
// context without it reads on each decision.
func WithRequestCache(ctx context.Context) context.Context {
	return context.WithValue(ctx, requestCacheKey{}, &requestCache{
		subjects: map[string][]Subject{},
		roles:    map[string]Role{},
		units:    map[string]string{},
		reach:    nil,
		mu:       sync.Mutex{},
	})
}

// WithoutRequestCache returns a context that keeps no reads, under a
// context that may keep them: a check after a write reads the current state,
// not the state that the request read before the write.
func WithoutRequestCache(ctx context.Context) context.Context {
	return context.WithValue(ctx, requestCacheKey{}, (*requestCache)(nil))
}

// cacheOf returns the request cache of ctx, or nil when ctx has none. Each
// method of a nil cache keeps nothing and finds nothing.
func cacheOf(ctx context.Context) *requestCache {
	c, ok := ctx.Value(requestCacheKey{}).(*requestCache)
	if !ok {
		return nil
	}

	return c
}

func (c *requestCache) subjectsOf(subject string) ([]Subject, bool) {
	if c == nil {
		return nil, false
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	s, ok := c.subjects[subject]

	return slices.Clone(s), ok
}

func (c *requestCache) keepSubjects(subject string, subjects []Subject) {
	if c == nil {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.subjects[subject] = slices.Clone(subjects)
}

func (c *requestCache) role(id string) (Role, bool) {
	if c == nil {
		return Role{}, false //nolint:exhaustruct_v5 // The zero role answers no role.
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	r, ok := c.roles[id]

	return r, ok
}

func (c *requestCache) keepRole(r Role) {
	if c == nil {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.roles[r.ID] = r
}

// reachValue returns the kept value of the reach setting, and nil when the
// cache has not read it.
func (c *requestCache) reachValue() *bool {
	if c == nil {
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	return c.reach
}

func (c *requestCache) keepReach(value bool) {
	if c == nil {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.reach = &value
}

// unitID returns the stable ID of the unit with the wire form ref, "" for a
// unit that does not exist, and false when the cache has not read it.
func (c *requestCache) unitID(ref string) (string, bool) {
	if c == nil {
		return "", false
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	id, ok := c.units[ref]

	return id, ok
}

func (c *requestCache) keepUnitID(ref, id string) {
	if c == nil {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.units[ref] = id
}
