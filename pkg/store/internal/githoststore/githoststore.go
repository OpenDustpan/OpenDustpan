// Package githoststore holds the rules of the Git host connection tables that
// the SQLite and PostgreSQL adapters share: the check of a stored row. A row
// is untrusted when it is read, because a backup, a restore or another version
// of the server can have written it.
package githoststore

import (
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
}

// Registration builds the registration of a stored row and checks it again
// with Registration.Validate. The check includes the address digest, so a row
// whose address changed after its key was stored does not pass. It returns
// ErrCorrupt for a row that fails.
func Registration(r RegistrationRow) (forge.Registration, error) {
	out := forge.Registration{
		CreatedAt: time.UnixMilli(r.CreatedAtMs).UTC(), UpdatedAt: time.UnixMilli(r.UpdatedAtMs).UTC(),
		KeyCreatedAt:         time.UnixMilli(r.KeyCreatedAtMs).UTC(),
		EventSecretCreatedAt: time.UnixMilli(r.EventSecretCreatedAtMs).UTC(), Kind: forge.Kind(r.Kind),
		AppID: r.AppID, BaseURL: r.BaseURL, HookID: r.HookID, KeySetID: r.KeySetID,
		EventSecretSetID: r.EventSecretSetID, AddressDigest: r.AddressDigest, SealedKey: r.SealedKey,
		SealedEventSecret: r.SealedEventSecret, Version: r.Version,
	}

	if err := out.Validate(); err != nil {
		return forge.Registration{}, fmt.Errorf("%w: %w", ErrCorrupt, err)
	}

	return out, nil
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
