package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/objectcontext"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

var _ objectcontext.Store = (*Store)(nil)

// contextTarget is an object of a context call, resolved to the stable ID of
// its namespace and its key. The store resolves the names of a reference in
// the transaction of each call, so a rename between two calls changes no row.
type contextTarget struct {
	key         codec.ContextKey
	namespaceID string
}

// resolveContext resolves ref in the transaction q. found is false when the
// namespace or the collection that ref names does not exist: a read answers
// no entry then.
func resolveContext(ctx context.Context, q *db.Queries, ref objectcontext.Ref) (contextTarget, bool, error) {
	var none contextTarget

	nsID, err := q.GetNamespaceID(ctx, ref.Namespace.String())
	if errors.Is(err, pgx.ErrNoRows) {
		return none, false, nil
	}

	if err != nil {
		return contextTarget{}, false, fmt.Errorf("select namespace ID: %w", err)
	}

	collectionID := ""

	if ref.Kind == objectcontext.KindCollection {
		row, colErr := q.GetCollection(ctx, db.GetCollectionParams{
			Namespace: ref.Namespace.String(),
			NameKey:   collection.Name(ref.Name).Key(),
		})
		if errors.Is(colErr, pgx.ErrNoRows) {
			return none, false, nil
		}

		if colErr != nil {
			return contextTarget{}, false, fmt.Errorf("select collection: %w", colErr)
		}

		collectionID = row.ID
	}

	key, err := codec.ContextKeyOf(ref, collectionID)
	if err != nil {
		return contextTarget{}, false, fmt.Errorf("context key: %w", err)
	}

	return contextTarget{namespaceID: nsID, key: key}, true, nil
}

// contextRows returns the stored rows of target, both sources, API first.
func contextRows(ctx context.Context, q *db.Queries, t contextTarget) ([]db.ListObjectContextRow, error) {
	rows, err := q.ListObjectContext(ctx, db.ListObjectContextParams{
		NamespaceID: t.namespaceID,
		ObjectKind:  t.key.Kind,
		ObjectID:    t.key.ObjectID,
		ResourceKey: t.key.ResourceKey,
	})
	if err != nil {
		return nil, fmt.Errorf("select object context: %w", err)
	}

	return rows, nil
}

func contextRowOf(r db.ListObjectContextRow) codec.ContextRow {
	return codec.ContextRow{
		Tool: r.Tool, Source: r.Source, Fields: r.Fields, Warning: r.Warning, ItemID: r.ItemID,
		UpdatedBy: r.UpdatedBy, ItemSeq: r.ItemSeq, UpdatedAtMs: r.UpdatedAtMs,
	}
}

// Entries implements objectcontext.Store. One read transaction serves the
// whole batch, so the entries are one snapshot.
func (s *Store) Entries(ctx context.Context, objects []objectcontext.Ref) ([]objectcontext.Entry, error) {
	if len(objects) == 0 || len(objects) > objectcontext.MaxBatch {
		return nil, fmt.Errorf("%w: batch of %d", objectcontext.ErrInvalidObject, len(objects))
	}

	for _, o := range objects {
		if err := o.Validate(); err != nil {
			return nil, fmt.Errorf("validate object: %w", err)
		}
	}

	var out []objectcontext.Entry

	err := s.inSnapshotTx(ctx, func(q *db.Queries) error {
		for _, ref := range objects {
			es, err := entriesOf(ctx, q, ref)
			if err != nil {
				return err
			}

			out = append(out, es...)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return out, nil
}

// entriesOf returns the visible entries of ref in the transaction q.
func entriesOf(ctx context.Context, q *db.Queries, ref objectcontext.Ref) ([]objectcontext.Entry, error) {
	target, found, err := resolveContext(ctx, q, ref)
	if err != nil || !found {
		return nil, err
	}

	rows, err := contextRows(ctx, q, target)
	if err != nil {
		return nil, err
	}

	out := make([]objectcontext.Entry, 0, len(rows))

	for _, r := range rows {
		e, hidden, err := codec.ContextEntry(ref, contextRowOf(r))
		if err != nil {
			return nil, fmt.Errorf("read context entry: %w", err)
		}

		if !hidden {
			out = append(out, e)
		}
	}

	return out, nil
}

// SetAPI implements objectcontext.Store.
//
//nolint:revive // The signature of the objectcontext.Store port.
func (s *Store) SetAPI(
	ctx context.Context, object objectcontext.Ref, p objectcontext.Patch, by string,
	fence namespace.Fence, e audit.Entry,
) (objectcontext.Entry, error) {
	if err := object.Validate(); err != nil {
		return objectcontext.Entry{}, fmt.Errorf("validate object: %w", err)
	}

	if object.Namespace != fence.Namespace {
		return objectcontext.Entry{}, fmt.Errorf("%w: the object is in another namespace than the fence",
			objectcontext.ErrInvalidObject)
	}

	var result objectcontext.Entry

	err := s.inFencedAuditedTx(ctx, fence, &e, func(q *db.Queries) error {
		target, found, err := resolveContext(ctx, q, object)
		if err != nil {
			return err
		}

		if !found {
			return fmt.Errorf("%w: %s does not exist", objectcontext.ErrInvalidObject, object.Key())
		}

		rows, err := contextRows(ctx, q, target)
		if err != nil {
			return err
		}

		before, output, err := currentAPIAndOutput(object, rows)
		if err != nil {
			return err
		}

		if err = objectcontext.CheckPatch(output, p); err != nil {
			return fmt.Errorf("check patch: %w", err)
		}

		after, err := p.Apply(before)
		if err != nil {
			return fmt.Errorf("apply patch: %w", err)
		}

		e.Changes = objectcontext.ChangesOf(before, after, p.Fields())
		result = objectcontext.Entry{
			UpdatedAt: e.At, Object: object, Source: objectcontext.SourceAPI, Readme: "", Item: "", Warning: "",
			UpdatedBy: by, Fields: after,
		}

		return writeContextEntry(ctx, q, target, &result, 0)
	})
	if err != nil {
		return objectcontext.Entry{}, err
	}

	return result, nil
}

// currentAPIAndOutput returns the fields of the API entry in rows, and the
// output entry in rows, or nil when the object has none.
func currentAPIAndOutput(
	object objectcontext.Ref, rows []db.ListObjectContextRow,
) (objectcontext.Fields, *objectcontext.Entry, error) {
	var (
		api    objectcontext.Fields
		output *objectcontext.Entry
	)

	for _, r := range rows {
		e, _, err := codec.ContextEntry(object, contextRowOf(r))
		if err != nil {
			return objectcontext.Fields{}, nil, fmt.Errorf("read context entry: %w", err)
		}

		if e.Source == objectcontext.SourceAPI {
			api = e.Fields
		} else {
			output = &e
		}
	}

	return api, output, nil
}

// writeContextEntry writes entry for target and replaces its owner rows, in
// the transaction q. seq is the order of the state item of an output entry,
// else 0.
func writeContextEntry(
	ctx context.Context, q *db.Queries, t contextTarget, entry *objectcontext.Entry, seq int64,
) error {
	fields, err := codec.EncodeContextFields(*entry)
	if err != nil {
		return fmt.Errorf("encode fields: %w", err)
	}

	err = q.UpsertObjectContext(ctx, db.UpsertObjectContextParams{
		NamespaceID: t.namespaceID,
		ObjectKind:  t.key.Kind,
		ObjectID:    t.key.ObjectID,
		Tool:        t.key.Tool,
		ResourceKey: t.key.ResourceKey,
		Source:      string(entry.Source),
		Fields:      fields,
		Warning:     entry.Warning,
		ItemID:      entry.Item,
		ItemSeq:     seq,
		UpdatedAtMs: codec.Millis(entry.UpdatedAt),
		UpdatedBy:   entry.UpdatedBy,
	})
	if err != nil {
		return fmt.Errorf("upsert object context: %w", err)
	}

	err = q.DeleteObjectOwners(ctx, db.DeleteObjectOwnersParams{
		NamespaceID: t.namespaceID,
		ObjectKind:  t.key.Kind,
		ObjectID:    t.key.ObjectID,
		ResourceKey: t.key.ResourceKey,
		Source:      string(entry.Source),
	})
	if err != nil {
		return fmt.Errorf("delete object owners: %w", err)
	}

	for _, owner := range objectcontext.OwnerSet(entry.Fields) {
		err = q.InsertObjectOwner(ctx, db.InsertObjectOwnerParams{
			NamespaceID: t.namespaceID,
			Owner:       owner,
			ObjectKind:  t.key.Kind,
			ObjectID:    t.key.ObjectID,
			ResourceKey: t.key.ResourceKey,
			Source:      string(entry.Source),
		})
		if err != nil {
			return fmt.Errorf("insert object owner: %w", err)
		}
	}

	return nil
}

// DeleteAPI implements objectcontext.Store.
func (s *Store) DeleteAPI(
	ctx context.Context, object objectcontext.Ref, fence namespace.Fence, e audit.Entry,
) error {
	if err := object.Validate(); err != nil {
		return fmt.Errorf("validate object: %w", err)
	}

	if object.Namespace != fence.Namespace {
		return fmt.Errorf("%w: the object is in another namespace than the fence", objectcontext.ErrInvalidObject)
	}

	return s.inFencedAuditedTx(ctx, fence, &e, func(q *db.Queries) error {
		target, found, err := resolveContext(ctx, q, object)
		if err != nil {
			return err
		}

		if !found {
			return objectcontext.ErrNotFound
		}

		rows, err := contextRows(ctx, q, target)
		if err != nil {
			return err
		}

		before, _, err := currentAPIAndOutput(object, rows)
		if err != nil {
			return err
		}

		n, err := q.DeleteObjectContext(ctx, db.DeleteObjectContextParams{
			NamespaceID: target.namespaceID,
			ObjectKind:  target.key.Kind,
			ObjectID:    target.key.ObjectID,
			ResourceKey: target.key.ResourceKey,
			Source:      string(objectcontext.SourceAPI),
		})
		if err != nil {
			return fmt.Errorf("delete object context: %w", err)
		}

		if n == 0 {
			return objectcontext.ErrNotFound
		}

		var cleared objectcontext.Fields

		e.Changes = objectcontext.ChangesOf(before, cleared, before.Set())

		return nil
	})
}

// ReplaceOutput implements objectcontext.Store. A unit whose output is empty
// keeps one row with no fields, which holds the order of the item. A reader
// never sees that row. Without it, a late run of an older item would bring
// back an output that a newer item cleared.
func (s *Store) ReplaceOutput(
	ctx context.Context, unit objectcontext.Ref, item objectcontext.OutputItem,
	entries []objectcontext.Entry, fence namespace.Fence,
) error {
	if err := validateOutput(unit, entries); err != nil {
		return err
	}

	if unit.Namespace != fence.Namespace {
		return fmt.Errorf("%w: the object is in another namespace than the fence", objectcontext.ErrInvalidObject)
	}

	return s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		target, found, err := resolveContext(ctx, q, unit)
		if err != nil {
			return err
		}

		if !found {
			return fmt.Errorf("%w: %s does not exist", objectcontext.ErrInvalidObject, unit.Key())
		}

		seq, err := q.GetUnitOutputSeq(ctx, db.GetUnitOutputSeqParams{
			NamespaceID: target.namespaceID,
			ObjectID:    unit.Unit.ID,
		})

		switch {
		case err == nil && seq > item.Seq:
			return nil
		case err != nil && !errors.Is(err, pgx.ErrNoRows):
			return fmt.Errorf("select output order: %w", err)
		}

		return replaceOutputRows(ctx, q, outputReplace{target: target, unit: unit, item: item, entries: entries})
	})
}

// validateOutput checks the unit and the entries of a ReplaceOutput call.
func validateOutput(unit objectcontext.Ref, entries []objectcontext.Entry) error {
	if err := unit.Validate(); err != nil || unit.Kind != objectcontext.KindUnit {
		return fmt.Errorf("%w: %s", objectcontext.ErrInvalidObject, unit.Key())
	}

	for i := range entries {
		e := &entries[i]
		if err := e.Fields.Validate(); err != nil {
			return fmt.Errorf("validate fields: %w", err)
		}

		if err := e.Object.Validate(); err != nil || e.Object.Unit != unit.Unit ||
			e.Source != objectcontext.SourceOutput {
			return fmt.Errorf("%w: entry %s", objectcontext.ErrInvalidObject, e.Object.Key())
		}
	}

	return nil
}

// outputReplace is one replacement of the output rows of a unit: the unit, the
// state item that the entries come from, and the entries.
type outputReplace struct {
	item    objectcontext.OutputItem
	unit    objectcontext.Ref
	target  contextTarget
	entries []objectcontext.Entry
}

// replaceOutputRows replaces the output rows of the unit in the transaction
// q, and keeps the order marker when no entry names the unit itself.
func replaceOutputRows(ctx context.Context, q *db.Queries, rep outputReplace) error {
	t, unit, item, entries := rep.target, rep.unit, rep.item, rep.entries

	err := q.DeleteUnitOutputContext(ctx, db.DeleteUnitOutputContextParams{
		NamespaceID: t.namespaceID,
		ObjectID:    unit.Unit.ID,
	})
	if err != nil {
		return fmt.Errorf("delete output context: %w", err)
	}

	hasUnit := false

	for i := range entries {
		e := entries[i]

		key, keyErr := codec.ContextKeyOf(e.Object, "")
		if keyErr != nil {
			return fmt.Errorf("context key: %w", keyErr)
		}

		hasUnit = hasUnit || e.Object.Kind == objectcontext.KindUnit

		entryTarget := contextTarget{namespaceID: t.namespaceID, key: key}

		if writeErr := writeContextEntry(ctx, q, entryTarget, &e, item.Seq); writeErr != nil {
			return writeErr
		}
	}

	if hasUnit {
		return nil
	}

	var noFields objectcontext.Fields

	marker := objectcontext.Entry{
		UpdatedAt: time.UnixMilli(0), Object: unit, Source: objectcontext.SourceOutput, Readme: "", Item: item.ID,
		Warning: "", UpdatedBy: "", Fields: noFields,
	}

	return writeContextEntry(ctx, q, t, &marker, item.Seq)
}

// DropUnit implements objectcontext.Store.
func (s *Store) DropUnit(ctx context.Context, unit objectcontext.Ref, fence namespace.Fence) error {
	if err := unit.Validate(); err != nil {
		return fmt.Errorf("validate object: %w", err)
	}

	if unit.Namespace != fence.Namespace {
		return fmt.Errorf("%w: the object is in another namespace than the fence", objectcontext.ErrInvalidObject)
	}

	return s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		nsID, err := q.GetNamespaceID(ctx, unit.Namespace.String())
		if err != nil {
			return fmt.Errorf("select namespace ID: %w", err)
		}

		err = q.DeleteUnitContext(ctx, db.DeleteUnitContextParams{NamespaceID: nsID, ObjectID: unit.Unit.ID})
		if err != nil {
			return fmt.Errorf("delete unit context: %w", err)
		}

		return nil
	})
}

// ByOwner implements objectcontext.Store.
func (s *Store) ByOwner(
	ctx context.Context, ns namespace.Name, owner, after string, limit int,
) ([]objectcontext.Entry, string, error) {
	if _, err := objectcontext.ParseOwner(owner); err != nil {
		return nil, "", fmt.Errorf("parse owner: %w", err)
	}

	if limit < 1 || limit > objectcontext.MaxBatch {
		return nil, "", fmt.Errorf("%w: limit %d", objectcontext.ErrInvalidContext, limit)
	}

	cur, err := codec.DecodeContextCursor(after)
	if err != nil {
		return nil, "", fmt.Errorf("decode cursor: %w", err)
	}

	var (
		out  []objectcontext.Entry
		next string
	)

	err = s.inSnapshotTx(ctx, func(q *db.Queries) error {
		var pageErr error

		out, next, pageErr = pageByOwner(ctx, q, ownerPage{ns: ns, owner: owner, cur: cur, limit: limit})

		return pageErr
	})
	if err != nil {
		return nil, "", err
	}

	return out, next, nil
}

// ownerPage names one page of the entries by owner: the namespace, the owner,
// the cursor after which the page starts, and the size of the page.
type ownerPage struct {
	cur   codec.ContextCursor
	ns    namespace.Name
	owner string
	limit int
}

// pageByOwner reads one page of the entries by owner in the transaction q.
func pageByOwner(ctx context.Context, q *db.Queries, pg ownerPage) ([]objectcontext.Entry, string, error) {
	ns, owner, cur, limit := pg.ns, pg.owner, pg.cur, pg.limit

	nsID, err := q.GetNamespaceID(ctx, ns.String())
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", nil
	}

	if err != nil {
		return nil, "", fmt.Errorf("select namespace ID: %w", err)
	}

	rows, err := q.PageObjectContextByOwner(ctx, db.PageObjectContextByOwnerParams{
		NamespaceID: nsID,
		Owner:       owner,
		AfterKind:   cur.Kind,
		AfterID:     cur.ObjectID,
		AfterKey:    cur.ResourceKey,
		AfterSource: cur.Source,
		PageLimit:   int32(limit) + 1, //nolint:gosec // limit is at most MaxBatch.
	})
	if err != nil {
		return nil, "", fmt.Errorf("select entries by owner: %w", err)
	}

	next := ""

	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[limit-1]
		next = codec.EncodeContextCursor(codec.ContextCursor{
			Kind: last.ObjectKind, ObjectID: last.ObjectID, ResourceKey: last.ResourceKey, Source: last.Source,
		})
	}

	out := make([]objectcontext.Entry, 0, len(rows))

	for i := range rows {
		e, ok, err := ownerEntry(ctx, q, ns, &rows[i])
		if err != nil {
			return nil, "", err
		}

		if ok {
			out = append(out, e)
		}
	}

	return out, next, nil
}

// ownerEntry converts a row of the owner page into an entry. ok is false for
// a row of a collection that no longer exists.
func ownerEntry(
	ctx context.Context, q *db.Queries, ns namespace.Name, r *db.PageObjectContextByOwnerRow,
) (objectcontext.Entry, bool, error) {
	var none objectcontext.Entry

	collectionName := ""

	if objectcontext.Kind(r.ObjectKind) == objectcontext.KindCollection {
		name, err := q.GetCollectionNameByID(
			ctx,
			db.GetCollectionNameByIDParams{Namespace: ns.String(), ID: r.ObjectID},
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return none, false, nil
		}

		if err != nil {
			return objectcontext.Entry{}, false, fmt.Errorf("select collection name: %w", err)
		}

		collectionName = name
	}

	ref, err := codec.ContextRef(ns, codec.ContextKey{
		Kind: r.ObjectKind, ObjectID: r.ObjectID, Tool: r.Tool, ResourceKey: r.ResourceKey,
	}, collectionName)
	if err != nil {
		return objectcontext.Entry{}, false, fmt.Errorf("context ref: %w", err)
	}

	e, _, err := codec.ContextEntry(ref, codec.ContextRow{
		Tool: r.Tool, Source: r.Source, Fields: r.Fields, Warning: r.Warning, ItemID: r.ItemID,
		UpdatedBy: r.UpdatedBy, ItemSeq: r.ItemSeq, UpdatedAtMs: r.UpdatedAtMs,
	})
	if err != nil {
		return objectcontext.Entry{}, false, fmt.Errorf("read context entry: %w", err)
	}

	return e, true, nil
}
