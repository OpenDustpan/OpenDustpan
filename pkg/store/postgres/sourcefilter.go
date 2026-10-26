package postgres

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/internal/source"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

// SourceFilters returns the store of the source filter of each namespace. The
// filter is a column of the namespace row. Each change checks the fence of its
// namespace and appends its audit entry in one transaction.
func (s *Store) SourceFilters() SourceFilterStore {
	return SourceFilterStore{s: s}
}

// SourceFilterStore is source.Filters over the database of a Store.
type SourceFilterStore struct {
	s *Store
}

var _ source.Filters = SourceFilterStore{s: nil}

// Filter implements source.Filters. A stored value that source.DecodeFilter
// refuses reads as source.ErrInvalid, and the error does not hold the value.
func (f SourceFilterStore) Filter(ctx context.Context, ns namespace.Name) (source.Filter, error) {
	var none source.Filter

	if err := validFilterNamespace(ns); err != nil {
		return none, err
	}

	raw, err := f.s.queries.GetNamespaceSourceFilter(ctx, ns.String())
	if errors.Is(err, pgx.ErrNoRows) {
		return none, namespace.ErrNotFound
	}

	if err != nil {
		return none, fmt.Errorf("select source filter: %w", err)
	}

	// Defense in depth: the CHECK of the column refuses a text that is not
	// JSON, and SetFilter validates each value. This check stays so that a row
	// of another version or a restore never reads as a value.
	filter, err := source.DecodeFilter(raw)
	if err != nil {
		return none, fmt.Errorf("%w: the stored source filter of %s", source.ErrInvalid, ns)
	}

	return filter, nil
}

// SetFilter implements source.Filters. A valid filter replaces a stored value
// that source.DecodeFilter refuses, so an admin can repair the row through the
// API.
func (f SourceFilterStore) SetFilter(
	ctx context.Context, ns namespace.Name, filter source.Filter, fence namespace.Fence, e audit.Entry,
) error {
	if err := validFilterNamespace(ns); err != nil {
		return err
	}

	// Defense in depth: the service validates the filter too, and the CHECK of
	// the column refuses a text that is not JSON. EncodeFilter validates it
	// again. This check stays so that a new caller cannot store a value that
	// the readers refuse.
	text, err := source.EncodeFilter(filter)
	if err != nil {
		return fmt.Errorf("set source filter: %w", err)
	}

	// Defense in depth: the transaction below checks the same binding again
	// and the fence check of the transaction compares the failover version of
	// the namespace of the fence. This check stays so that a refusal needs no
	// transaction. It does not rely on the caller, the service or the
	// transaction.
	if err := settingFenceBinds(ns, fence); err != nil {
		return err
	}

	return f.writeFilter(ctx, ns, text, fence, e)
}

// writeFilter writes text as the source filter of ns and appends e in one
// fenced transaction. The transaction reads the value that the write replaces
// and records it as the Old value of the one change of e, so a concurrent
// change cannot make the entry name a stale value.
func (f SourceFilterStore) writeFilter(
	ctx context.Context, ns namespace.Name, text string, fence namespace.Fence, e audit.Entry,
) error {
	// The entry is a copy, but its changes share the array of the caller.
	e.Changes = slices.Clone(e.Changes)

	return f.s.inFencedAuditedTx(ctx, fence, &e, func(q *db.Queries) error {
		// Defense in depth: the pre-check of SetFilter makes the same
		// comparison, and the fence check of the transaction compares the
		// failover version only. This check stays so that the UPDATE below
		// cannot write the filter of ns under the fence of another namespace,
		// also when a new caller skips the pre-check.
		if err := settingFenceBinds(ns, fence); err != nil {
			return err
		}

		old, err := q.GetNamespaceSourceFilter(ctx, ns.String())
		if errors.Is(err, pgx.ErrNoRows) {
			return namespace.ErrNotFound
		}

		if err != nil {
			return fmt.Errorf("select source filter: %w", err)
		}

		// A stored value that DecodeFilter refuses, as a row of another
		// version or a restore can hold, is the one case that a valid write
		// repairs. The entry records the raw value, marked invalid. Reads still
		// refuse it.
		if len(e.Changes) == 1 {
			shown, shownErr := shownSourceFilter(old)
			if shownErr != nil {
				return shownErr
			}

			e.Changes[0].Old = &shown
		}

		n, err := q.SetNamespaceSourceFilter(ctx, db.SetNamespaceSourceFilterParams{
			SourceFilter: text,
			Name:         ns.String(),
		})
		if err != nil {
			return fmt.Errorf("update source filter: %w", err)
		}

		if n == 0 {
			return namespace.ErrNotFound
		}

		return nil
	})
}

// shownSourceFilter returns the text that the audit entry shows for the
// stored value raw: the canonical text of the filter that raw holds, or the
// quoted raw value with the invalid mark when DecodeFilter refuses it.
func shownSourceFilter(raw string) (string, error) {
	old, decodeErr := source.DecodeFilter(raw)
	if decodeErr == nil {
		shown, err := source.EncodeFilter(old)
		if err != nil {
			return "", fmt.Errorf("encode the replaced source filter: %w", err)
		}

		return shown, nil
	}

	return invalidOldPrefix + strconv.Quote(raw), nil
}

// validFilterNamespace returns an error that wraps source.ErrInvalid when ns
// is not a valid namespace name.
func validFilterNamespace(ns namespace.Name) error {
	// Defense in depth: the handler resolves the namespace of the route, and
	// the query uses a placeholder. This check stays so that a new caller
	// cannot pass a name that no namespace can have.
	if _, err := namespace.ParseName(ns.String()); err != nil {
		return fmt.Errorf("%w: namespace name", source.ErrInvalid)
	}

	return nil
}
