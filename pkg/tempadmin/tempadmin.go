// Package tempadmin keeps the temporary administrators of an installation.
//
// A temporary administrator is a principal of the kind person that the host
// command makes for one named person after every administrator is lost. The
// record carries a fixed expiry: the principal works until that time, and a
// sweep disables it then. The API answers the flag with the principal, so
// the console shows a banner for it. Each step writes an audit entry: the
// create, each sign-in, and the expiry.
package tempadmin

import (
	"context"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/principal"
)

// Sentinel errors returned by this package and by Store implementations.
var (
	// ErrNotFound reports a temporary administrator that does not exist.
	ErrNotFound = errors.New("temporary administrator not found")
	// ErrAlreadyExists reports a principal that already has a temporary
	// administrator record.
	ErrAlreadyExists = errors.New("temporary administrator already exists")
	// ErrInvalid reports a record or an input that fails validation.
	ErrInvalid = errors.New("invalid temporary administrator")
	// ErrExpired reports a use after the expiry of the record.
	ErrExpired = errors.New("temporary administrator expired")
	// ErrMissingDependency reports a Config without a required port.
	ErrMissingDependency = errors.New("missing dependency")
)

// Lifetime is the fixed time a temporary administrator works after its
// create. The create sets the expiry once, and nothing extends it.
const Lifetime = 24 * time.Hour

// maxIDLength bounds the principal ID of a record, in bytes.
const maxIDLength = 64

// TempAdmin is one temporary administrator. PrincipalID is the stable ID of
// the principal, with the prefix "prn-". Subject is the name of the person
// the host command passed. ExpiresAt is the fixed end of the grant, exactly
// Lifetime after CreatedAt. CreatedBy names the host operator that made it.
type TempAdmin struct {
	CreatedAt   time.Time
	ExpiresAt   time.Time
	PrincipalID string
	Subject     string
	CreatedBy   string
}

// ActiveAt reports whether r works at now: now holds a time before the
// expiry.
func (r TempAdmin) ActiveAt(now time.Time) bool {
	return now.Before(r.ExpiresAt)
}

// Validate reports whether r holds usable values: a principal ID in ID
// form, a subject that principal.ValidateSubject accepts, a named creator,
// and an expiry exactly Lifetime after the creation.
func (r TempAdmin) Validate() error {
	if !ident.Valid(r.PrincipalID, maxIDLength) {
		return fmt.Errorf("%w: principal %q", ErrInvalid, r.PrincipalID)
	}

	if err := principal.ValidateSubject(r.Subject); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	if r.CreatedBy == "" {
		return fmt.Errorf("%w: missing creator", ErrInvalid)
	}

	want := r.CreatedAt.UTC().Truncate(time.Millisecond).Add(Lifetime)
	if !r.ExpiresAt.Equal(want) {
		return fmt.Errorf("%w: the expiry is not %s after the creation", ErrInvalid, Lifetime)
	}

	return nil
}

// Ref returns the audit object of the temporary administrator of the
// principal id: "temp-admin:<id>". An id outside ID form gives
// "temp-admin:invalid", so request text never reaches the audit log.
func Ref(id string) string {
	if !ident.Valid(id, maxIDLength) {
		return "temp-admin:invalid"
	}

	return "temp-admin:" + id
}

// Action names the action of the audit entries of the life cycle: the
// create, each sign-in, and the expiry. No route declares it: the host
// command and the server write these entries on their own.
const Action = "server:manage-temporary-administrators"

// Store is the persistence port for temporary administrators. The records
// belong to the installation, so their writes carry no namespace fence.
//
// Each write takes the audit entry of the change, in the scope
// audit.ServerScope, and appends it in the same transaction as the write:
// both commit, or neither. A write returns audit.ErrInvalidEntry, and
// changes nothing, when the entry fails validation or names another scope.
// The time of the change is e.At.
type Store interface {
	// CreateTempAdmin persists r. It returns ErrAlreadyExists when the
	// principal already has a record, and principal.ErrNotFound when the
	// principal does not exist.
	CreateTempAdmin(ctx context.Context, r TempAdmin, e audit.Entry) error
	// GetTempAdmin returns the record of the principal principalID, or
	// ErrNotFound.
	GetTempAdmin(ctx context.Context, principalID string) (TempAdmin, error)
	// GetTempAdminBySubject returns the record of the person with the
	// subject subject, or ErrNotFound.
	GetTempAdminBySubject(ctx context.Context, subject string) (TempAdmin, error)
	// ListTempAdmins returns every record, ordered by expiry.
	ListTempAdmins(ctx context.Context) ([]TempAdmin, error)
	// ExpireTempAdmin disables the principal principalID and drops its
	// record at e.At, with the entry e. It returns ErrNotFound when no
	// record exists.
	ExpireTempAdmin(ctx context.Context, principalID string, e audit.Entry) error
}

// Reader reads temporary administrator records. Store implements it. The
// authenticator decorator and the API adapters take it, so a reader needs
// no write port.
type Reader interface {
	// GetTempAdminBySubject returns the record of the person with the
	// subject subject, or ErrNotFound.
	GetTempAdminBySubject(ctx context.Context, subject string) (TempAdmin, error)
}
