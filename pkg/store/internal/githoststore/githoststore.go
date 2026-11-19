// Package githoststore holds the rules of the Git host connection tables that
// the SQLite and PostgreSQL adapters share: the check of a stored row. A row
// is untrusted when it is read, because a backup, a restore or another version
// of the server can have written it.
package githoststore

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/forge"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// ErrCorrupt reports a stored row that does not pass the checks of its type.
// The adapters do not serve such a row.
var ErrCorrupt = errors.New("stored Git host row is not valid")

// ConnectionRow is a connection row as the database holds it. Namespace is the
// name of the namespace.
type ConnectionRow struct {
	Namespace      string
	ID             string
	Kind           string
	Auth           string
	AppScope       string
	InstallationID string
	Push           bool
	IsDefault      bool
	Version        int64
	CreatedAtMs    int64
	UpdatedAtMs    int64
}

// Connection builds the record of a stored row and checks it again with
// ConnectionRecord.Validate. It returns ErrCorrupt for a row that fails.
func Connection(r ConnectionRow) (forge.ConnectionRecord, error) {
	c := forge.ConnectionRecord{
		CreatedAt: time.UnixMilli(r.CreatedAtMs).UTC(), UpdatedAt: time.UnixMilli(r.UpdatedAtMs).UTC(),
		Namespace: namespace.Name(r.Namespace), ID: r.ID, Kind: forge.Kind(r.Kind), Auth: forge.AuthKind(r.Auth),
		Scope: forge.AppScope(r.AppScope), InstallationID: r.InstallationID, Version: r.Version, Push: r.Push,
		Default: r.IsDefault,
	}

	if err := c.Validate(); err != nil {
		return forge.ConnectionRecord{}, fmt.Errorf("%w: %w", ErrCorrupt, err)
	}

	return c, nil
}

// RegistrationRow is a registration row as the database holds it.
type RegistrationRow struct {
	Kind                   string
	AppID                  string
	BaseURL                string
	HookID                 string
	SealedKey              []byte
	KeySetID               string
	KeyCreatedAtMs         int64
	SealedEventSecret      []byte
	EventSecretSetID       string
	EventSecretCreatedAtMs int64
	AddressDigest          string
	Version                int64
	CreatedAtMs            int64
	UpdatedAtMs            int64
	// The three columns of the old event secret hold NULL while no rotation
	// window is open. OldSealedEventSecret is nil for NULL.
	OldEventSecretEndsAtMs sql.NullInt64
	OldEventSecretSetID    sql.NullString
	OldSealedEventSecret   []byte
}

// OldEventSecretColumns holds the three columns of the old event secret of a
// registration as a database stores them: all NULL, or all set.
type OldEventSecretColumns struct {
	EndsAtMs  sql.NullInt64
	SetID     sql.NullString
	SealedKey []byte
}

// OldEventSecret returns the columns of the old event secret of r. A
// registration with no window gives NULL in all three. It returns an error
// that wraps forge.ErrInvalid for a registration that holds a part of the set,
// so no adapter writes a part.
//
// Defense in depth: the type of the registration and the CHECK constraint of
// each schema refuse a part of the set too. This check stays so that an
// adapter never sends such a row to the database, and does not rely on either.
func OldEventSecret(r *forge.Registration) (OldEventSecretColumns, error) {
	set := r.OldEventSecretSetID != ""
	sealed := len(r.OldSealedEventSecret) > 0
	ends := !r.OldEventSecretEndsAt.IsZero()

	var none OldEventSecretColumns

	if !set && !sealed && !ends {
		return none, nil
	}

	if !set || !sealed || !ends {
		return none, fmt.Errorf(
			"%w: the old event secret needs its set ID, its sealed value and its end, or none of them",
			forge.ErrInvalid,
		)
	}

	return OldEventSecretColumns{
		EndsAtMs:  sql.NullInt64{Int64: r.OldEventSecretEndsAt.UnixMilli(), Valid: true},
		SetID:     sql.NullString{String: r.OldEventSecretSetID, Valid: true},
		SealedKey: r.OldSealedEventSecret,
	}, nil
}

// Registration builds the registration of a stored row and checks it again
// with Registration.Validate. The check includes the address digest, so a row
// whose address changed after its key was stored does not pass. It returns
// ErrCorrupt for a row that fails.
func Registration(r RegistrationRow) (forge.Registration, error) {
	// Defense in depth: the CHECK constraint of each schema and
	// Registration.Validate refuse a part of the old set too. A row that another
	// version wrote fails closed here whatever they do.
	old, oldErr := oldEventSecretOf(&r)
	if oldErr != nil {
		return forge.Registration{}, fmt.Errorf("%w: %w", ErrCorrupt, oldErr)
	}

	out := forge.Registration{
		CreatedAt: time.UnixMilli(r.CreatedAtMs).UTC(), UpdatedAt: time.UnixMilli(r.UpdatedAtMs).UTC(),
		KeyCreatedAt:         time.UnixMilli(r.KeyCreatedAtMs).UTC(),
		EventSecretCreatedAt: time.UnixMilli(r.EventSecretCreatedAtMs).UTC(), Kind: forge.Kind(r.Kind),
		AppID: r.AppID, BaseURL: r.BaseURL, HookID: r.HookID, KeySetID: r.KeySetID,
		EventSecretSetID: r.EventSecretSetID, AddressDigest: r.AddressDigest, SealedKey: r.SealedKey,
		SealedEventSecret: r.SealedEventSecret, OldEventSecretSetID: old.id, OldSealedEventSecret: old.sealed,
		OldEventSecretEndsAt: old.endsAt, Version: r.Version,
	}

	if err := out.Validate(); err != nil {
		return forge.Registration{}, fmt.Errorf("%w: %w", ErrCorrupt, err)
	}

	return out, nil
}

type oldEventSecret struct {
	id     string
	sealed []byte
	endsAt time.Time
}

// oldEventSecretOf reads the three columns of the old event secret of a row.
// It returns an error that wraps forge.ErrInvalid for a part of the set or for
// an empty sealed value.
func oldEventSecretOf(r *RegistrationRow) (oldEventSecret, error) {
	switch {
	case !r.OldEventSecretEndsAtMs.Valid && !r.OldEventSecretSetID.Valid && r.OldSealedEventSecret == nil:
		return oldEventSecret{id: "", sealed: nil, endsAt: time.Time{}}, nil
	case !r.OldEventSecretEndsAtMs.Valid || !r.OldEventSecretSetID.Valid || len(r.OldSealedEventSecret) == 0:
		return oldEventSecret{}, fmt.Errorf(
			"%w: the row holds a part of the old event secret", forge.ErrInvalid,
		)
	}

	return oldEventSecret{
		id: r.OldEventSecretSetID.String, sealed: r.OldSealedEventSecret,
		endsAt: time.UnixMilli(r.OldEventSecretEndsAtMs.Int64).UTC(),
	}, nil
}

// AssignmentRow is an installation row as the database holds it. Namespace is
// the name of the namespace.
type AssignmentRow struct {
	InstallationID string
	Namespace      string
	Version        int64
	CreatedAtMs    int64
	UpdatedAtMs    int64
}

// Assignment builds the assignment of a stored row and checks it again with
// Assignment.Validate. It returns ErrCorrupt for a row that fails.
func Assignment(r AssignmentRow) (forge.Assignment, error) {
	a := forge.Assignment{
		CreatedAt: time.UnixMilli(r.CreatedAtMs).UTC(), UpdatedAt: time.UnixMilli(r.UpdatedAtMs).UTC(),
		InstallationID: r.InstallationID, Namespace: namespace.Name(r.Namespace), Version: r.Version,
	}

	if err := a.Validate(); err != nil {
		return forge.Assignment{}, fmt.Errorf("%w: %w", ErrCorrupt, err)
	}

	return a, nil
}

// MaxEventReceipts is the number of event receipts that the table holds at
// most.
const MaxEventReceipts = 100000

// EventReceiptMemory is the time that a receipt stays in the table: the store
// deletes a receipt that is older than this time when it records the next one.
const EventReceiptMemory = 24 * time.Hour

// bodyDigestLength is the length of a hex SHA-256 digest.
const bodyDigestLength = 64

// CheckEventReceipt returns an error that wraps forge.ErrInvalid for a receipt
// that a store must not keep: no known kind, no delivery ID, no time, or a body
// digest that is not 64 lowercase hex digits.
//
// Defense in depth: the service checks the same values before it calls the
// store, and the verifier checks the delivery ID before that. This check stays
// so that no caller can store a receipt with a key that never matches.
func CheckEventReceipt(r *forge.EventReceipt) error {
	switch {
	case r.Kind != forge.KindGitHub:
		return fmt.Errorf("%w: the receipt kind %q is not supported", forge.ErrInvalid, r.Kind)
	case r.DeliveryID == "":
		return fmt.Errorf("%w: the receipt has no delivery ID", forge.ErrInvalid)
	case !lowerHex64(r.BodyDigest):
		return fmt.Errorf("%w: the body digest is %d lowercase hex digits", forge.ErrInvalid, bodyDigestLength)
	case r.ReceivedAt.IsZero():
		return fmt.Errorf("%w: the receipt has no time", forge.ErrInvalid)
	}

	return nil
}

func lowerHex64(s string) bool {
	if len(s) != bodyDigestLength {
		return false
	}

	for i := range len(s) {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}

	return true
}
