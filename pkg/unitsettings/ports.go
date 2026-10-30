package unitsettings

import (
	"context"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Store keeps the settings record of each unit and the unit defaults of each
// namespace. A SQL adapter serves it from the database of the namespace of the
// unit. Every write carries the fence of that namespace, and the store checks
// it in the transaction of the write. A write appends its audit entry in that
// transaction (fail closed).
//
// The store keys a record by the stable unit ID, so a rename keeps the record
// and a deleted and re-created name does not get it. The purge of a unit
// removes its record in the transaction of the purge.
//
// The store checks no grant. The caller checks the actions on the unit and on
// the connection and the pool that the settings name. The store does not check
// that the pool and the connection exist: a pool or a connection can end
// after the write, so the planner checks them again at the start of a job.
//
// A version is a count of writes. A write names the version that the caller
// read, and 0 names a unit that has no record. The check runs in the
// transaction of the write, so two writers with one version cannot both win.
type Store interface {
	// Get returns the record of unit, the unit reference with its stable unit
	// ID. It returns ErrNotFound when the unit has no record, ErrInvalid for
	// a unit that fails Validate or for a stored record that fails Parse (the
	// caller fails closed), and ErrUnknownUnit when the unit does not exist.
	Get(ctx context.Context, unit identity.Object) (Settings, error)
	// Put stores s as the record of s.Unit and returns the stored record.
	// expected is the version that the caller read: 0 creates the record and
	// fails when one exists. In one transaction it checks the fence, checks
	// that the unit exists with its stable ID and its name, checks the
	// version, runs Validate, writes the record with Version one above the
	// stored one and UpdatedAt set to e.At, and appends the entry that
	// ChangeEntry makes from e.
	//
	// A write that changes no field (Equal) of a record at expected writes
	// nothing: no new version, no new time and no audit entry. The result is
	// the stored record.
	//
	// It returns:
	//   - ErrInvalid when s fails Validate, or when e fails validation or
	//     names another namespace than the unit.
	//   - ErrUnknownUnit when the unit does not exist in its namespace, or its
	//     stable ID and its name point to different units.
	//   - ErrConflict when the stored version is not expected.
	//   - namespace.ErrFenced when the fence is stale or belongs to another
	//     namespace than the unit.
	//
	// On every error the store changes nothing.
	Put(ctx context.Context, s *Settings, expected int64, fence namespace.Fence, e audit.Entry) (Settings, error)
	// Delete removes the record of unit and appends the entry that ResetEntry
	// makes from e. expected is the version that the caller read, and it must
	// be one or more. It returns ErrNotFound when the unit has no record,
	// ErrConflict when the stored version is not expected, ErrUnknownUnit,
	// ErrInvalid, and namespace.ErrFenced. On every error the store changes
	// nothing.
	Delete(ctx context.Context, unit identity.Object, expected int64, fence namespace.Fence, e audit.Entry) error
	// Defaults returns the unit defaults of the namespace ns. A namespace that
	// never set them gives the zero value with Version 0. It returns
	// namespace.ErrNotFound for an unknown namespace, and ErrInvalid for a
	// stored value that fails Validate.
	Defaults(ctx context.Context, ns namespace.Name) (NamespaceDefaults, error)
	// SetDefaults stores d as the unit defaults of d.Namespace and returns them, with
	// the same rules for expected, the fence and the audit entry as Put (the
	// entry is DefaultsEntry of e). It returns ErrInvalid, ErrConflict,
	// namespace.ErrNotFound and namespace.ErrFenced. A write that changes no
	// value writes nothing. The change applies to the jobs that start later.
	// It changes no record of a unit.
	SetDefaults(ctx context.Context, d NamespaceDefaults, expected int64, fence namespace.Fence,
		e audit.Entry) (NamespaceDefaults, error)
}
