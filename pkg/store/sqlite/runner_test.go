package sqlite_test

import (
	"context"
	"fmt"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/runner/runnertest"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// runnerDomain makes the rows that the runner stores need, on the real
// durable store and the real namespaces of one SQLite store.
type runnerDomain struct {
	t       *testing.T
	s       *sqlite.Store
	counter atomic.Int64
}

var _ runnertest.Domain = (*runnerDomain)(nil)

func (d *runnerDomain) CreateNamespace(ctx context.Context, name namespace.Name) (namespace.Fence, error) {
	ns := namespace.Namespace{
		ID: namespace.NewID(), CreatedAt: time.UnixMilli(1_700_000_000_000).UTC(), Name: name, HomeRegion: "eu-west",
		FailoverVersion: 1, Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}

	if err := d.s.CreateNamespace(ctx, ns); err != nil {
		return namespace.Fence{}, fmt.Errorf("create namespace: %w", err)
	}

	return ns.Fence(), nil
}

func (d *runnerDomain) BumpFence(ctx context.Context, fence namespace.Fence) (namespace.Fence, error) {
	if _, err := d.s.BumpFailoverVersions(ctx); err != nil {
		return namespace.Fence{}, fmt.Errorf("bump failover versions: %w", err)
	}

	ns, err := d.s.GetNamespace(ctx, fence.Namespace)
	if err != nil {
		return namespace.Fence{}, fmt.Errorf("get namespace: %w", err)
	}

	return ns.Fence(), nil
}

func (d *runnerDomain) Claim(ctx context.Context, kind durable.Kind, owner string) (durable.Claim, error) {
	return d.ClaimLease(ctx, kind, owner, time.Hour)
}

func (d *runnerDomain) ClaimLease(
	ctx context.Context, kind durable.Kind, owner string, lease time.Duration,
) (durable.Claim, error) {
	ops := d.s.DurableOperations()

	key := fmt.Sprintf("op-%d", d.counter.Add(1))
	if _, _, err := ops.Start(ctx, durable.StartRequest{Key: key, Kind: kind, Version: 0, Input: nil}); err != nil {
		return durable.Claim{}, fmt.Errorf("start operation: %w", err)
	}

	op, err := ops.Claim(ctx, durable.ClaimRequest{
		Owner: owner, Kinds: []durable.Kind{kind}, MaxVersions: nil, Lease: lease,
	})
	if err != nil {
		return durable.Claim{}, fmt.Errorf("claim operation: %w", err)
	}

	return op.Claim, nil
}

func (d *runnerDomain) Takeover(ctx context.Context, c durable.Claim) (durable.Claim, error) {
	ops := d.s.DurableOperations()

	op, err := ops.Get(ctx, c.Operation)
	if err != nil {
		return durable.Claim{}, fmt.Errorf("get operation: %w", err)
	}

	if err = ops.Release(ctx, c); err != nil {
		return durable.Claim{}, fmt.Errorf("release claim: %w", err)
	}

	taken, err := ops.Claim(ctx, durable.ClaimRequest{
		Owner: c.Owner, Kinds: []durable.Kind{op.Kind}, MaxVersions: nil, Lease: time.Hour,
	})
	if err != nil {
		return durable.Claim{}, fmt.Errorf("claim operation again: %w", err)
	}

	return taken.Claim, nil
}

func (d *runnerDomain) End(ctx context.Context, c durable.Claim) error {
	if err := d.s.DurableOperations().Complete(ctx, c); err != nil {
		return fmt.Errorf("complete operation: %w", err)
	}

	return nil
}

func (d *runnerDomain) UsePool(ctx context.Context, _ namespace.Fence, id string) error {
	key := fmt.Sprintf("use-%s-%d", id, d.counter.Add(1))

	_, _, err := d.s.DurableOperations().Start(ctx, durable.StartRequest{
		Key: key, Kind: durable.Kind("job.runner." + id), Version: 0, Input: nil,
	})
	if err != nil {
		return fmt.Errorf("start operation: %w", err)
	}

	return nil
}

func (d *runnerDomain) AuditEntries(ctx context.Context, ns namespace.Name) ([]audit.Entry, error) {
	var out []audit.Entry

	for page := 1; ; page++ {
		listing, err := d.s.List(ctx, ns, audit.Page{Number: page, Size: audit.MaxPageSize})
		if err != nil {
			return nil, fmt.Errorf("list audit: %w", err)
		}

		out = append(out, listing.Entries...)

		if len(listing.Entries) < audit.MaxPageSize {
			break
		}
	}

	slices.SortFunc(out, func(a, b audit.Entry) int { return int(a.Sequence - b.Sequence) })

	return out, nil
}

func TestRunnersConformance(t *testing.T) {
	t.Parallel()

	runnertest.Run(t, func(t *testing.T) runnertest.Harness {
		t.Helper()

		s := open(t)
		r := s.Runners()

		return runnertest.Harness{
			Pools: r, Members: r, Sessions: r, Domain: &runnerDomain{t: t, s: s, counter: atomic.Int64{}},
		}
	})
}
