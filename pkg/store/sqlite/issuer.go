package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/profile"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

var _ profile.IssuerStore = (*Issuer)(nil)

// Issuer is the store of the signing keys and the settings of the issuer of
// each namespace, on the database of a Store. It implements
// profile.IssuerStore.
type Issuer struct {
	store *Store
}

// Issuer returns the store of the issuer on s.
func (s *Store) Issuer() *Issuer {
	return &Issuer{store: s}
}

// NamespaceByID implements profile.NamespaceResolver.
func (i *Issuer) NamespaceByID(ctx context.Context, id namespace.ID) (namespace.Namespace, error) {
	i.store.mu.RLock()
	defer i.store.mu.RUnlock()

	row, err := i.store.queries.GetNamespaceByID(ctx, id.String())
	if errors.Is(err, sql.ErrNoRows) {
		return namespace.Namespace{}, namespace.ErrNotFound
	}

	if err != nil {
		return namespace.Namespace{}, fmt.Errorf("select namespace by ID: %w", err)
	}

	return codec.FromNamespaceRow(codec.NamespaceRow(row)), nil
}

// issuerKeys reads every key of the namespace with the stable ID nsID.
func issuerKeys(ctx context.Context, q *db.Queries, ns namespace.Name, nsID string) ([]profile.Key, error) {
	rows, err := q.ListIssuerKeys(ctx, nsID)
	if err != nil {
		return nil, fmt.Errorf("select issuer keys: %w", err)
	}

	out := make([]profile.Key, 0, len(rows))
	for n := range rows {
		out = append(out, codec.FromIssuerKeyRow(ns, codec.IssuerKeyRow{
			ID: rows[n].ID, State: rows[n].State, Public: rows[n].PublicKey, Sealed: rows[n].Sealed,
			CreatedAt: rows[n].CreatedAtMs, ActivatedAt: rows[n].ActivatedAtMs, RetiredAt: rows[n].RetiredAtMs,
			DeletedAt: rows[n].DeletedAtMs,
		}))
	}

	return out, nil
}

// Published implements profile.Keys.
func (i *Issuer) Published(ctx context.Context, ns namespace.Name) ([]profile.Key, error) {
	keys, err := i.readKeys(ctx, ns, func(k profile.Key) bool { return k.State.Published() })

	for n := range keys {
		keys[n].Sealed = nil
	}

	return keys, err
}

// Live implements profile.Keys.
func (i *Issuer) Live(ctx context.Context, ns namespace.Name) ([]profile.Key, error) {
	return i.readKeys(ctx, ns, func(k profile.Key) bool { return k.State.Published() })
}

// History implements profile.Keys.
func (i *Issuer) History(ctx context.Context, ns namespace.Name) ([]profile.KeyInfo, error) {
	keys, err := i.readKeys(ctx, ns, func(profile.Key) bool { return true })
	if err != nil {
		return nil, err
	}

	out := make([]profile.KeyInfo, 0, len(keys))
	for n := range keys {
		out = append(out, keys[n].Info())
	}

	return out, nil
}

// Apply implements profile.Keys.
func (i *Issuer) Apply(
	ctx context.Context, ns namespace.Name, ch profile.KeyChange, fence namespace.Fence, e []audit.Entry,
) error {
	if fence.Namespace != ns {
		return fmt.Errorf("%w: the fence names %q, the change names %q", profile.ErrInvalid, fence.Namespace, ns)
	}

	i.store.mu.RLock()
	defer i.store.mu.RUnlock()

	return i.inAuditedTx(ctx, fence, e, func(q *db.Queries) error {
		nsID, err := q.GetNamespaceID(ctx, ns.String())
		if err != nil {
			return fmt.Errorf("select namespace ID: %w", err)
		}

		current, err := issuerKeys(ctx, q, ns, nsID)
		if err != nil {
			return err
		}

		next, err := profile.ApplyKeys(ns, current, ch)
		if err != nil {
			return err
		}

		return writeIssuerKeys(ctx, q, nsID, current, next)
	})
}

// writeIssuerKeys stores the difference between the keys before and after a
// change.
func writeIssuerKeys(ctx context.Context, q *db.Queries, nsID string, before, after []profile.Key) error {
	w := codec.DiffIssuerKeys(before, after)

	for i := range w.Moved {
		k := &w.Moved[i]
		r := codec.ToIssuerKeyRow(*k)

		n, err := q.UpdateIssuerKey(ctx, db.UpdateIssuerKeyParams{
			State: r.State, Sealed: codec.NonNilBytes(r.Sealed), ActivatedAtMs: r.ActivatedAt, RetiredAtMs: r.RetiredAt,
			DeletedAtMs: r.DeletedAt, NamespaceID: nsID, ID: r.ID, FromState: string(w.From[k.ID]),
		})
		if err != nil {
			return fmt.Errorf("update issuer key: %w", err)
		}

		if n == 0 {
			return fmt.Errorf("%w: key %s changed", profile.ErrConflict, k.ID)
		}
	}

	for i := range w.Added {
		k := &w.Added[i]
		r := codec.ToIssuerKeyRow(*k)

		err := q.InsertIssuerKey(ctx, db.InsertIssuerKeyParams{
			NamespaceID: nsID, ID: r.ID, State: r.State, PublicKey: r.Public, Sealed: codec.NonNilBytes(r.Sealed),
			CreatedAtMs: r.CreatedAt, ActivatedAtMs: r.ActivatedAt, RetiredAtMs: r.RetiredAt, DeletedAtMs: r.DeletedAt,
		})
		if isUniqueViolation(err) {
			return fmt.Errorf("%w: the namespace already has key %s or a key of the state %s", profile.ErrConflict,
				k.ID, k.State)
		}

		if err != nil {
			return fmt.Errorf("insert issuer key: %w", err)
		}
	}

	return nil
}

// GetIssuerSettings implements profile.IssuerSettingsStore.
func (i *Issuer) GetIssuerSettings(ctx context.Context, ns namespace.Name) (profile.IssuerSettings, error) {
	i.store.mu.RLock()
	defer i.store.mu.RUnlock()

	nsID, err := i.store.queries.GetNamespaceID(ctx, ns.String())
	if errors.Is(err, sql.ErrNoRows) {
		return profile.IssuerSettings{}, namespace.ErrNotFound
	}

	if err != nil {
		return profile.IssuerSettings{}, fmt.Errorf("select namespace ID: %w", err)
	}

	row, err := i.store.queries.GetIssuerSettings(ctx, nsID)
	if errors.Is(err, sql.ErrNoRows) {
		return profile.DefaultIssuerSettings(ns), nil
	}

	if err != nil {
		return profile.IssuerSettings{}, fmt.Errorf("select issuer settings: %w", err)
	}

	template, err := codec.DecodeSubjectTemplate(row.SubjectTemplate)
	if err != nil {
		return profile.IssuerSettings{}, err
	}

	check, err := codec.DecodeCopyCheck(row.CopyCheck)
	if err != nil {
		return profile.IssuerSettings{}, err
	}

	return profile.IssuerSettings{
		UpdatedAt: codec.FromIssuerMillis(row.UpdatedAtMs), CopyCheck: check, Namespace: ns, IssuerURL: row.IssuerUrl,
		UpdatedBy: row.UpdatedBy, SubjectTemplate: template, Version: row.Version,
		AllowBlockSubjects: row.AllowBlockSubjects == 1, IncludeActorName: row.IncludeActorName == 1,
	}, nil
}

// PutIssuerSettings implements profile.IssuerSettingsStore.
func (i *Issuer) PutIssuerSettings(
	ctx context.Context, s profile.IssuerSettings, ifVersion int64, fence namespace.Fence, e []audit.Entry,
) (profile.IssuerSettings, error) {
	if fence.Namespace != s.Namespace || ifVersion < 0 {
		return profile.IssuerSettings{}, fmt.Errorf("%w: the settings name another namespace than the fence",
			profile.ErrInvalid)
	}

	if err := profile.CheckIssuerURL(s.IssuerURL); err != nil {
		return profile.IssuerSettings{}, err
	}

	template, err := profile.CanonicalSubjectTemplate(s.SubjectTemplate)
	if err != nil {
		return profile.IssuerSettings{}, err
	}

	encoded, err := codec.EncodeSubjectTemplate(template)
	if err != nil {
		return profile.IssuerSettings{}, err
	}

	i.store.mu.RLock()
	defer i.store.mu.RUnlock()

	err = i.inAuditedTx(ctx, fence, e, func(q *db.Queries) error {
		nsID, idErr := q.GetNamespaceID(ctx, s.Namespace.String())
		if idErr != nil {
			return fmt.Errorf("select namespace ID: %w", idErr)
		}

		n, upErr := q.UpsertIssuerSettings(ctx, db.UpsertIssuerSettingsParams{
			NamespaceID:        nsID,
			IssuerUrl:          s.IssuerURL,
			SubjectTemplate:    encoded,
			AllowBlockSubjects: codec.Flag(s.AllowBlockSubjects),
			IncludeActorName:   codec.Flag(s.IncludeActorName),
			NewVersion:         ifVersion + 1,
			UpdatedAtMs:        codec.IssuerMillis(s.UpdatedAt),
			UpdatedBy:          s.UpdatedBy,
		})
		if upErr != nil {
			return fmt.Errorf("store issuer settings: %w", upErr)
		}

		if n == 0 {
			return fmt.Errorf("%w: the issuer settings changed since version %d", profile.ErrConflict, ifVersion)
		}

		return nil
	})
	if err != nil {
		return profile.IssuerSettings{}, err
	}

	s.SubjectTemplate = template
	s.Version = ifVersion + 1

	// The copy check stays as stored: a read gives the caller its current form.
	stored, err := i.GetIssuerSettings(ctx, s.Namespace)
	if err != nil {
		return profile.IssuerSettings{}, err
	}

	s.CopyCheck = stored.CopyCheck

	return s, nil
}

// RecordCopyCheck implements profile.IssuerSettingsStore.
func (i *Issuer) RecordCopyCheck(
	ctx context.Context, ns namespace.Name, c profile.CopyCheck, fence namespace.Fence, e []audit.Entry,
) error {
	if fence.Namespace != ns {
		return fmt.Errorf("%w: the fence names %q, the copy check names %q", profile.ErrInvalid, fence.Namespace, ns)
	}

	template, err := codec.EncodeSubjectTemplate(profile.DefaultSubjectTemplate())
	if err != nil {
		return err
	}

	i.store.mu.RLock()
	defer i.store.mu.RUnlock()

	return i.inAuditedTx(ctx, fence, e, func(q *db.Queries) error {
		nsID, idErr := q.GetNamespaceID(ctx, ns.String())
		if idErr != nil {
			return fmt.Errorf("select namespace ID: %w", idErr)
		}

		keys, keyErr := issuerKeys(ctx, q, ns, nsID)
		if keyErr != nil {
			return keyErr
		}

		// Only a key that the namespace still has can be verified.
		c.Verified = slices.DeleteFunc(slices.Clone(c.Verified), func(id profile.KeyID) bool {
			return !slices.ContainsFunc(keys, func(k profile.Key) bool {
				return k.ID == id && k.State != profile.KeyDeleted
			})
		})

		raw, encErr := codec.EncodeCopyCheck(&c)
		if encErr != nil {
			return encErr
		}

		upErr := q.UpsertIssuerCopyCheck(ctx, db.UpsertIssuerCopyCheckParams{
			NamespaceID: nsID, DefaultTemplate: template, CopyCheck: raw,
		})
		if upErr != nil {
			return fmt.Errorf("store copy check: %w", upErr)
		}

		return nil
	})
}

// inAuditedTx runs fn and appends each entry of es in the same fenced
// transaction. It checks every entry before the transaction starts.
func (i *Issuer) inAuditedTx(
	ctx context.Context, fence namespace.Fence, es []audit.Entry, fn func(q *db.Queries) error,
) error {
	if len(es) == 0 {
		return fmt.Errorf("%w: a write needs at least one audit entry", profile.ErrInvalid)
	}

	for n := range es {
		if err := es[n].Validate(); err != nil {
			return fmt.Errorf("audit entry: %w", err)
		}

		if es[n].Namespace != fence.Namespace {
			return fmt.Errorf("%w: entry names %q, fence names %q",
				audit.ErrInvalidEntry, es[n].Namespace, fence.Namespace)
		}
	}

	// The store appends the last entry after fn, so fn appends the others.
	last := es[len(es)-1]

	return i.store.inFencedAuditedTx(ctx, fence, &last, func(q *db.Queries) error {
		if err := fn(q); err != nil {
			return err
		}

		for n := range len(es) - 1 {
			if _, err := appendAuditEntry(ctx, q, es[n]); err != nil {
				return fmt.Errorf("append audit entry: %w", err)
			}
		}

		return nil
	})
}

// readKeys reads the keys of ns that keep reads, outside a write.
func (i *Issuer) readKeys(ctx context.Context, ns namespace.Name, keep func(profile.Key) bool) ([]profile.Key, error) {
	i.store.mu.RLock()
	defer i.store.mu.RUnlock()

	nsID, err := i.store.queries.GetNamespaceID(ctx, ns.String())
	if errors.Is(err, sql.ErrNoRows) {
		return []profile.Key{}, nil
	}

	if err != nil {
		return nil, fmt.Errorf("select namespace ID: %w", err)
	}

	all, err := issuerKeys(ctx, i.store.queries, ns, nsID)
	if err != nil {
		return nil, err
	}

	return slices.DeleteFunc(all, func(k profile.Key) bool { return !keep(k) }), nil
}
