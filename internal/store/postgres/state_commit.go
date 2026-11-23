package postgres

import (
	"context"

	"opendustpan.org/opendustpan/internal/store/codec"
	"opendustpan.org/opendustpan/internal/store/postgres/internal/db"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// stateCommit wraps a fenced state version commit in one transaction.
type stateCommit struct {
	store *Store
	fence namespace.Fence
	// retryFinalize lets a repeated finalize report the result that already
	// committed. Without it, a finalize of a version that is not pending is
	// refused as before.
	retryFinalize bool
}

// newStateCommit builds the unexported transaction guard for state version
// commits only. It marks a state version current only inside one fenced
// transaction. Lock writes do not use it: they go through SaveWorkspace in
// one fenced transaction.
func newStateCommit(store *Store, fence namespace.Fence) stateCommit {
	return stateCommit{store: store, fence: fence, retryFinalize: false}
}

func (c stateCommit) FinalizeStateVersion(ctx context.Context, sv workspace.StateVersion) error {
	return c.commitFinalize(ctx, sv, nil)
}

func (c stateCommit) FinalizeStateVersionExpecting(
	ctx context.Context, sv workspace.StateVersion, expect workspace.StateVersionID,
) error {
	return c.commitFinalize(ctx, sv, &expect)
}

// withRetry returns the guard with the idempotent retry of a finalize.
func (c stateCommit) withRetry() stateCommit {
	c.retryFinalize = true
	return c
}

// commitFinalize finalizes sv in one fenced transaction, with the expected
// current version when expect is set.
func (c stateCommit) commitFinalize(
	ctx context.Context, sv workspace.StateVersion, expect *workspace.StateVersionID,
) error {
	row, err := codec.FinalizeStateVersionRow(sv)
	if err != nil {
		return err
	}

	return c.store.inFencedTx(ctx, c.fence, func(q *db.Queries) error {
		return finalizeInTx(
			ctx,
			unitStateTx{q: q, logger: c.store.logger, retryFinalize: c.retryFinalize},
			sv,
			row,
			expect,
		)
	})
}
