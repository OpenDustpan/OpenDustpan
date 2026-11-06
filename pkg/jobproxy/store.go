package jobproxy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Scope names the owner of a stored proxy rule set: a namespace, or one unit
// of the namespace by its stable unit ID. A blueprint version holds its
// proxy rule set in the blueprint version, not in this store.
type Scope struct {
	Namespace namespace.Name
	// UnitID is the stable ID of the unit, or empty for the proxy rule set
	// of the namespace.
	UnitID string
}

// Stored is a proxy rule set as the store keeps it. Document holds the bytes
// that the caller saved, so a person reads the document as they wrote it.
type Stored struct {
	Scope    Scope
	Document []byte
	// Digest is the lowercase hex SHA-256 of Document.
	Digest string
	// Version is a count of writes. The first write gives 1.
	Version int64
	// UpdatedAt is the time of the last write, in Unix milliseconds.
	UpdatedAt int64
}

// Change is one write of a proxy rule set: the scope, the document and the
// version that the caller read.
type Change struct {
	Scope    Scope
	Document []byte
	// Expected is the version that the caller read, or 0 for a scope that
	// has no proxy rule set.
	Expected int64
}

// DocumentDigest returns the lowercase hex SHA-256 of doc, the Digest of a
// stored proxy rule set.
func DocumentDigest(doc []byte) string {
	sum := sha256.Sum256(doc)

	return hex.EncodeToString(sum[:])
}

// ValidScope checks the shape of s: a namespace name, and a unit ID that is
// empty or from 1 to 64 letters, digits, _ or -.
func ValidScope(s Scope) error {
	if _, err := namespace.ParseName(string(s.Namespace)); err != nil {
		return fmt.Errorf("%w: the scope names no valid namespace", ErrInvalid)
	}

	if s.UnitID != "" && !namePattern.MatchString(s.UnitID) {
		return fmt.Errorf("%w: the scope names no valid unit ID", ErrInvalid)
	}

	return nil
}

// RuleStore keeps the proxy rule set of each namespace and each unit. A SQL
// adapter serves it from the database of the namespace. Every write carries
// the fence of the namespace and the audit entry of the change. The store
// checks the fence, checks the version and appends the entry in the
// transaction of the write, and it changes nothing when one of them fails
// (fail closed).
//
// The store checks no grant: the caller checks the action on the namespace
// or the unit. The store does not check that the unit exists.
//
// A version is a count of writes. A write names the version that the caller
// read, and 0 names a scope with no proxy rule set. The check runs in the
// transaction of the write, so two writers with one version cannot both win.
type RuleStore interface {
	// Get returns the proxy rule set of scope. It returns ErrNotFound when
	// the scope has none, and ErrInvalid for a scope that fails ValidScope.
	// A unit scope never returns the set of its namespace, and a scope of one
	// namespace never returns a set of another namespace.
	Get(ctx context.Context, scope Scope) (Stored, error)
	// Put stores c.Document as the proxy rule set of c.Scope and returns the
	// stored set, with Version one above the stored one, Digest of the
	// document and UpdatedAt set to e.At. It returns:
	//   - ErrInvalid when the scope fails ValidScope, when the document fails
	//     Parse with no allowed range, or when e names another namespace than
	//     the scope.
	//   - ErrConflict when c.Expected is not the stored version (0 when the
	//     scope has a set already).
	//   - namespace.ErrFenced when the fence is stale or belongs to another
	//     namespace than the scope.
	Put(ctx context.Context, c Change, fence namespace.Fence, e audit.Entry) (Stored, error)
	// Delete removes the proxy rule set of scope. It returns ErrNotFound
	// when the scope has none, and ErrInvalid, ErrConflict and
	// namespace.ErrFenced as Put does. On an error it changes nothing.
	Delete(ctx context.Context, scope Scope, expected int64, fence namespace.Fence, e audit.Entry) error
}
