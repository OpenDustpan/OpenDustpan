// Package manage is the client port for managing OpenDustpan as code. The
// command-line client and the providers for OpenTofu and the Pulumi CLI call
// this port, and an adapter turns each call into a request to the tool-neutral
// REST API.
//
// The package holds the port, its errors and the version pin rule. It imports
// no HTTP package and no generated client, so each adapter can sit in a module
// of its own.
package manage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
)

// Kind names a kind of object that a team manages as code.
type Kind string

// The kinds that the first release manages.
const (
	// KindCollection is a collection of units in a namespace.
	KindCollection Kind = "collection"
	// KindApprovalRule is an approval rule.
	KindApprovalRule Kind = "approval-rule"
	// KindOwnershipRule is an ownership rule.
	KindOwnershipRule Kind = "ownership-rule"
)

// Kinds returns the kinds that a client must support, in a fixed order.
func Kinds() []Kind {
	return []Kind{KindCollection, KindApprovalRule, KindOwnershipRule}
}

// Errors that a Client returns. An adapter translates the status code and the
// error code of the server into one of them, and wraps it with the detail of
// the server. A caller tests for them with errors.Is.
var (
	// ErrInvalidRef means that a Ref breaks the allowlist of Ref.Validate. The
	// adapter sends no request.
	ErrInvalidRef = errors.New("manage: reference is not valid")
	// ErrNotFound means that the object does not exist, or that the caller may
	// not know that it exists.
	ErrNotFound = errors.New("manage: object not found")
	// ErrAlreadyExists means that Create found an object with the same
	// reference. Create never adopts it.
	ErrAlreadyExists = errors.New("manage: object already exists")
	// ErrConflict means that the version of the object on the server differs
	// from the version the caller sent. The server changed nothing.
	ErrConflict = errors.New("manage: object version differs")
	// ErrInvalidSpec means that the server refused the spec of the object.
	ErrInvalidSpec = errors.New("manage: spec is not valid")
	// ErrUnauthenticated means that the server did not accept the token.
	ErrUnauthenticated = errors.New("manage: not signed in")
	// ErrForbidden means that the token does not allow the action.
	ErrForbidden = errors.New("manage: action not allowed")
	// ErrFreshAuthRequired means that the step needs a fresh authentication
	// (error code mfa.fresh-step-required). An access token never gives one.
	// The server changed nothing. The wrapped text names the path of a new
	// sign-in.
	ErrFreshAuthRequired = errors.New("manage: fresh authentication required")
)

// Ref names one object: its kind, its namespace and its ID in the namespace.
type Ref struct {
	Kind      Kind
	Namespace string
	ID        string
}

// refName is the allowlist for the namespace and the ID of a Ref. It is a
// subset of what the server accepts, so the client refuses a value before it
// builds a path from it.
var refName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// Validate returns ErrInvalidRef, wrapped, when the kind is not known or when
// the namespace or the ID is not on the allowlist. It refuses a value and does
// not change it to make it valid.
//
// Defense in depth: the server validates the same fields, and the adapter
// encodes each path segment. This check stays so that a new adapter cannot
// build a request path from a hostile value.
func (r Ref) Validate() error {
	known := false

	for _, kind := range Kinds() {
		if r.Kind == kind {
			known = true
		}
	}

	if !known {
		return fmt.Errorf("%w: kind %q", ErrInvalidRef, string(r.Kind))
	}

	if !refName.MatchString(r.Namespace) {
		return fmt.Errorf("%w: namespace %q", ErrInvalidRef, r.Namespace)
	}

	if !refName.MatchString(r.ID) {
		return fmt.Errorf("%w: id %q", ErrInvalidRef, r.ID)
	}

	return nil
}

// Object is an object as the server holds it.
type Object struct {
	Ref Ref
	// Version is the entity tag of the object. A caller sends it back to
	// change or delete the object. It is opaque.
	Version string
	// Spec is the JSON document of the settings of the object, in the shape of
	// the API for its kind.
	Spec json.RawMessage
}

// Page asks for one page of a list.
type Page struct {
	// Cursor is the Next value of the previous page. It is empty for the first
	// page.
	Cursor string
	// Limit is the maximum number of objects, or 0 for the default of the
	// server.
	Limit int
}

// ObjectPage is one page of a list.
type ObjectPage struct {
	Objects []Object
	// Next is the cursor of the next page. It is empty on the last page.
	Next string
}

// Client is the port that the command-line client and the providers use. Each
// method returns the errors of this package, wrapped with the detail of the
// server, and no driver or transport error. A method that takes a Ref returns
// ErrInvalidRef before it sends a request.
//
// Every method also returns the error of the context when the context ends,
// and ErrUnauthenticated, ErrForbidden and ErrFreshAuthRequired when the
// server answers so.
//
// Write methods are safe to retry. The adapter sends an idempotency key, so a
// retry after a lost answer does not make a second change.
type Client interface {
	// ServerVersion returns the version of the server. A caller checks it
	// with CheckPin before any other call.
	ServerVersion(ctx context.Context) (string, error)

	// Get returns the object. It returns ErrNotFound when there is none.
	Get(ctx context.Context, ref Ref) (Object, error)

	// List returns one page of the objects of a kind in a namespace, in a
	// stable order. It returns ErrInvalidRef when the kind or the namespace
	// is not valid.
	List(ctx context.Context, kind Kind, namespace string, page Page) (ObjectPage, error)

	// Create makes the object and returns it with its first version. It
	// returns ErrAlreadyExists when an object has the reference, and
	// ErrInvalidSpec when the server refuses the spec.
	Create(ctx context.Context, ref Ref, spec json.RawMessage) (Object, error)

	// Update replaces the spec of the object when the version of obj is the
	// version on the server. It returns the new object. It returns
	// ErrConflict when the versions differ, ErrNotFound when there is no
	// object, and ErrInvalidSpec when the server refuses the spec.
	Update(ctx context.Context, obj Object) (Object, error)

	// Delete removes the object when version is the version on the server. It
	// returns ErrConflict when the versions differ and ErrNotFound when there
	// is no object.
	Delete(ctx context.Context, ref Ref, version string) error
}
