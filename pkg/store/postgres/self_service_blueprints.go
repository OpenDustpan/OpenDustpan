package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/internal/selfservice"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/internal/selfservicestore"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

// SelfServiceBlueprints returns the store of self-service blueprints. Each write checks
// the installation state and the fence of its namespace, and appends its audit
// entries in its own transaction.
func (s *Store) SelfServiceBlueprints() SelfServiceBlueprintStore {
	return SelfServiceBlueprintStore{s: s}
}

// SelfServiceBlueprintStore is selfservice.BlueprintStore over the database of a Store.
type SelfServiceBlueprintStore struct {
	s *Store
}

var _ selfservice.BlueprintStore = SelfServiceBlueprintStore{s: nil}

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

	return s.inFencedUpdateTx(ctx, fence, func(q *db.Queries) error {
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

// PutDraft implements selfservice.BlueprintStore.
func (c SelfServiceBlueprintStore) PutDraft(
	ctx context.Context, b selfservice.Blueprint, version int64, fence namespace.Fence, e []audit.Entry,
) (int64, error) {
	if err := selfservicestore.CheckDraftWrite(&b, version, fence, e); err != nil {
		return 0, err
	}

	doc, err := selfservicestore.EncodeDraft(&b)
	if err != nil {
		return 0, err
	}

	now := codec.Millis(time.Now())
	ref := selfservice.BlueprintRef{Namespace: fence.Namespace, Name: b.Name}

	err = c.s.inFencedEntriesTx(ctx, fence, e, func(q *db.Queries) error {
		if version > 0 {
			return updateSelfServiceDraft(ctx, q, selfServiceDraftWrite{ref: ref, doc: doc, now: now, version: version})
		}

		return insertSelfServiceDraft(ctx, q, selfServiceDraftWrite{ref: ref, doc: doc, now: now, version: 0})
	})
	if err != nil {
		return 0, err
	}

	return version + 1, nil
}

// selfServiceDraftWrite is one write of the draft of ref: its JSON, the time, and
// the version that the caller read, 0 for a new blueprint.
type selfServiceDraftWrite struct {
	ref     selfservice.BlueprintRef
	doc     string
	now     int64
	version int64
}

// updateSelfServiceDraft writes the draft of w.ref when it is at w.version.
func updateSelfServiceDraft(ctx context.Context, q *db.Queries, w selfServiceDraftWrite) error {
	n, err := q.UpdateSelfServiceBlueprintDraft(ctx, db.UpdateSelfServiceBlueprintDraftParams{
		DraftJson: w.doc, DraftUpdatedAtMs: w.now, Namespace: w.ref.Namespace.String(), Name: w.ref.Name,
		DraftVersion: w.version,
	})
	if err != nil {
		return fmt.Errorf("update blueprint draft: %w", err)
	}

	if n == 0 {
		return fmt.Errorf("%w: draft %s is not at version %d", selfservice.ErrConflict, w.ref, w.version)
	}

	return nil
}

// insertSelfServiceDraft makes the blueprint w.ref with its first draft, below the
// limit of blueprints of its namespace.
func insertSelfServiceDraft(ctx context.Context, q *db.Queries, w selfServiceDraftWrite) error {
	ns := w.ref.Namespace.String()

	n, err := q.InsertSelfServiceBlueprint(ctx, db.InsertSelfServiceBlueprintParams{
		Namespace: ns, Name: w.ref.Name, DraftJson: w.doc, DraftUpdatedAtMs: w.now,
	})
	if err != nil {
		return fmt.Errorf("insert blueprint: %w", err)
	}

	if n == 0 {
		return fmt.Errorf("%w: blueprint %s exists", selfservice.ErrConflict, w.ref)
	}

	// The fence check locks the namespace row FOR UPDATE, so the count is exact:
	// no other insert of the namespace runs between this insert and the commit.
	count, err := q.CountSelfServiceBlueprints(ctx, ns)
	if err != nil {
		return fmt.Errorf("count blueprints: %w", err)
	}

	if count > selfservice.MaxBlueprints {
		return fmt.Errorf("%w: %s has %d blueprints", selfservice.ErrLimit, ns, selfservice.MaxBlueprints)
	}

	return nil
}

// Draft implements selfservice.BlueprintStore.
func (c SelfServiceBlueprintStore) Draft(
	ctx context.Context,
	ref selfservice.BlueprintRef,
) (selfservice.Blueprint, int64, error) {
	row, err := c.s.queries.GetSelfServiceBlueprintDraft(
		ctx, db.GetSelfServiceBlueprintDraftParams{Namespace: ref.Namespace.String(), Name: ref.Name},
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return selfservice.Blueprint{}, 0, fmt.Errorf("%w: draft %s", selfservice.ErrNotFound, ref)
	}

	if err != nil {
		return selfservice.Blueprint{}, 0, fmt.Errorf("select blueprint draft: %w", err)
	}

	b, err := selfservicestore.Draft(ref, row.DraftJson)
	if err != nil {
		return selfservice.Blueprint{}, 0, err
	}

	return b, row.DraftVersion, nil
}

// Publish implements selfservice.BlueprintStore.
func (c SelfServiceBlueprintStore) Publish(
	ctx context.Context, p selfservice.Publication, fence namespace.Fence, e []audit.Entry,
) (selfservice.Version, error) {
	if err := selfservicestore.CheckPublication(&p, fence, e); err != nil {
		return selfservice.Version{}, err
	}

	doc, err := selfservicestore.EncodeVersion(&p.Blueprint)
	if err != nil {
		return selfservice.Version{}, err
	}

	ns := fence.Namespace.String()
	row := selfservicestore.VersionRow{
		Namespace: ns, Name: p.Ref.Name, DocumentJSON: doc, ExprVersion: string(p.Blueprint.ExprVersion),
		PublishedBy: p.By, PublishedAtMs: codec.Millis(p.At), Number: 0, DraftVersion: p.DraftVersion, Retired: 0,
	}

	err = c.s.inFencedEntriesTx(ctx, fence, e, func(q *db.Queries) error {
		return publishNext(ctx, q, &p, &row)
	})
	if err != nil {
		return selfservice.Version{}, err
	}

	return selfservicestore.Version(row)
}

// publishNext inserts the next version for p in the open transaction. It
// checks, in this order: the draft is at p.DraftVersion, p holds the stored
// draft, the draft version has no version, and the blueprint is below its
// limit of versions. It sets row.Number.
func publishNext(
	ctx context.Context,
	q *db.Queries,
	p *selfservice.Publication,
	row *selfservicestore.VersionRow,
) error {
	if err := checkDraftOfPublication(ctx, q, p); err != nil {
		return err
	}

	ns := p.Ref.Namespace.String()

	last, err := q.LastSelfServiceBlueprintVersion(
		ctx, db.LastSelfServiceBlueprintVersionParams{Namespace: ns, Name: p.Ref.Name},
	)
	if err != nil {
		return fmt.Errorf("select last blueprint version: %w", err)
	}

	if last >= selfservice.MaxVersions {
		return fmt.Errorf("%w: %s has %d versions", selfservice.ErrLimit, p.Ref, selfservice.MaxVersions)
	}

	row.Number = last + 1

	err = q.InsertSelfServiceBlueprintVersion(ctx, db.InsertSelfServiceBlueprintVersionParams{
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
func checkDraftOfPublication(ctx context.Context, q *db.Queries, p *selfservice.Publication) error {
	ns := p.Ref.Namespace.String()

	stored, err := q.GetSelfServiceBlueprintDraft(
		ctx,
		db.GetSelfServiceBlueprintDraftParams{Namespace: ns, Name: p.Ref.Name},
	)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && stored.DraftVersion != p.DraftVersion) {
		return fmt.Errorf("%w: draft %s is not at version %d", selfservice.ErrConflict, p.Ref, p.DraftVersion)
	}

	if err != nil {
		return fmt.Errorf("select draft: %w", err)
	}

	if checkErr := selfservicestore.CheckStoredDraft(p.Ref, stored.DraftJson, &p.Blueprint); checkErr != nil {
		return checkErr
	}

	number, err := q.GetSelfServiceBlueprintVersionOfDraft(
		ctx,
		db.GetSelfServiceBlueprintVersionOfDraftParams{Namespace: ns, Name: p.Ref.Name, DraftVersion: p.DraftVersion},
	)
	if err == nil {
		return fmt.Errorf(
			"%w: %s draft %d is version %d",
			selfservice.ErrAlreadyPublished,
			p.Ref,
			p.DraftVersion,
			number,
		)
	}

	if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("select version of draft: %w", err)
	}

	return nil
}

// Version implements selfservice.BlueprintStore.
func (c SelfServiceBlueprintStore) Version(
	ctx context.Context,
	ref selfservice.BlueprintRef,
	n int,
) (selfservice.Version, error) {
	row, err := c.s.queries.GetSelfServiceBlueprintVersion(ctx, db.GetSelfServiceBlueprintVersionParams{
		Namespace: ref.Namespace.String(), Name: ref.Name, Number: int64(n),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return selfservice.Version{}, fmt.Errorf("%w: %s version %d", selfservice.ErrNotFound, ref, n)
	}

	if err != nil {
		return selfservice.Version{}, fmt.Errorf("select blueprint version: %w", err)
	}

	v, err := selfservicestore.Version(selfservicestore.VersionRow{
		Namespace: row.Namespace, Name: row.Name, DocumentJSON: row.DocumentJson, ExprVersion: row.ExprVersion,
		PublishedBy: row.PublishedBy, PublishedAtMs: row.PublishedAtMs, Number: row.Number,
		DraftVersion: row.DraftVersion, Retired: row.Retired,
	})
	if err != nil {
		return selfservice.Version{}, err
	}

	// Defense in depth: the query matched the namespace and the name. This
	// check stays so that a change of the query cannot serve a version of
	// another blueprint.
	if v.Ref() != ref || v.Number != n {
		return selfservice.Version{}, fmt.Errorf("%w: %s version %d", selfservice.ErrNotFound, ref, n)
	}

	return v, nil
}

// ListBlueprints implements selfservice.BlueprintStore. A row that fails its checks
// is an error, not a gap in the list, so a damaged blueprint never reads as a
// blueprint that does not exist.
func (c SelfServiceBlueprintStore) ListBlueprints(
	ctx context.Context,
	ns namespace.Name,
) ([]selfservice.Version, error) {
	rows, err := c.s.queries.ListSelfServiceBlueprintLatest(ctx, ns.String())
	if err != nil {
		return nil, fmt.Errorf("select blueprints: %w", err)
	}

	out := make([]selfservice.Version, 0, len(rows))

	for _, row := range rows {
		v, vErr := selfservicestore.Version(selfservicestore.VersionRow{
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
			return nil, fmt.Errorf("%w: list of %s holds a version of %s", selfservicestore.ErrCorrupt, ns, v.Namespace)
		}

		out = append(out, v)
	}

	return out, nil
}

// Retire implements selfservice.BlueprintStore.
func (c SelfServiceBlueprintStore) Retire(
	ctx context.Context, ref selfservice.BlueprintRef, n int, fence namespace.Fence, e []audit.Entry,
) error {
	if err := selfservicestore.CheckRef(ref, n, fence, e); err != nil {
		return err
	}

	return c.s.inFencedEntriesTx(ctx, fence, e, func(q *db.Queries) error {
		rows, err := q.RetireSelfServiceBlueprintVersion(ctx, db.RetireSelfServiceBlueprintVersionParams{
			Namespace: ref.Namespace.String(), Name: ref.Name, Number: int64(n),
		})
		if err != nil {
			return fmt.Errorf("retire blueprint version: %w", err)
		}

		if rows == 0 {
			return fmt.Errorf("%w: %s version %d", selfservice.ErrNotFound, ref, n)
		}

		return nil
	})
}

// SetExprVersion implements selfservice.BlueprintStore.
func (c SelfServiceBlueprintStore) SetExprVersion(
	ctx context.Context, ch selfservice.ExprVersionChange, fence namespace.Fence, e []audit.Entry,
) error {
	if err := selfservicestore.CheckRef(ch.Ref, ch.Number, fence, e); err != nil {
		return err
	}

	if err := selfservicestore.CheckExprVersion(ch.Expr); err != nil {
		return err
	}

	return c.s.inFencedEntriesTx(ctx, fence, e, func(q *db.Queries) error {
		rows, err := q.SetSelfServiceBlueprintExprVersion(ctx, db.SetSelfServiceBlueprintExprVersionParams{
			ExprVersion: ch.Expr, Namespace: ch.Ref.Namespace.String(), Name: ch.Ref.Name, Number: int64(ch.Number),
		})
		if err != nil {
			return fmt.Errorf("set blueprint Expr version: %w", err)
		}

		if rows == 0 {
			return fmt.Errorf("%w: %s version %d", selfservice.ErrNotFound, ch.Ref, ch.Number)
		}

		return nil
	})
}
