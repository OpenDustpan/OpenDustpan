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
}

// newStateCommit builds the unexported transaction guard for state version
// commits only. It marks a state version current only inside one fenced
// transaction. Lock writes do not use it: they go through SaveWorkspace in
// one fenced transaction.
func newStateCommit(store *Store, fence namespace.Fence) stateCommit {
	return stateCommit{store: store, fence: fence}
}

func (c stateCommit) FinalizeStateVersion(ctx context.Context, sv workspace.StateVersion) error {
	return c.commitFinalize(ctx, sv, nil)
}

func (c stateCommit) FinalizeStateVersionExpecting(
	ctx context.Context, sv workspace.StateVersion, expect workspace.StateVersionID,
) error {
	return c.commitFinalize(ctx, sv, &expect)
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
		return finalizeInTx(ctx, unitStateTx{q: q, logger: c.store.logger}, sv, row, expect)
	})
}
