package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/registry"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/internal/registrystore"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

// Registry returns the store of registry releases. Each write checks the
// installation state and the fence of its namespace, and appends its audit
// entry in its own transaction.
//
//nolint:ireturn // Store-agnostic wiring takes the port.
func (s *Store) Registry() registry.Store {
	return registryStore{s: s}
}

// registryStore is registry.Store over the database of a Store.
type registryStore struct {
	s *Store
}

var _ registry.Store = registryStore{s: nil}

// Publish implements registry.Store.
func (r registryStore) Publish(ctx context.Context, rel registry.Release, fence namespace.Fence, e audit.Entry) error {
	if err := rel.Validate(); err != nil {
		return fmt.Errorf("publish registry release: %w", err)
	}

	// Defense in depth: the fence check below compares the namespace of the
	// fence with the namespace row. This check stays so that a release of one
	// namespace cannot be written under the fence of another namespace, whatever
	// the audit entry says. It does not rely on the service.
	if rel.Address.Namespace != fence.Namespace {
		return fmt.Errorf("%w: release names %q, fence names %q",
			namespace.ErrFenced, rel.Address.Namespace, fence.Namespace)
	}

	archives, err := registrystore.EncodeArchives(rel.Archives)
	if err != nil {
		return err
	}

	storedFiles, err := registrystore.EncodeFiles(rel.Files)
	if err != nil {
		return err
	}

	// The entry is checked first, as the audited transactions of the other
	// stores do, so that an invalid entry answers the same error whatever the
	// fence or the content says.
	if err = e.Validate(); err != nil {
		return fmt.Errorf("audit entry: %w", err)
	}

	if e.Namespace != fence.Namespace {
		return fmt.Errorf("%w: entry names %q, fence names %q", audit.ErrInvalidEntry, e.Namespace, fence.Namespace)
	}

	return r.s.inFencedUpdateTx(ctx, fence, func(q *db.Queries) error {
		result, repeat, pubErr := publishRegistryRelease(ctx, q, rel, archives, storedFiles)
		if pubErr != nil {
			return pubErr
		}

		// A repeat of a published version changes nothing and writes no audit
		// entry. It still finishes the idempotency record of its request, with
		// the stored release: the first publisher and time, as the live answer
		// of the repeat shows them.
		if !repeat {
			if _, auditErr := appendAuditEntry(ctx, q, e); auditErr != nil {
				return fmt.Errorf("append audit entry: %w", auditErr)
			}
		}

		return finishCompletion(ctx, q, result)
	})
}

// publishRegistryRelease writes rel in the open transaction. The transaction
// holds the namespace row FOR UPDATE, so no other publish in the namespace
// runs between the read and the insert.
// It returns the release that the store holds after the write: rel for a new
// release, or the stored release and true for a repeat of the same content.
func publishRegistryRelease(
	ctx context.Context, q *db.Queries, rel registry.Release, archives, storedFiles string,
) (registry.Release, bool, error) {
	var none registry.Release

	// The key check is in the transaction of the write. Defense in depth: the
	// service checks the key before it copies the files. This check stays so
	// that a key removed after that check cannot sign a new release, and so that
	// a new caller of the store cannot publish a release of an untrusted key.
	// A release of a module has no signature and no key.
	if rel.KeyFingerprint != "" {
		_, keyErr := q.GetRegistryKey(ctx, db.GetRegistryKeyParams{
			Namespace: rel.Address.Namespace.String(), Fingerprint: rel.KeyFingerprint,
		})
		if errors.Is(keyErr, sql.ErrNoRows) {
			return none, false, registry.ErrKeyNotFound
		}

		if keyErr != nil {
			return none, false, fmt.Errorf("select registry key: %w", keyErr)
		}
	}

	row, err := q.GetRegistryRelease(ctx, db.GetRegistryReleaseParams{
		Namespace: rel.Address.Namespace.String(), Kind: string(rel.Address.Kind), Name: rel.Address.Name,
		System: rel.Address.System, Version: rel.Version,
	})

	switch {
	case err == nil:
		stored, relErr := registrystore.Release(registrystore.Row{
			Namespace: row.Namespace, Kind: row.Kind, Name: row.Name, System: row.System, Version: row.Version,
			SumsFile: row.SumsFile, SumsSHA256: row.SumsSha256, SignatureFile: row.SignatureFile,
			SignatureSHA256: row.SignatureSha256, KeyFingerprint: row.KeyFingerprint, Protocols: row.Protocols,
			PublishedBy: row.PublishedBy, ArchivesJSON: row.ArchivesJson, PublishedAtMs: row.PublishedAtMs,
			StoredFilesJSON: row.StoredFilesJson,
		})
		// A stored row that fails its checks is never the same content: the
		// publish does not claim it.
		if relErr == nil && stored.SameContent(rel) {
			return stored, true, nil
		}

		return none, false, registry.ErrVersionExists
	case !errors.Is(err, sql.ErrNoRows):
		return none, false, fmt.Errorf("select registry release: %w", err)
	}

	n, err := q.InsertRegistryRelease(ctx, db.InsertRegistryReleaseParams{
		Namespace:       rel.Address.Namespace.String(),
		Kind:            string(rel.Address.Kind),
		Name:            rel.Address.Name,
		System:          rel.Address.System,
		Version:         rel.Version,
		SumsFile:        rel.SumsFile,
		SumsSha256:      rel.SumsSHA256,
		SignatureFile:   rel.SignatureFile,
		SignatureSha256: rel.SignatureSHA256,
		KeyFingerprint:  rel.KeyFingerprint,
		Protocols: registrystore.JoinProtocols(
			rel.Protocols,
		),
		PublishedBy:     rel.PublishedBy,
		ArchivesJson:    archives,
		PublishedAtMs:   codec.Millis(rel.PublishedAt),
		StoredFilesJson: storedFiles,
	})
	if err != nil {
		return none, false, fmt.Errorf("insert registry release: %w", err)
	}

	if n == 0 {
		return none, false, registry.ErrVersionExists
	}

	return rel, false, nil
}

// Get implements registry.Store.
func (r registryStore) Get(ctx context.Context, addr registry.Address, version string) (registry.Release, error) {
	if err := addr.Validate(); err != nil {
		return registry.Release{}, fmt.Errorf("get registry release: %w", err)
	}

	if err := registry.ValidateVersion(version); err != nil {
		return registry.Release{}, fmt.Errorf("get registry release: %w", err)
	}

	row, err := r.s.queries.GetRegistryRelease(ctx, db.GetRegistryReleaseParams{
		Namespace: addr.Namespace.String(), Kind: string(addr.Kind), Name: addr.Name, System: addr.System,
		Version: version,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return registry.Release{}, registry.ErrNotFound
	}

	if err != nil {
		return registry.Release{}, fmt.Errorf("select registry release: %w", err)
	}

	rel, err := registrystore.Release(registryRow(row))
	if err != nil {
		// A row that fails its checks is not served, and it reads as no release.
		return registry.Release{}, registry.ErrNotFound
	}

	return rel, nil
}

// registryRow converts the generated row of a release.
func registryRow(row db.GetRegistryReleaseRow) registrystore.Row {
	return registrystore.Row{
		Namespace: row.Namespace, Kind: row.Kind, Name: row.Name, System: row.System, Version: row.Version,
		SumsFile: row.SumsFile, SumsSHA256: row.SumsSha256, SignatureFile: row.SignatureFile,
		SignatureSHA256: row.SignatureSha256, KeyFingerprint: row.KeyFingerprint, Protocols: row.Protocols,
		PublishedBy: row.PublishedBy, ArchivesJSON: row.ArchivesJson, PublishedAtMs: row.PublishedAtMs,
		StoredFilesJSON: row.StoredFilesJson,
	}
}

// List implements registry.Store. A row that fails its checks is left out.
func (r registryStore) List(ctx context.Context, addr registry.Address) ([]registry.Release, error) {
	if err := addr.Validate(); err != nil {
		return nil, fmt.Errorf("list registry releases: %w", err)
	}

	rows, err := r.s.queries.ListRegistryReleases(ctx, db.ListRegistryReleasesParams{
		Namespace: addr.Namespace.String(), Kind: string(addr.Kind), Name: addr.Name, System: addr.System,
	})
	if err != nil {
		return nil, fmt.Errorf("select registry releases: %w", err)
	}

	out := make([]registry.Release, 0, len(rows))

	for i := range rows {
		rel, relErr := registrystore.Release(registryRow(db.GetRegistryReleaseRow(rows[i])))
		// Defense in depth: the query matched the address. This check stays so
		// that a change of the query cannot list a release of another address.
		if relErr != nil || rel.Address != addr {
			continue
		}

		out = append(out, rel)
	}

	slices.SortStableFunc(out, func(a, b registry.Release) int {
		return registry.CompareVersions(a.Version, b.Version)
	})

	return out, nil
}

// Addresses implements registry.Store. A row that fails its checks is left out.
func (r registryStore) Addresses(
	ctx context.Context, ns namespace.Name, kind registry.Kind, after string, limit int,
) ([]registry.Address, string, error) {
	probe := registry.Address{Namespace: ns, Kind: kind, Name: "probe", System: probeSystem(kind)}
	if err := probe.Validate(); err != nil {
		return nil, "", fmt.Errorf("list registry addresses: %w", err)
	}

	if limit < 1 || limit > registry.MaxAddressPage {
		return nil, "", fmt.Errorf("%w: limit %d", registry.ErrInvalid, limit)
	}

	// Defense in depth: the callers check the cursor too; the store refuses a
	// cursor that does not hold an address of the kind.
	cursor, err := registry.ParseAddressKey(ns, kind, after)
	if err != nil {
		return nil, "", fmt.Errorf("list registry addresses: %w", err)
	}

	// The query reads one row more than the page, so that the extra row tells
	// whether a next page exists.
	rows, err := r.s.queries.ListRegistryAddresses(ctx, db.ListRegistryAddressesParams{
		Namespace: ns.String(), Kind: string(kind), AfterName: cursor.Name, AfterSystem: cursor.System,
		Lim: int32(limit) + 1,
	})
	if err != nil {
		return nil, "", fmt.Errorf("select registry addresses: %w", err)
	}

	next := ""

	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[limit-1]
		next = registry.AddressKey(registry.Address{
			Namespace: ns, Kind: kind, Name: last.Name, System: last.System,
		})
	}

	out := make([]registry.Address, 0, len(rows))

	for _, row := range rows {
		addr, addrErr := registrystore.Address(ns.String(), string(kind), row.Name, row.System)
		if addrErr != nil {
			continue
		}

		out = append(out, addr)
	}

	return out, next, nil
}

// probeSystem returns a valid system for kind, so that Addresses can check the
// namespace and the kind with the validation of an address.
func probeSystem(kind registry.Kind) string {
	if kind == registry.KindModule {
		return "probe"
	}

	return ""
}
