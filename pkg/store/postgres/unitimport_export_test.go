package postgres //nolint:testpackage // The Tool seams are unexported, so the exporter must stay in that package.

import (
	"context"

	"opendustpan.org/opendustpan/internal/unitimport"
	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

// SeedLedgerRow stores a ledger row in its own transaction, as a Tool adapter
// does in the transaction of its history item.
func (i *Imports) SeedLedgerRow(ctx context.Context, ns namespace.Name, r unitimport.LedgerRow) error {
	return i.store.inTx(ctx, func(q *db.Queries) error { return putLedgerRow(ctx, q, ns, r) })
}

// SeedTarget records the target of an import unit in its own transaction, as a
// Tool adapter does in the transaction of Create.
func (i *Imports) SeedTarget(ctx context.Context, key unitimport.UnitKey, target history.Unit) error {
	return i.store.inTx(ctx, func(q *db.Queries) error { return setImportUnitTarget(ctx, q, key, target) })
}
