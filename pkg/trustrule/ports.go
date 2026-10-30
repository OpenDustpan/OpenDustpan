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
}
