package principal

import (
	"context"
	"errors"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// KindServer is the kind of a server-made principal: a principal of one
// namespace that the server makes for one purpose. ParseKind refuses it, so
// no request to the principal API can make, name or change the kind. Only
// ServerPrincipalStore stores a principal of this kind.
const KindServer Kind = "server"

// ErrServerPrincipal reports a change that no request can make to a
// server-made principal: a token, a trust rule, an act-as edge, a group
// membership, a change of its record, or a delete. A revoke of one of its
// grants is not such a change.
var ErrServerPrincipal = errors.New("only the server can change a server-made principal")

// PurposeKind names the work that a server-made principal does.
type PurposeKind string

// The kinds of a purpose. Each kind defines the actions that its principal
// can hold. A grant to the principal of a purpose with an action outside
// that list gives the principal nothing more.
const (
	// PurposeScheduledRuns starts the plans of the schedule triggers of one
	// unit. Its principal can hold identity.ActionUnitRunPlan on that unit.
	PurposeScheduledRuns PurposeKind = "scheduled-runs"
	// PurposeUpstreamRuns starts the plans of the follows triggers of one
	// unit. Its principal can hold identity.ActionUnitRunPlan on that unit.
	PurposeUpstreamRuns PurposeKind = "upstream-runs"
)

// Purpose is the work of one server-made principal: a kind and the object
// that the work serves. Served is a unit object with its stable ID: the
// purpose follows the unit and not its name. A namespace has at most one
// server-made principal for each purpose.
type Purpose struct {
	Served identity.Object
	Kind   PurposeKind
}

// OwnerKind names what the accountable owner of a server-made principal is.
type OwnerKind string

// The kinds of an accountable owner.
const (
	// OwnerPerson is one principal of the kind person.
	OwnerPerson OwnerKind = "person"
	// OwnerGroup is one group. Each current member can take the
	// responsibility, and the grants of the group cap the principal.
	OwnerGroup OwnerKind = "group"
)

// Owner is the accountable owner of a server-made principal: the person or
// the group that answers for its grants. ID is the ID of the principal
// record or of the group record. The current grants of the owner cap what
// the server-made principal does at each use.
type Owner struct {
	Kind OwnerKind
	ID   string
}

// ServerPrincipal is the stored record of one server-made principal. The
// principal record with the ID PrincipalID has the kind KindServer, and the
// namespace Namespace. OwnerSince is the time of the last change of the
// owner. A read sets LastUsedAt to the time of its last use, or to the zero
// time when the server never acted as it.
//
// The record holds no grant. The grants of the principal are ordinary
// stored grants of the access package, made through the same grant path as
// the grants of a person. The record holds no credential either: only the
// server process acts as the principal.
type ServerPrincipal struct {
	CreatedAt   time.Time
	OwnerSince  time.Time
	LastUsedAt  time.Time
	PrincipalID string
	Namespace   namespace.Name
	Purpose     Purpose
	Owner       Owner
}

// ServerPrincipalStore keeps the records of the server-made principals.
//
// Each write takes the fence of the namespace of the principal and checks
// it in the transaction of the write, and appends its audit entry in that
// namespace in the same transaction: both commit, or neither. The subject
// of each entry names the server, and the reason of the entry names the
// cause, for example the save of a trigger. A write returns ErrInvalid when
// the fence or the entry names another namespace, and namespace.ErrNotFound
// or namespace.ErrFenced from the fence check. On every error a write
// changes nothing.
//
// The store does not check access. The caller checks that the owner holds
// every action that the purpose needs, with the escalation check of a grant.
//
// The methods of RecordStore, NamespaceRecordStore and MemberStore answer
// ErrServerPrincipal for a change of a principal of the kind KindServer.
// Their reads list it as every other principal, with its kind.
type ServerPrincipalStore interface {
	// EnsureServerPrincipal returns the server-made principal of
	// sp.Purpose in the namespace sp.Namespace. When no such principal
	// exists, it makes the principal record of the kind KindServer and
	// the record sp in one transaction, and appends the entry e. A second
	// call for the same purpose returns the record that exists, makes no
	// change, and appends no entry. It returns ErrInvalid when
	// sp.Purpose.Served is not a unit of sp.Namespace with a stable ID,
	// when the purpose kind is unknown, or when the owner is not a valid
	// owner. It returns ErrNotFound when the owner record has ended.
	EnsureServerPrincipal(
		ctx context.Context, sp ServerPrincipal, fence namespace.Fence, e audit.Entry,
	) (ServerPrincipal, error)
	// ServerPrincipalFor returns the server-made principal of purpose in
	// the namespace ns, or ErrNotFound.
	ServerPrincipalFor(ctx context.Context, ns namespace.Name, purpose Purpose) (ServerPrincipal, error)
	// ListServerPrincipals returns the server-made principals of the
	// namespace ns that exist, ordered by the ID of the principal record:
	// the rows of q, with that ID as the key, and the count of every such
	// principal of ns.
	ListServerPrincipals(ctx context.Context, ns namespace.Name, q Query) ([]ServerPrincipal, int64, error)
	// SetOwner makes owner the accountable owner of the server-made
	// principal with the ID principalID in the namespace of fence, at e.At.
	// It keeps every grant of the principal. It returns ErrNotFound when no
	// such principal exists in that namespace or the owner record has
	// ended, and ErrInvalid when owner is not a valid owner.
	SetOwner(ctx context.Context, principalID string, owner Owner, fence namespace.Fence, e audit.Entry) error
}
