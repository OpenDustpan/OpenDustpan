package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/internal/store/postgres/internal/db"
	"opendustpan.org/opendustpan/internal/store/trustrulestore"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/trustrule"
)

var _ trustrule.BundleStore = TrustRules{s: nil}

// SaveBundle implements trustrule.BundleStore.
func (t TrustRules) SaveBundle(
	ctx context.Context, ns namespace.Name, f trustrule.BundleFields, fence namespace.Fence, e audit.Entry,
) (trustrule.Bundle, error) {
	w := trustrulestore.NewBundleWrite(f, e.At, false)

	return t.writeBundle(ctx, &bundleRequest{ns: ns, fields: f, write: w, fence: fence, entry: e})
}

// RotateBundle implements trustrule.BundleStore.
func (t TrustRules) RotateBundle(
	ctx context.Context, ns namespace.Name, f trustrule.BundleFields, fence namespace.Fence, e audit.Entry,
) (trustrule.Bundle, error) {
	w := trustrulestore.NewBundleWrite(f, e.At, true)

	return t.writeBundle(ctx, &bundleRequest{ns: ns, fields: f, write: w, fence: fence, entry: e})
}

// bundleRequest is one bundle save or rotate.
type bundleRequest struct {
	ns     namespace.Name
	fields trustrule.BundleFields
	write  trustrulestore.BundleWrite
	fence  namespace.Fence
	entry  audit.Entry
}

// writeBundle stores a new version of the bundle of the namespace of req. A
// rotate also moves the active pointer, and it needs the stored revision. The
// transaction holds the namespace row FOR UPDATE, so two writes of one bundle
// run in turn.
func (t TrustRules) writeBundle(ctx context.Context, req *bundleRequest) (trustrule.Bundle, error) {
	if err := trustrulestore.CheckBundleSave(req.ns, req.fields, req.write, req.fence); err != nil {
		return trustrule.Bundle{}, err
	}

	var out trustrule.Bundle

	err := t.s.inFencedAuditedTx(ctx, req.fence, &req.entry, func(q *db.Queries) error {
		b, txErr := writeBundleTx(ctx, q, req)
		if txErr != nil {
			return txErr
		}

		out = b

		return nil
	})
	if err != nil {
		return trustrule.Bundle{}, err
	}

	return out, nil
}

// writeBundleTx is the body of the transaction of writeBundle.
func writeBundleTx(ctx context.Context, q *db.Queries, req *bundleRequest) (trustrule.Bundle, error) {
	stored, versions, err := readBundleState(ctx, q, req.ns)
	if err != nil {
		return trustrule.Bundle{}, err
	}

	plan, err := trustrulestore.PlanBundle(bundleRow(stored), versions, req.write)
	if err != nil {
		return trustrule.Bundle{}, fmt.Errorf("plan the trust bundle write: %w", err)
	}

	if stored == nil {
		stored, err = insertBundleRow(ctx, q, req, &plan)
	} else {
		err = updateBundleRow(ctx, q, stored, &plan)
	}

	if err != nil {
		return trustrule.Bundle{}, err
	}

	req.entry.ObjectID = stored.ID

	err = insertBundleVersion(ctx, q, stored.ID, &plan, req.fields.PEM)
	if err != nil {
		return trustrule.Bundle{}, err
	}

	stored.ActiveVersion, stored.Revision = plan.Active, stored.Revision+1

	return loadBundle(ctx, q, stored)
}

// readBundleState reads the bundle row of ns and its versions. The row is nil
// when the namespace has no bundle.
func readBundleState(
	ctx context.Context, q *db.Queries, ns namespace.Name,
) (*db.TrustBundle, []trustrulestore.VersionRow, error) {
	stored, err := q.GetTrustBundleOfNamespace(ctx, ns.String())
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, nil
	}

	if err != nil {
		return nil, nil, fmt.Errorf("select the trust bundle: %w", err)
	}

	rows, err := q.ListTrustBundleVersions(ctx, stored.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("select the trust bundle versions: %w", err)
	}

	return &stored, bundleVersionRows(rows), nil
}

// insertBundleRow writes the first row of the bundle of the namespace of req
// and returns it with revision zero, which the caller moves to one.
func insertBundleRow(
	ctx context.Context, q *db.Queries, req *bundleRequest, plan *trustrulestore.BundlePlan,
) (*db.TrustBundle, error) {
	stored := db.TrustBundle{
		ID: string(trustrule.NewBundleID()), Namespace: req.ns.String(), ActiveVersion: plan.Active,
		Revision: 0, CreatedAtMs: plan.NowMs,
	}

	err := q.InsertTrustBundle(ctx, db.InsertTrustBundleParams{
		ID: stored.ID, Namespace: stored.Namespace, ActiveVersion: plan.Active, Revision: 1,
		CreatedAtMs: stored.CreatedAtMs,
	})
	if err != nil {
		return nil, fmt.Errorf("insert trust bundle: %w", err)
	}

	return &stored, nil
}

// updateBundleRow moves the active pointer and the revision of the stored row.
// A row that changed since the read gives trustrule.ErrStale.
func updateBundleRow(
	ctx context.Context, q *db.Queries, stored *db.TrustBundle, plan *trustrulestore.BundlePlan,
) error {
	n, err := q.UpdateTrustBundle(ctx, db.UpdateTrustBundleParams{
		ActiveVersion: plan.Active, Revision: stored.Revision + 1, ID: stored.ID, StoredRevision: stored.Revision,
	})
	if err != nil {
		return fmt.Errorf("update trust bundle: %w", err)
	}

	if n == 0 {
		return trustrule.ErrStale
	}

	return nil
}

// insertBundleVersion writes the new version of the plan and deletes the
// versions that the plan retires.
func insertBundleVersion(
	ctx context.Context, q *db.Queries, bundleID string, plan *trustrulestore.BundlePlan, pem string,
) error {
	err := q.InsertTrustBundleVersion(ctx, db.InsertTrustBundleVersionParams{
		BundleID: bundleID, Version: plan.Next, Pem: pem, CreatedAtMs: plan.NowMs,
		OverlapEndsAtMs: plan.OverlapEndsAtMs,
	})
	if err != nil {
		return fmt.Errorf("insert trust bundle version: %w", err)
	}

	for _, v := range plan.Retired {
		_, delErr := q.DeleteTrustBundleVersion(ctx, db.DeleteTrustBundleVersionParams{BundleID: bundleID, Version: v})
		if delErr != nil {
			return fmt.Errorf("delete the retired trust bundle version: %w", delErr)
		}
	}

	return nil
}

// GetBundle implements trustrule.BundleStore.
func (t TrustRules) GetBundle(ctx context.Context, ns namespace.Name) (trustrule.Bundle, error) {
	if err := trustrulestore.CheckBundleNamespace(ns); err != nil {
		return trustrule.Bundle{}, err
	}

	row, err := t.s.queries.GetTrustBundleOfNamespace(ctx, ns.String())
	if errors.Is(err, pgx.ErrNoRows) {
		return trustrule.Bundle{}, trustrule.ErrNotFound
	}

	if err != nil {
		return trustrule.Bundle{}, fmt.Errorf("select the trust bundle: %w", err)
	}

	b, err := loadBundle(ctx, t.s.queries, &row)
	if errors.Is(err, trustrulestore.ErrCorrupt) {
		return trustrule.Bundle{}, trustrule.ErrNotFound
	}

	if err != nil {
		return trustrule.Bundle{}, err
	}

	// Defense in depth: the query above already selects by namespace. This check
	// stays so that a changed query or a row from another source cannot return
	// the bundle of another namespace; it does not rely on the query.
	if err := trustrulestore.CheckBundleOfNamespace(b, ns); err != nil {
		return trustrule.Bundle{}, err
	}

	return b, nil
}

// DeleteBundleVersion implements trustrule.BundleStore.
func (t TrustRules) DeleteBundleVersion(
	ctx context.Context, ns namespace.Name, version int64, fence namespace.Fence, e audit.Entry,
) error {
	if err := trustrulestore.CheckBundleDelete(ns, fence); err != nil {
		return err
	}

	return t.s.inFencedAuditedTx(ctx, fence, &e, func(q *db.Queries) error {
		stored, err := q.GetTrustBundleOfNamespace(ctx, ns.String())
		if errors.Is(err, pgx.ErrNoRows) {
			return trustrule.ErrNotFound
		}

		if err != nil {
			return fmt.Errorf("select the trust bundle: %w", err)
		}

		if version == stored.ActiveVersion {
			return trustrule.ErrBundleActive
		}

		n, err := q.DeleteTrustBundleVersion(ctx, db.DeleteTrustBundleVersionParams{
			BundleID: stored.ID, Version: version,
		})
		if err != nil {
			return fmt.Errorf("delete the trust bundle version: %w", err)
		}

		if n == 0 {
			return trustrule.ErrNotFound
		}

		e.ObjectID = stored.ID

		n, err = q.UpdateTrustBundle(ctx, db.UpdateTrustBundleParams{
			ActiveVersion: stored.ActiveVersion, Revision: stored.Revision + 1, ID: stored.ID,
			StoredRevision: stored.Revision,
		})
		if err != nil {
			return fmt.Errorf("update trust bundle: %w", err)
		}

		if n == 0 {
			return trustrule.ErrStale
		}

		return nil
	})
}

// loadBundle reads the versions of the bundle row and returns the checked
// bundle.
func loadBundle(ctx context.Context, q *db.Queries, row *db.TrustBundle) (trustrule.Bundle, error) {
	rows, err := q.ListTrustBundleVersions(ctx, row.ID)
	if err != nil {
		return trustrule.Bundle{}, fmt.Errorf("select the trust bundle versions: %w", err)
	}

	b, err := trustrulestore.Bundle(bundleRow(row), bundleVersionRows(rows))
	if err != nil {
		return trustrule.Bundle{}, fmt.Errorf("read the trust bundle: %w", err)
	}

	return b, nil
}

// bundleRow returns the shared form of a stored bundle row. A nil row gives the
// row with no ID, which means that the namespace has no bundle.
func bundleRow(row *db.TrustBundle) trustrulestore.BundleRow {
	if row == nil {
		return trustrulestore.BundleRow{ID: "", Namespace: "", Active: 0, Revision: 0, CreatedAtMs: 0}
	}

	return trustrulestore.BundleRow{
		ID: row.ID, Namespace: row.Namespace, Active: row.ActiveVersion, Revision: row.Revision,
		CreatedAtMs: row.CreatedAtMs,
	}
}

// bundleVersionRows returns the shared form of the stored version rows.
func bundleVersionRows(rows []db.ListTrustBundleVersionsRow) []trustrulestore.VersionRow {
	out := make([]trustrulestore.VersionRow, 0, len(rows))

	for i := range rows {
		out = append(out, trustrulestore.VersionRow{
			Version: rows[i].Version, PEM: rows[i].Pem, CreatedAtMs: rows[i].CreatedAtMs,
			OverlapEndsAtMs: rows[i].OverlapEndsAtMs,
		})
	}

	return out
}
