package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/registry"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/internal/registrystore"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

// RegistryKeys returns the store of the trusted signing keys of the
// namespaces. Each write checks the installation state and the fence of its
// namespace, and appends its audit entry in its own transaction.
//
//nolint:ireturn // Store-agnostic wiring takes the port.
func (s *Store) RegistryKeys() registry.KeyStore {
	return registryKeys{s: s}
}

// registryKeys is registry.KeyStore over the database of a Store.
type registryKeys struct {
	s *Store
}

var _ registry.KeyStore = registryKeys{s: nil}

// checkRegistryKeyWrite runs the checks that every write of a key makes before
// it opens a transaction: the entry, and the namespace of the fence against
// the namespace of the entry and of the key.
func checkRegistryKeyWrite(ns namespace.Name, fence namespace.Fence, e audit.Entry) error {
	if err := e.Validate(); err != nil {
		return fmt.Errorf("audit entry: %w", err)
	}

	// Defense in depth: the fence check in the transaction compares the namespace
	// of the fence with the namespace row. This check stays so that a key of one
	// namespace cannot be written under the fence of another namespace, whatever
	// the audit entry says. It does not rely on the service.
	if ns != fence.Namespace || e.Namespace != fence.Namespace {
		return fmt.Errorf("%w: key names %q, entry names %q, fence names %q",
			namespace.ErrFenced, ns, e.Namespace, fence.Namespace)
	}

	return nil
}

// AddKey implements registry.KeyStore.
func (k registryKeys) AddKey(ctx context.Context, rec registry.KeyRecord, fence namespace.Fence, e audit.Entry) error {
	if err := rec.Validate(); err != nil {
		return fmt.Errorf("add registry key: %w", err)
	}

	if err := checkRegistryKeyWrite(rec.Namespace, fence, e); err != nil {
		return err
	}

	k.s.mu.RLock()
	defer k.s.mu.RUnlock()

	return k.s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		row, err := q.GetRegistryKey(ctx, db.GetRegistryKeyParams{
			Namespace: rec.Namespace.String(), Fingerprint: rec.Key.Fingerprint,
		})

		switch {
		case err == nil:
			// The same bytes are a repeat: no change and no audit entry.
			if !bytes.Equal([]byte(row.ArmoredKey), rec.Key.Armored) {
				return registry.ErrKeyExists
			}

			return finishCompletion(ctx, q, rec)
		case !errors.Is(err, sql.ErrNoRows):
			return fmt.Errorf("select registry key: %w", err)
		}

		// The write lock of the transaction is held, so the count is exact.
		held, err := q.CountRegistryKeys(ctx, rec.Namespace.String())
		if err != nil {
			return fmt.Errorf("count registry keys: %w", err)
		}

		if held >= registry.MaxKeysPerNamespace {
			return registry.ErrKeyLimit
		}

		_, err = q.InsertRegistryKey(ctx, db.InsertRegistryKeyParams{
			Namespace: rec.Namespace.String(), Fingerprint: rec.Key.Fingerprint, ArmoredKey: string(rec.Key.Armored),
			AddedBy: rec.AddedBy, AddedAtMs: codec.Millis(rec.AddedAt),
		})
		if err != nil {
			return fmt.Errorf("insert registry key: %w", err)
		}

		if _, err = appendAuditEntry(ctx, q, e); err != nil {
			return fmt.Errorf("append audit entry: %w", err)
		}

		return finishCompletion(ctx, q, rec)
	})
}

// RemoveKey implements registry.KeyStore.
func (k registryKeys) RemoveKey(
	ctx context.Context, ns namespace.Name, fingerprint string, fence namespace.Fence, e audit.Entry,
) error {
	if _, err := namespace.ParseName(ns.String()); err != nil {
		return fmt.Errorf("remove registry key: %w: namespace: %w", registry.ErrInvalid, err)
	}

	if err := checkRegistryKeyWrite(ns, fence, e); err != nil {
		return err
	}

	k.s.mu.RLock()
	defer k.s.mu.RUnlock()

	return k.s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		n, err := q.DeleteRegistryKey(ctx, db.DeleteRegistryKeyParams{Namespace: ns.String(), Fingerprint: fingerprint})
		if err != nil {
			return fmt.Errorf("delete registry key: %w", err)
		}

		if n == 0 {
			return registry.ErrKeyNotFound
		}

		if _, err = appendAuditEntry(ctx, q, e); err != nil {
			return fmt.Errorf("append audit entry: %w", err)
		}

		return finishCompletion(ctx, q, fingerprint)
	})
}

// Keys implements registry.KeyStore. A row that fails its checks is left out.
func (k registryKeys) Keys(ctx context.Context, ns namespace.Name) ([]registry.KeyRecord, error) {
	if _, err := namespace.ParseName(ns.String()); err != nil {
		return nil, fmt.Errorf("list registry keys: %w: namespace: %w", registry.ErrInvalid, err)
	}

	k.s.mu.RLock()
	defer k.s.mu.RUnlock()

	rows, err := k.s.queries.ListRegistryKeys(ctx, ns.String())
	if err != nil {
		return nil, fmt.Errorf("select registry keys: %w", err)
	}

	out := make([]registry.KeyRecord, 0, len(rows))

	for _, row := range rows {
		rec, recErr := registrystore.Key(registrystore.KeyRow{
			Namespace: row.Namespace, Fingerprint: row.Fingerprint, ArmoredKey: row.ArmoredKey,
			AddedBy: row.AddedBy, AddedAtMs: row.AddedAtMs,
		})
		// Defense in depth: the query matched the namespace. This check stays so
		// that a change of the query cannot list a key of another namespace.
		if recErr != nil || rec.Namespace != ns {
			continue
		}

		out = append(out, rec)
	}

	return out, nil
}
