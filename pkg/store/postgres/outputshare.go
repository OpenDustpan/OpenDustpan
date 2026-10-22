package postgres

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/outputshare"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

// OutputSharingSettings returns the store of the output sharing setting of
// each namespace. The setting is a column of the namespace row. Each change
// checks the fence of its namespace and appends its audit entry in one
// transaction.
//
//nolint:ireturn // Store-agnostic wiring takes the port.
func (s *Store) OutputSharingSettings() outputshare.Settings {
	return outputSharingSettings{s: s}
}

// outputSharingSettings is outputshare.Settings over the database of a Store.
type outputSharingSettings struct {
	s *Store
}

var _ outputshare.Settings = outputSharingSettings{s: nil}

// Setting implements outputshare.Settings. A stored value that fails
// Default.Validate reads as ErrInvalid, and the error does not hold the
// value.
func (o outputSharingSettings) Setting(ctx context.Context, ns namespace.Name) (outputshare.Default, error) {
	if err := validSettingNamespace(ns); err != nil {
		return "", err
	}

	raw, err := o.s.queries.GetNamespaceOutputSharingDefault(ctx, ns.String())
	if errors.Is(err, pgx.ErrNoRows) {
		return "", namespace.ErrNotFound
	}

	if err != nil {
		return "", fmt.Errorf("select output sharing setting: %w", err)
	}

	// Defense in depth: the CHECK of the column refuses another value, and
	// SetSetting validates each value. This check stays so that a row of
	// another version or a restore never reads as a value.
	d := outputshare.Default(raw)
	if d.Validate() != nil {
		return "", fmt.Errorf("%w: the stored output sharing setting of %s", outputshare.ErrInvalid, ns)
	}

	return d, nil
}

// SetSetting implements outputshare.Settings.
func (o outputSharingSettings) SetSetting(
	ctx context.Context, ns namespace.Name, d outputshare.Default, fence namespace.Fence, e audit.Entry,
) error {
	if err := validSettingNamespace(ns); err != nil {
		return err
	}

	// Defense in depth: the service validates the value too, and the CHECK of
	// the column refuses another value. This check stays so that a new caller
	// cannot store a value that the readers refuse.
	if err := d.Validate(); err != nil {
		return fmt.Errorf("set output sharing setting: %w", err)
	}

	// Defense in depth: the transaction below checks the same binding again
	// and the fence check of the transaction compares the failover version of
	// the namespace of the fence. This check stays so that a refusal needs no
	// transaction. It does not rely on the caller, the service or the
	// transaction.
	if err := settingFenceBinds(ns, fence); err != nil {
		return err
	}

	return o.writeSetting(ctx, ns, d, fence, e)
}

// settingFenceBinds returns namespace.ErrFenced unless the fence names ns. The
// fence check of the transaction compares the version of the namespace row of
// the fence, so only this comparison stops the setting of one namespace from
// being written under a valid fence of another namespace.
func settingFenceBinds(ns namespace.Name, fence namespace.Fence) error {
	if fence.Namespace != ns {
		return fmt.Errorf("%w: setting names %q, fence names %q", namespace.ErrFenced, ns, fence.Namespace)
	}

	return nil
}

// writeSetting writes d as the setting of ns and appends e in one fenced
// transaction. The transaction reads the value that the write replaces and
// records it as the Old value of the one change of e, so a concurrent change
// cannot make the entry name a stale value.
func (o outputSharingSettings) writeSetting(
	ctx context.Context, ns namespace.Name, d outputshare.Default, fence namespace.Fence, e audit.Entry,
) error {
	// The entry is a copy, but its changes share the array of the caller.
	e.Changes = slices.Clone(e.Changes)

	return o.s.inFencedAuditedTx(ctx, fence, &e, func(q *db.Queries) error {
		// Defense in depth: the pre-check of SetSetting makes the same
		// comparison, and the fence check of the transaction compares the
		// failover version only. This check stays so that the UPDATE below
		// cannot write the setting of ns under the fence of another
		// namespace, also when a new caller skips the pre-check.
		if err := settingFenceBinds(ns, fence); err != nil {
			return err
		}

		old, err := q.GetNamespaceOutputSharingDefault(ctx, ns.String())
		if errors.Is(err, pgx.ErrNoRows) {
			return namespace.ErrNotFound
		}

		if err != nil {
			return fmt.Errorf("select output sharing setting: %w", err)
		}

		// A stored value of another version or a restore never reaches the
		// audit entry, and a change over it fails closed like a read.
		if outputshare.Default(old).Validate() != nil {
			return fmt.Errorf("%w: the stored output sharing setting of %s", outputshare.ErrInvalid, ns)
		}

		if len(e.Changes) == 1 {
			e.Changes[0].Old = &old
		}

		n, err := q.SetNamespaceOutputSharingDefault(ctx, db.SetNamespaceOutputSharingDefaultParams{
			OutputSharingDefault: string(d),
			Name:                 ns.String(),
		})
		if err != nil {
			return fmt.Errorf("update output sharing setting: %w", err)
		}

		if n == 0 {
			return namespace.ErrNotFound
		}

		return nil
	})
}

// validSettingNamespace returns an error that wraps outputshare.ErrInvalid
// when ns is not a valid namespace name.
func validSettingNamespace(ns namespace.Name) error {
	// Defense in depth: the handler resolves the namespace of the route, and
	// the query uses a placeholder. This check stays so that a new caller
	// cannot pass a name that no namespace can have.
	if _, err := namespace.ParseName(ns.String()); err != nil {
		return fmt.Errorf("%w: namespace name", outputshare.ErrInvalid)
	}

	return nil
}
