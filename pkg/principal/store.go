package principal

import (
	"context"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
)

// Store is the persistence port for principals, groups and memberships.
// The records belong to the installation, so no write carries a namespace
// fence. Each list answers its rows and its total in one read
// transaction.
//
// Each write takes the audit entry of the change, in the scope
// audit.ServerScope, and appends it in the same transaction as the write
// (ADR 60, fail closed): both commit, or neither. A write returns
// audit.ErrInvalidEntry, and changes nothing, when the entry fails
// validation or names another scope. The time of the change is e.At: an end
// time, such as the end of a membership, is e.At too.
type Store interface {
	RecordStore
	GroupStore
	MemberStore
}

// RecordStore keeps the records of the principals.
type RecordStore interface {
	// CreatePrincipal persists p. It returns ErrAlreadyExists when the ID is
	// taken, or when a principal that exists has the subject.
	CreatePrincipal(ctx context.Context, p Principal, e audit.Entry) error
	// GetPrincipal returns the principal with the ID id, or ErrNotFound
	// when no such principal exists or it was deleted.
	GetPrincipal(ctx context.Context, id string) (Principal, error)
	// GetPrincipalBySubject returns the principal that exists with the
	// subject subject, or ErrNotFound.
	GetPrincipalBySubject(ctx context.Context, subject string) (Principal, error)
	// ListPrincipals returns the principals that exist, ordered by subject:
	// the rows of q, with the subject as the key. A kind that is not empty
	// keeps the principals of that kind, in the rows and in the total.
	ListPrincipals(ctx context.Context, q Query, kind Kind) ([]Principal, int64, error)
	// UpdatePrincipal replaces the display name and the update time of the
	// principal with the ID p.ID. The subject, the kind and the creation
	// time do not change. It returns ErrNotFound when no such principal
	// exists.
	UpdatePrincipal(ctx context.Context, p Principal, e audit.Entry) error
	// DeletePrincipal ends the principal with the ID id and each membership
	// of it, at e.At. The rows stay for the history. It returns ErrNotFound
	// when no such principal exists.
	DeletePrincipal(ctx context.Context, id string, e audit.Entry) error
}

// GroupStore keeps the group records.
type GroupStore interface {
	// CreateGroup persists g. It returns ErrAlreadyExists when the ID is
	// taken, or when a group that exists has the name without regard to
	// case.
	CreateGroup(ctx context.Context, g Group, e audit.Entry) error
	// GetGroup returns the group with the ID id, or ErrNotFound when no
	// such group exists or it was deleted.
	GetGroup(ctx context.Context, id string) (Group, error)
	// ListGroups returns the groups that exist, ordered by name without
	// regard to case: the rows of q, with the lower-case name as the key.
	ListGroups(ctx context.Context, q Query) ([]Group, int64, error)
	// UpdateGroup replaces the name, the description and the update time of
	// the group with the ID g.ID. It returns ErrNotFound when no such group
	// exists, and ErrAlreadyExists when another group has the name without
	// regard to case.
	UpdateGroup(ctx context.Context, g Group, e audit.Entry) error
	// DeleteGroup ends the group with the ID id and each membership in it,
	// at e.At. It returns ErrNotFound when no such group exists.
	DeleteGroup(ctx context.Context, id string, e audit.Entry) error
}

// MemberStore keeps the memberships of principals in groups. A membership
// starts at AddedAt and ends at a removal. An ended membership stays for
// the history, and the principal can join the group again.
type MemberStore interface {
	// AddMember persists m. It returns ErrNotFound when the group or the
	// principal does not exist, ErrAlreadyExists when the principal is a
	// member of the group, and ErrManagedByDirectory when the source of the
	// group is not SourceLocal.
	AddMember(ctx context.Context, m Membership, e audit.Entry) error
	// RemoveMember ends the membership of the principal principalID in the
	// group groupID, at e.At. It returns ErrNotFound when no such
	// membership exists, and ErrManagedByDirectory when the source of the
	// group is not SourceLocal.
	RemoveMember(ctx context.Context, groupID, principalID string, e audit.Entry) error
	// ListMembers returns the members of the group groupID, ordered by
	// subject: the rows of q, with the subject as the key. It returns
	// ErrNotFound when the group does not exist.
	ListMembers(ctx context.Context, groupID string, q Query) ([]Member, int64, error)
	// ListGroupsOf returns the groups of the principal principalID, ordered
	// by name without regard to case: the rows of q, with the lower-case
	// name as the key. It returns ErrNotFound when the principal does not
	// exist.
	ListGroupsOf(ctx context.Context, principalID string, q Query) ([]Group, int64, error)
}

// Guard checks that a caller may change who holds a set of grants (ADR 52).
// A membership change gives or takes the grants of the group, and the delete
// of a principal or a group revokes its grants. So the caller must hold, on
// the object of each grant, every action that the grant gives there, as it
// must to make or revoke the grant itself. access.Service meets it.
//
// Each method returns nil when caller may make the change, or an error that
// wraps the reason: access.ErrEscalation for an action that caller does not
// hold.
type Guard interface {
	// CheckGroupGrants checks the current grants of the group groupID.
	CheckGroupGrants(ctx context.Context, caller identity.Principal, groupID string) error
	// CheckPrincipalGrants checks the current grants of the principal
	// principalID.
	CheckPrincipalGrants(ctx context.Context, caller identity.Principal, principalID string) error
}
