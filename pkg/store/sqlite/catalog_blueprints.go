package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/internal/catalog"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/catalogstore"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

// CatalogBlueprints returns the store of catalog blueprints. Each write checks
// the installation state and the fence of its namespace, and appends its audit
// entries in its own transaction.
func (s *Store) CatalogBlueprints() CatalogBlueprintStore {
	return CatalogBlueprintStore{s: s}
}

// CatalogBlueprintStore is catalog.BlueprintStore over the database of a Store.
type CatalogBlueprintStore struct {
	s *Store
}

var _ catalog.BlueprintStore = CatalogBlueprintStore{s: nil}

// inFencedEntriesTx runs fn and appends e to the chain of the namespace of
// fence in one fenced transaction, so the change and its entries commit
// together, or neither does. The checks run in this order: the entries, then
// the fence in the transaction, then fn, then the appends.
func (s *Store) inFencedEntriesTx(
	ctx context.Context, fence namespace.Fence, e []audit.Entry, fn func(q *db.Queries) error,
) error {
	for i := range e {
		if err := e[i].Validate(); err != nil {
			return fmt.Errorf("audit entry: %w", err)
		}

		if e[i].Namespace != fence.Namespace {
			return fmt.Errorf("%w: entry names %q, fence names %q",
				audit.ErrInvalidEntry, e[i].Namespace, fence.Namespace)
		}
	}

	return s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		if err := fn(q); err != nil {
			return err
		}

		for i := range e {
			if _, err := appendAuditEntry(ctx, q, e[i]); err != nil {
				return fmt.Errorf("append audit entry: %w", err)
			}
		}

		return nil
	})
}

// PutDraft implements catalog.BlueprintStore.
func (c CatalogBlueprintStore) PutDraft(
	ctx context.Context, b catalog.Blueprint, version int64, fence namespace.Fence, e []audit.Entry,
) (int64, error) {
	if err := catalogstore.CheckDraftWrite(&b, version, fence, e); err != nil {
		return 0, err
	}

	doc, err := catalogstore.EncodeDraft(&b)
	if err != nil {
		return 0, err
	}

	now := codec.Millis(time.Now())
	ref := catalog.BlueprintRef{Namespace: fence.Namespace, Name: b.Name}

	c.s.mu.RLock()
	defer c.s.mu.RUnlock()

	err = c.s.inFencedEntriesTx(ctx, fence, e, func(q *db.Queries) error {
		if version > 0 {
			return updateCatalogDraft(ctx, q, catalogDraftWrite{ref: ref, doc: doc, now: now, version: version})
		}

		return insertCatalogDraft(ctx, q, catalogDraftWrite{ref: ref, doc: doc, now: now, version: 0})
	})
	if err != nil {
		return 0, err
	}

	return version + 1, nil
}

// catalogDraftWrite is one write of the draft of ref: its JSON, the time, and
// the version that the caller read, 0 for a new blueprint.
type catalogDraftWrite struct {
	ref     catalog.BlueprintRef
	doc     string
	now     int64
	version int64
}

// updateCatalogDraft writes the draft of w.ref when it is at w.version.
func updateCatalogDraft(ctx context.Context, q *db.Queries, w catalogDraftWrite) error {
	n, err := q.UpdateCatalogBlueprintDraft(ctx, db.UpdateCatalogBlueprintDraftParams{
		DraftJson: w.doc, DraftUpdatedAtMs: w.now, Namespace: w.ref.Namespace.String(), Name: w.ref.Name,
		DraftVersion: w.version,
	})
	if err != nil {
		return fmt.Errorf("update blueprint draft: %w", err)
	}

	if n == 0 {
		return fmt.Errorf("%w: draft %s is not at version %d", catalog.ErrConflict, w.ref, w.version)
	}

	return nil
}

// insertCatalogDraft makes the blueprint w.ref with its first draft, below the
// limit of blueprints of its namespace.
func insertCatalogDraft(ctx context.Context, q *db.Queries, w catalogDraftWrite) error {
	ns := w.ref.Namespace.String()

	n, err := q.InsertCatalogBlueprint(ctx, db.InsertCatalogBlueprintParams{
		Namespace: ns, Name: w.ref.Name, DraftJson: w.doc, DraftUpdatedAtMs: w.now,
	})
	if err != nil {
		return fmt.Errorf("insert blueprint: %w", err)
	}

	if n == 0 {
		return fmt.Errorf("%w: blueprint %s exists", catalog.ErrConflict, w.ref)
	}

	// The write lock of the transaction is held, so the count is exact: no
	// other insert runs between this insert and the commit.
	count, err := q.CountCatalogBlueprints(ctx, ns)
	if err != nil {
		return fmt.Errorf("count blueprints: %w", err)
	}

	if count > catalog.MaxBlueprints {
		return fmt.Errorf("%w: %s has %d blueprints", catalog.ErrLimit, ns, catalog.MaxBlueprints)
	}

	return nil
}

// Draft implements catalog.BlueprintStore.
func (c CatalogBlueprintStore) Draft(ctx context.Context, ref catalog.BlueprintRef) (catalog.Blueprint, int64, error) {
	c.s.mu.RLock()
	defer c.s.mu.RUnlock()

	row, err := c.s.queries.GetCatalogBlueprintDraft(
		ctx, db.GetCatalogBlueprintDraftParams{Namespace: ref.Namespace.String(), Name: ref.Name},
	)
	if errors.Is(err, sql.ErrNoRows) {
		return catalog.Blueprint{}, 0, fmt.Errorf("%w: draft %s", catalog.ErrNotFound, ref)
	}

	if err != nil {
		return catalog.Blueprint{}, 0, fmt.Errorf("select blueprint draft: %w", err)
	}

	b, err := catalogstore.Draft(ref, row.DraftJson)
	if err != nil {
		return catalog.Blueprint{}, 0, err
	}

	return b, row.DraftVersion, nil
}

// Publish implements catalog.BlueprintStore.
func (c CatalogBlueprintStore) Publish(
	ctx context.Context, p catalog.Publication, fence namespace.Fence, e []audit.Entry,
) (catalog.Version, error) {
	if err := catalogstore.CheckPublication(&p, fence, e); err != nil {
		return catalog.Version{}, err
	}

	doc, err := catalogstore.EncodeVersion(&p.Blueprint)
	if err != nil {
		return catalog.Version{}, err
	}

	ns := fence.Namespace.String()
	row := catalogstore.VersionRow{
		Namespace: ns, Name: p.Ref.Name, DocumentJSON: doc, ExprVersion: p.Blueprint.ExprVersion,
		PublishedBy: p.By, PublishedAtMs: codec.Millis(p.At), Number: 0, DraftVersion: p.DraftVersion, Retired: 0,
	}

	c.s.mu.RLock()
	defer c.s.mu.RUnlock()

	err = c.s.inFencedEntriesTx(ctx, fence, e, func(q *db.Queries) error {
		return publishNext(ctx, q, &p, &row)
	})
	if err != nil {
		return catalog.Version{}, err
	}

	return catalogstore.Version(row)
}

// publishNext inserts the next version for p in the open transaction. It
// checks, in this order: the draft is at p.DraftVersion, p holds the stored
// draft, the draft version has no version, and the blueprint is below its
// limit of versions. It sets row.Number.
func publishNext(ctx context.Context, q *db.Queries, p *catalog.Publication, row *catalogstore.VersionRow) error {
	if err := checkDraftOfPublication(ctx, q, p); err != nil {
		return err
	}

	ns := p.Ref.Namespace.String()

	last, err := q.LastCatalogBlueprintVersion(
		ctx, db.LastCatalogBlueprintVersionParams{Namespace: ns, Name: p.Ref.Name},
	)
	if err != nil {
		return fmt.Errorf("select last blueprint version: %w", err)
	}

	if last >= catalog.MaxVersions {
		return fmt.Errorf("%w: %s has %d versions", catalog.ErrLimit, p.Ref, catalog.MaxVersions)
	}

	row.Number = last + 1

	err = q.InsertCatalogBlueprintVersion(ctx, db.InsertCatalogBlueprintVersionParams{
		Namespace: ns, Name: p.Ref.Name, Number: row.Number, DraftVersion: row.DraftVersion,
		DocumentJson: row.DocumentJSON, ExprVersion: row.ExprVersion, PublishedBy: row.PublishedBy,
		PublishedAtMs: row.PublishedAtMs,
	})
	if err != nil {
		return fmt.Errorf("insert blueprint version: %w", err)
	}

	return nil
}

// checkDraftOfPublication checks that p publishes the stored draft, once.
func checkDraftOfPublication(ctx context.Context, q *db.Queries, p *catalog.Publication) error {
	ns := p.Ref.Namespace.String()

	stored, err := q.GetCatalogBlueprintDraft(ctx, db.GetCatalogBlueprintDraftParams{Namespace: ns, Name: p.Ref.Name})
	if errors.Is(err, sql.ErrNoRows) || (err == nil && stored.DraftVersion != p.DraftVersion) {
		return fmt.Errorf("%w: draft %s is not at version %d", catalog.ErrConflict, p.Ref, p.DraftVersion)
	}

	if err != nil {
		return fmt.Errorf("select draft: %w", err)
	}

	if checkErr := catalogstore.CheckStoredDraft(p.Ref, stored.DraftJson, &p.Blueprint); checkErr != nil {
		return checkErr
	}

	number, err := q.GetCatalogBlueprintVersionOfDraft(
		ctx,
		db.GetCatalogBlueprintVersionOfDraftParams{Namespace: ns, Name: p.Ref.Name, DraftVersion: p.DraftVersion},
	)
	if err == nil {
		return fmt.Errorf("%w: %s draft %d is version %d", catalog.ErrAlreadyPublished, p.Ref, p.DraftVersion, number)
	}

	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("select version of draft: %w", err)
	}

	return nil
}

// Version implements catalog.BlueprintStore.
func (c CatalogBlueprintStore) Version(ctx context.Context, ref catalog.BlueprintRef, n int) (catalog.Version, error) {
	c.s.mu.RLock()
	defer c.s.mu.RUnlock()

	row, err := c.s.queries.GetCatalogBlueprintVersion(ctx, db.GetCatalogBlueprintVersionParams{
		Namespace: ref.Namespace.String(), Name: ref.Name, Number: int64(n),
	})
	if errors.Is(err, sql.ErrNoRows) {
		return catalog.Version{}, fmt.Errorf("%w: %s version %d", catalog.ErrNotFound, ref, n)
	}

	if err != nil {
		return catalog.Version{}, fmt.Errorf("select blueprint version: %w", err)
	}

	v, err := catalogstore.Version(catalogstore.VersionRow{
		Namespace: row.Namespace, Name: row.Name, DocumentJSON: row.DocumentJson, ExprVersion: row.ExprVersion,
		PublishedBy: row.PublishedBy, PublishedAtMs: row.PublishedAtMs, Number: row.Number,
		DraftVersion: row.DraftVersion, Retired: row.Retired,
	})
	if err != nil {
		return catalog.Version{}, err
	}

	// Defense in depth: the query matched the namespace and the name. This
	// check stays so that a change of the query cannot serve a version of
	// another blueprint.
	if v.Ref() != ref || v.Number != n {
		return catalog.Version{}, fmt.Errorf("%w: %s version %d", catalog.ErrNotFound, ref, n)
	}

	return v, nil
}

// ListBlueprints implements catalog.BlueprintStore. A row that fails its checks
// is an error, not a gap in the list, so a damaged blueprint never reads as a
// blueprint that does not exist.
func (c CatalogBlueprintStore) ListBlueprints(ctx context.Context, ns namespace.Name) ([]catalog.Version, error) {
	c.s.mu.RLock()
	defer c.s.mu.RUnlock()

	rows, err := c.s.queries.ListCatalogBlueprintLatest(ctx, ns.String())
	if err != nil {
		return nil, fmt.Errorf("select blueprints: %w", err)
	}

	out := make([]catalog.Version, 0, len(rows))

	for _, row := range rows {
		v, vErr := catalogstore.Version(catalogstore.VersionRow{
			Namespace: row.Namespace, Name: row.Name, DocumentJSON: row.DocumentJson, ExprVersion: row.ExprVersion,
			PublishedBy: row.PublishedBy, PublishedAtMs: row.PublishedAtMs, Number: row.Number,
			DraftVersion: row.DraftVersion, Retired: row.Retired,
		})
		if vErr != nil {
			return nil, vErr
		}

		// Defense in depth: the query matched the namespace. This check stays so
		// that a change of the query cannot list a blueprint of another namespace.
		if v.Namespace != ns {
			return nil, fmt.Errorf("%w: list of %s holds a version of %s", catalogstore.ErrCorrupt, ns, v.Namespace)
		}

		out = append(out, v)
	}

	return out, nil
}

// Retire implements catalog.BlueprintStore.
func (c CatalogBlueprintStore) Retire(
	ctx context.Context, ref catalog.BlueprintRef, n int, fence namespace.Fence, e []audit.Entry,
) error {
	if err := catalogstore.CheckRef(ref, n, fence, e); err != nil {
		return err
	}

	c.s.mu.RLock()
	defer c.s.mu.RUnlock()

	return c.s.inFencedEntriesTx(ctx, fence, e, func(q *db.Queries) error {
		rows, err := q.RetireCatalogBlueprintVersion(ctx, db.RetireCatalogBlueprintVersionParams{
			Namespace: ref.Namespace.String(), Name: ref.Name, Number: int64(n),
		})
		if err != nil {
			return fmt.Errorf("retire blueprint version: %w", err)
		}

		if rows == 0 {
			return fmt.Errorf("%w: %s version %d", catalog.ErrNotFound, ref, n)
		}

		return nil
	})
}

// SetExprVersion implements catalog.BlueprintStore.
func (c CatalogBlueprintStore) SetExprVersion(
	ctx context.Context, ch catalog.ExprVersionChange, fence namespace.Fence, e []audit.Entry,
) error {
	if err := catalogstore.CheckRef(ch.Ref, ch.Number, fence, e); err != nil {
		return err
	}

	if err := catalogstore.CheckExprVersion(ch.Expr); err != nil {
		return err
	}

	c.s.mu.RLock()
	defer c.s.mu.RUnlock()

	return c.s.inFencedEntriesTx(ctx, fence, e, func(q *db.Queries) error {
		rows, err := q.SetCatalogBlueprintExprVersion(ctx, db.SetCatalogBlueprintExprVersionParams{
			ExprVersion: ch.Expr, Namespace: ch.Ref.Namespace.String(), Name: ch.Ref.Name, Number: int64(ch.Number),
		})
		if err != nil {
			return fmt.Errorf("set blueprint Expr version: %w", err)
		}

		if rows == 0 {
			return fmt.Errorf("%w: %s version %d", catalog.ErrNotFound, ch.Ref, ch.Number)
		}

		return nil
	})
}
