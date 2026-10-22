package postgres

import (
	"context"
	"errors"
	"fmt"

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

	// Defense in depth: the fence check of the transaction compares the
	// namespace of the fence with the namespace row. This check stays so that
	// the setting of one namespace cannot be written under the fence of
	// another namespace. It does not rely on the caller.
	if fence.Namespace != ns {
		return fmt.Errorf("%w: setting names %q, fence names %q", namespace.ErrFenced, ns, fence.Namespace)
	}

	return o.s.inFencedAuditedTx(ctx, fence, &e, func(q *db.Queries) error {
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
