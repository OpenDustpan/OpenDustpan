package runnerproxysvc

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// Process is one started job proxy of a claim. Only NewProcess makes one.
type Process struct {
	addr      string
	done      <-chan struct{}
	wait      func() error
	stop      func(context.Context) error
	stopWatch chan struct{}
	stopOnce  sync.Once
}

// NewProcess returns the handle of one job proxy. done closes when the
// process ends, wait returns the error of the process, and stop ends it.
func NewProcess(
	addr string, done <-chan struct{}, wait func() error, stop func(context.Context) error,
) *Process {
	if done == nil {
		closed := make(chan struct{})
		close(closed)

		done = closed
	}

	return &Process{
		addr: addr, done: done, wait: wait, stop: stop,
		stopWatch: make(chan struct{}), stopOnce: sync.Once{},
	}
}

// Addr returns the address of the proxy.
func (p *Process) Addr() string { return p.addr }

// Done returns the channel that closes when the proxy ends.
func (p *Process) Done() <-chan struct{} { return p.done }

// Err returns the error of the proxy, or nil.
func (p *Process) Err() error {
	if p.wait == nil {
		return nil
	}

	return p.wait()
}

// Stop ends the proxy and wakes the watcher of the coordinator.
func (p *Process) Stop(ctx context.Context) error {
	p.stopOnce.Do(func() { close(p.stopWatch) })

	if p.stop == nil {
		return nil
	}

	if err := p.stop(ctx); err != nil {
		return fmt.Errorf("%w: %w", ErrClose, err)
	}

	return nil
}

// StartFunc starts one job proxy for a claim.
type StartFunc func(ctx context.Context, claim Claim) (*Process, error)

// RouteFunc routes the sandbox of a claim to the proxy address only and
// drops each other packet. A second call for the same claim changes the
// address and keeps the drop.
type RouteFunc func(ctx context.Context, claim Claim, addr string) error

// RemoveRouteFunc removes the route of a claim whose proxy ended.
type RemoveRouteFunc func(ctx context.Context, claim Claim) error

// FailFunc records that a claim failed, with the fixed reason of the failure.
type FailFunc func(ctx context.Context, claim Claim, reason error)

// CoordinatorConfig holds the dependencies of a Coordinator. Each field is
// required.
type CoordinatorConfig struct {
	Start       StartFunc
	Route       RouteFunc
	RemoveRoute RemoveRouteFunc
	Fail        FailFunc
}

// Coordinator starts one job proxy per claim before the steps start, routes
// the sandbox to it, and fails the claim closed when the proxy is lost.
type Coordinator struct {
	cfg    CoordinatorConfig
	mu     sync.Mutex
	claims map[string]*Process
}

// NewCoordinator returns a Coordinator. It returns an error when cfg lacks a
// dependency.
func NewCoordinator(cfg CoordinatorConfig) (*Coordinator, error) {
	switch {
	case cfg.Start == nil:
		return nil, fmt.Errorf("%w: the proxy starter", ErrConfig)
	case cfg.Route == nil:
		return nil, fmt.Errorf("%w: the route", ErrConfig)
	case cfg.RemoveRoute == nil:
		return nil, fmt.Errorf("%w: the route remover", ErrConfig)
	case cfg.Fail == nil:
		return nil, fmt.Errorf("%w: the claim failer", ErrConfig)
	}

	return &Coordinator{cfg: cfg, mu: sync.Mutex{}, claims: map[string]*Process{}}, nil
}

// Start starts the job proxy of claim before any step starts, and routes the
// sandbox to it. A claim with no proxy rules starts nothing and keeps its
// routing. A start failure fails the claim and returns ErrStart.
func (c *Coordinator) Start(ctx context.Context, claim Claim) error {
	if claim.ID == "" {
		return fmt.Errorf("%w: the claim ID", ErrConfig)
	}

	place, err := Plan(claim.Isolation, claim.Rules)
	if err != nil {
		c.fail(ctx, claim, err)

		return err
	}

	if place.Proxies == 0 {
		return nil
	}

	c.mu.Lock()
	_, exists := c.claims[claim.ID]
	c.mu.Unlock()

	if exists {
		return fmt.Errorf("%w: claim %s already has a proxy", ErrConfig, claim.ID)
	}

	proc, err := c.cfg.Start(ctx, claim)

	switch {
	case err != nil:
		reason := fmt.Errorf("%w: %w", ErrStart, err)
		c.fail(ctx, claim, reason)

		return reason
	case proc == nil:
		reason := fmt.Errorf("%w: the starter returned no process", ErrStart)
		c.fail(ctx, claim, reason)

		return reason
	}

	if routeErr := c.cfg.Route(ctx, claim, proc.Addr()); routeErr != nil {
		stopErr := proc.Stop(ctx)

		reason := fmt.Errorf("%w: %w", ErrStart, routeErr)
		if stopErr != nil {
			reason = errors.Join(reason, stopErr)
		}

		c.fail(ctx, claim, reason)

		return reason
	}

	c.mu.Lock()
	c.claims[claim.ID] = proc
	c.mu.Unlock()

	go c.watch(context.WithoutCancel(ctx), claim, proc)

	return nil
}

// End stops the job proxy of claim and removes the route of the sandbox.
func (c *Coordinator) End(ctx context.Context, claim Claim) error {
	c.mu.Lock()
	proc, ok := c.claims[claim.ID]
	delete(c.claims, claim.ID)
	c.mu.Unlock()

	if !ok {
		return nil
	}

	stopErr := proc.Stop(ctx)

	if routeErr := c.cfg.RemoveRoute(ctx, claim); routeErr != nil {
		return errors.Join(stopErr, fmt.Errorf("%w: %w", ErrRoute, routeErr))
	}

	return stopErr
}

// watch fails the claim when its job proxy ends before End. The route stays
// proxy only: a lost proxy never opens a direct path.
func (c *Coordinator) watch(ctx context.Context, claim Claim, proc *Process) {
	select {
	case <-proc.Done():
	case <-proc.stopWatch:
		return
	}

	c.mu.Lock()

	current, ok := c.claims[claim.ID]
	if !ok || current != proc {
		c.mu.Unlock()

		return
	}

	delete(c.claims, claim.ID)
	c.mu.Unlock()

	reason := ErrLost
	if err := proc.Err(); err != nil {
		reason = fmt.Errorf("%w: %w", ErrLost, err)
	}

	c.fail(ctx, claim, reason)
}

// fail records a failed claim. A failure to write the record cannot change
// the decision: the claim is already failed.
func (c *Coordinator) fail(ctx context.Context, claim Claim, reason error) {
	c.cfg.Fail(ctx, claim, reason)
}
