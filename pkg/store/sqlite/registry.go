package sqlite

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
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

// errRegistryRepeat ends the transaction of a publish of a version that exists
// with the same content. The transaction changes nothing and writes no audit
// entry, and Publish returns nil.
var errRegistryRepeat = errors.New("registry release repeat")

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

	r.s.mu.RLock()
	defer r.s.mu.RUnlock()

	err = r.s.inFencedAuditedTx(ctx, fence, &e, func(q *db.Queries) error {
		return publishRegistryRelease(ctx, q, rel, archives)
	})
	if errors.Is(err, errRegistryRepeat) {
		return nil
	}

	return err
}

// publishRegistryRelease writes rel in the open transaction. The write lock of
// the transaction is held, so no other publish runs between the read and the
// insert.
func publishRegistryRelease(ctx context.Context, q *db.Queries, rel registry.Release, archives string) error {
	row, err := q.GetRegistryRelease(ctx, db.GetRegistryReleaseParams{
		Namespace: rel.Address.Namespace.String(), Kind: string(rel.Address.Kind), Name: rel.Address.Name,
		System: rel.Address.System, Version: rel.Version,
	})

	switch {
	case err == nil:
		stored, relErr := registrystore.Release(registrystore.Row{
			Namespace: row.Namespace, Kind: row.Kind, Name: row.Name, System: row.System, Version: row.Version,
			SumsFile: row.SumsFile, SumsSHA256: row.SumsSha256, SignatureFile: row.SignatureFile,
			SignatureSHA256: row.SignatureSha256, KeyFingerprint: row.KeyFingerprint, PublishedBy: row.PublishedBy,
			ArchivesJSON: row.ArchivesJson, PublishedAtMs: row.PublishedAtMs,
		})
		// A stored row that fails its checks is never the same content: the
		// publish does not claim it.
		if relErr == nil && stored.SameContent(rel) {
			return errRegistryRepeat
		}

		return registry.ErrVersionExists
	case !errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("select registry release: %w", err)
	}

	n, err := q.InsertRegistryRelease(ctx, db.InsertRegistryReleaseParams{
		Namespace: rel.Address.Namespace.String(), Kind: string(rel.Address.Kind), Name: rel.Address.Name,
		System: rel.Address.System, Version: rel.Version, SumsFile: rel.SumsFile, SumsSha256: rel.SumsSHA256,
		SignatureFile: rel.SignatureFile, SignatureSha256: rel.SignatureSHA256, KeyFingerprint: rel.KeyFingerprint,
		PublishedBy: rel.PublishedBy, ArchivesJson: archives, PublishedAtMs: codec.Millis(rel.PublishedAt),
	})
	if err != nil {
		return fmt.Errorf("insert registry release: %w", err)
	}

	if n == 0 {
		return registry.ErrVersionExists
	}

	return nil
}

// Get implements registry.Store.
func (r registryStore) Get(ctx context.Context, addr registry.Address, version string) (registry.Release, error) {
	if err := addr.Validate(); err != nil {
		return registry.Release{}, fmt.Errorf("get registry release: %w", err)
	}

	if err := registry.ValidateVersion(version); err != nil {
		return registry.Release{}, fmt.Errorf("get registry release: %w", err)
	}

	r.s.mu.RLock()
	defer r.s.mu.RUnlock()

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
		SignatureSHA256: row.SignatureSha256, KeyFingerprint: row.KeyFingerprint, PublishedBy: row.PublishedBy,
		ArchivesJSON: row.ArchivesJson, PublishedAtMs: row.PublishedAtMs,
	}
}

// List implements registry.Store. A row that fails its checks is left out.
func (r registryStore) List(ctx context.Context, addr registry.Address) ([]registry.Release, error) {
	if err := addr.Validate(); err != nil {
		return nil, fmt.Errorf("list registry releases: %w", err)
	}

	r.s.mu.RLock()
	defer r.s.mu.RUnlock()

	rows, err := r.s.queries.ListRegistryReleases(ctx, db.ListRegistryReleasesParams{
		Namespace: addr.Namespace.String(), Kind: string(addr.Kind), Name: addr.Name, System: addr.System,
	})
	if err != nil {
		return nil, fmt.Errorf("select registry releases: %w", err)
	}

	out := make([]registry.Release, 0, len(rows))

	for _, row := range rows {
		rel, relErr := registrystore.Release(registryRow(db.GetRegistryReleaseRow(row)))
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
	ctx context.Context, ns namespace.Name, kind registry.Kind,
) ([]registry.Address, error) {
	probe := registry.Address{Namespace: ns, Kind: kind, Name: "probe", System: probeSystem(kind)}
	if err := probe.Validate(); err != nil {
		return nil, fmt.Errorf("list registry addresses: %w", err)
	}

	r.s.mu.RLock()
	defer r.s.mu.RUnlock()

	rows, err := r.s.queries.ListRegistryAddresses(ctx, db.ListRegistryAddressesParams{
		Namespace: ns.String(), Kind: string(kind),
	})
	if err != nil {
		return nil, fmt.Errorf("select registry addresses: %w", err)
	}

	out := make([]registry.Address, 0, len(rows))

	for _, row := range rows {
		addr, addrErr := registrystore.Address(ns.String(), string(kind), row.Name, row.System)
		if addrErr != nil {
			continue
		}

		out = append(out, addr)
	}

	return out, nil
}

// probeSystem returns a valid system for kind, so that Addresses can check the
// namespace and the kind with the validation of an address.
func probeSystem(kind registry.Kind) string {
	if kind == registry.KindModule {
		return "probe"
	}

	return ""
}
