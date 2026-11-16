package trustrule

import (
	"context"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Store keeps the trust rules. A SQL adapter serves it from the database of
// the installation. Every write carries the fence of the namespace of the
// rule and the audit entry of the change. The caller stamps the time, the
// actor and the object on the entry. The store checks the fence and appends
// the entry in the transaction of the write, and it changes nothing when
// either fails (fail closed). An entry that fails validation, or that names
// another namespace than the fence, gives audit.ErrInvalidEntry.
//
// A store does not check access. The caller checks the action on the
// namespace, and principal:manage-tokens on the principal of a new rule.
type Store interface {
	// Create stores r. In the transaction of the write it checks that the
	// principal r.PrincipalID exists, is not deleted, is a service
	// principal, and is a principal of the namespace r.Namespace. It returns:
	//   - ErrInvalid when r.Validate fails.
	//   - ErrPrincipal when the principal fails the check above.
	//   - ErrBundle when r names a bundle that is not the bundle of the
	//     namespace.
	//   - ErrExists when the ID is taken, or when the namespace has a rule
	//     with the same name, compared exactly.
	//   - ErrLimit when the namespace holds MaxPerNamespace rules.
	//   - namespace.ErrFenced when the fence is stale or belongs to another
	//     namespace than r.
	Create(ctx context.Context, r Rule, fence namespace.Fence, e audit.Entry) error
	// Get returns the rule id of the namespace ns. It returns ErrNotFound for
	// an unknown ID and for a rule of another namespace, and ErrInvalid for
	// an ID or a namespace that fails validation. A stored row that fails
	// the checks of a rule is not served: it gives ErrNotFound.
	Get(ctx context.Context, ns namespace.Name, id ID) (Rule, error)
	// List returns the rules of the namespace ns, ordered by name, then ID.
	// A namespace with no rule gives an empty list. A stored row that fails
	// the checks of a rule is left out. It returns ErrInvalid for a namespace
	// that fails validation.
	List(ctx context.Context, ns namespace.Name) ([]Rule, error)
	// Delete removes the rule id of the namespace ns and appends e in the
	// transaction. It returns ErrNotFound for an unknown rule and for a rule
	// of another namespace, ErrInvalid for an ID or a namespace that fails
	// validation, and namespace.ErrFenced.
	Delete(ctx context.Context, ns namespace.Name, id ID, fence namespace.Fence, e audit.Entry) error
	// Update stores r over the rule id of the namespace ns when its revision
	// is one past the stored revision, and appends e in the transaction. The
	// name, the issuer, the audience, the key source, the conditions, the
	// principal, the SPIFFE ID pattern and the bundle may change; the
	// namespace, the creator and the creation time never change. In the
	// transaction it checks the principal as Create does, and that a bundle
	// that r names is the bundle of the namespace. It returns:
	//   - ErrNotFound for an unknown rule and for a rule of another
	//     namespace, and ErrInvalid for an ID or a namespace that fails
	//     validation, or for a rule that fails validation.
	//   - ErrPrincipal when the principal fails the check of Create.
	//   - ErrBundle when r names a bundle that is not the bundle of the
	//     namespace.
	//   - ErrExists when another rule of the namespace has the name.
	//   - ErrStale when the revision of r is not one past the stored
	//     revision.
	//   - namespace.ErrFenced when the fence is stale or belongs to another
	//     namespace than ns.
	Update(ctx context.Context, ns namespace.Name, r Rule, fence namespace.Fence, e audit.Entry) (Rule, error)
}

// BundleStore keeps the trust bundle of each namespace: one bundle per
// namespace with append-only versions and one active pointer.
type BundleStore interface {
	// SaveBundle checks the PEM text of f, stores it as a new version of the
	// bundle of the namespace ns, and appends e in the transaction. The
	// first save makes the bundle and marks the version active; a later save
	// keeps the active pointer and retires versions past MaxBundleVersions.
	// It returns ErrBundle for input that fails the checks, with the CA
	// index in the message, and changes nothing.
	SaveBundle(
		ctx context.Context, ns namespace.Name, f BundleFields, fence namespace.Fence, e audit.Entry,
	) (Bundle, error)
	// RotateBundle checks the PEM text of f and the overlap, stores it as a
	// new version, and moves the active pointer to it with
	// overlapEndsAt = now + overlap. The overlap must not exceed MaxOverlap.
	// It returns ErrBundle for input that fails the checks, ErrStale when
	// f.Revision is not the current revision of the bundle, and changes
	// nothing on any error.
	RotateBundle(
		ctx context.Context, ns namespace.Name, f BundleFields, fence namespace.Fence, e audit.Entry,
	) (Bundle, error)
	// GetBundle returns the bundle of the namespace ns, with all kept
	// versions. It returns ErrNotFound for a namespace with no bundle, and
	// ErrInvalid for a namespace that fails validation. A stored row that
	// fails its checks is not served: it gives ErrNotFound.
	GetBundle(ctx context.Context, ns namespace.Name) (Bundle, error)
	// DeleteBundleVersion removes a retired version of the bundle of the
	// namespace ns and appends e in the transaction. It returns
	// ErrBundleActive for the active version, ErrNotFound for an unknown
	// version or for a rule of another namespace, and namespace.ErrFenced.
	DeleteBundleVersion(
		ctx context.Context, ns namespace.Name, version int64, fence namespace.Fence, e audit.Entry,
	) error
}
