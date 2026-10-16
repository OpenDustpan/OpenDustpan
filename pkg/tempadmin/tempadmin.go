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

// Lifetime is the longest time a temporary administrator works after its
// create. The create sets the expiry once, and nothing extends it. A create
// with a shorter lifetime ends earlier: the host command uses the token
// policy maximum when that is below Lifetime, so the record and the token
// end together.
const Lifetime = 24 * time.Hour

// maxIDLength bounds the principal ID of a record, in bytes.
const maxIDLength = 64

// TempAdmin is one temporary administrator. PrincipalID is the stable ID of
// the principal, with the prefix "prn-". Subject is the name of the person
// the host command passed. ExpiresAt is the end of the grant, at most
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
// and an expiry after the creation and at most Lifetime after it, in whole
// milliseconds.
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

	created := r.CreatedAt.UTC().Truncate(time.Millisecond)
	expires := r.ExpiresAt.UTC().Truncate(time.Millisecond)

	if !r.ExpiresAt.Equal(expires) {
		return fmt.Errorf("%w: the expiry is not a whole count of milliseconds", ErrInvalid)
	}

	if !expires.After(created) || expires.Sub(created) > Lifetime {
		return fmt.Errorf("%w: the expiry is not within %s after the creation", ErrInvalid, Lifetime)
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

// HostSubject is the subject of the audit entries of the host command: the
// host itself acted, through no sign-in. It starts with '@', which no
// subject of a principal holds, so it never names a principal.
const HostSubject = "@host"

// HostTokenName is the name of the personal token the host command issues.
// One name for every recovery token, so the token list shows their origin
// and the expiry ends them.
const HostTokenName = "host-recovery"

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
	// ListTempAdmins returns every record, ordered by expiry. Expired
	// records stay listed: the expiry keeps them as tombstones, so a
	// caller whose record is not active is still denied.
	ListTempAdmins(ctx context.Context) ([]TempAdmin, error)
	// ExpireTempAdmin ends the temporary administrator of the principal
	// principalID at e.At, with the entry e: it disables the principal,
	// ends every site-administrator grant of the principal on the
	// installation, ends the host-recovery tokens of the principal, and
	// keeps the record as a tombstone, so a later use is still denied. It
	// returns ErrNotFound when no record exists, and ErrExpired when the
	// record already ended: a repeated expiry writes no second entry.
	ExpireTempAdmin(ctx context.Context, principalID string, e audit.Entry) error
}

// Reader reads temporary administrator records. Store implements it. The
// authenticator decorator, the access token service and the API adapters
// take it, so a reader needs no write port.
type Reader interface {
	// GetTempAdminBySubject returns the record of the person with the
	// subject subject, or ErrNotFound.
	GetTempAdminBySubject(ctx context.Context, subject string) (TempAdmin, error)
	// ListTempAdmins returns every record, ordered by expiry, including
	// the expired tombstones.
	ListTempAdmins(ctx context.Context) ([]TempAdmin, error)
}
