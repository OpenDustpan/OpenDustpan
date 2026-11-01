package postgres //nolint:testpackage // The Tool seams are unexported, so the exporter must stay in that package.

import (
	"context"
	"fmt"

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

// UploadStatement holds the values of the commit statement of an upload
// version.
type UploadStatement struct {
	Namespace       string
	FailoverVersion int64
	ImportID        string
	UnitDir         string
	Facts           string
	Sequence        int64
	StartedAtMs     int64
	DeadlineMs      int64
	SkewMs          int64
}

// RunUploadStatement runs the commit statement of an upload version alone, with
// no transaction check, and returns the number of rows that it inserted. The
// tests use it to prove that each predicate of the statement stops a commit
// when the other layers do not.
func (i *Imports) RunUploadStatement(ctx context.Context, p UploadStatement) (int64, error) {
	n, err := i.store.queries.InsertImportUploadCommit(ctx, db.InsertImportUploadCommitParams{
		UnitDir: p.UnitDir, Sequence: p.Sequence, Facts: p.Facts, Namespace: p.Namespace,
		FailoverVersion: p.FailoverVersion, ImportID: p.ImportID, StartedAtMs: p.StartedAtMs,
		DeadlineMs: p.DeadlineMs, SkewMs: p.SkewMs,
	})
	if err != nil {
		return 0, fmt.Errorf("insert upload commit: %w", err)
	}

	return n, nil
}
