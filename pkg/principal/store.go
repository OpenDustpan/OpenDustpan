package principal

import (
	"context"
	"fmt"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Store is the persistence port for principals, groups and memberships.
// The records of RecordStore, GroupStore and MemberStore belong to the
// installation, so their writes carry no namespace fence. The writes of
// NamespaceRecordStore carry one. Each list answers its rows and its total in one read
// transaction.
//
// Each write takes the audit entry of the change, in the scope
// audit.ServerScope, and appends it in the same transaction as the write
// (ADR 60, fail closed): both commit, or neither. A write returns
// audit.ErrInvalidEntry, and changes nothing, when the entry fails
// validation or names another scope. The time of the change is e.At: an end
// time, such as the end of a membership, is e.At too.
//
// DeletePrincipal, DisablePrincipal, DeleteGroup and RemoveMember return
// access.ErrLastSiteAdministrator, and change nothing, when the change would
// leave the installation with no site administrator. A site administrator is
// a principal of the installation of the kind KindPerson that is not deleted
// or disabled and that holds a current grant of the role site-administrator
// on the installation, directly or through an active membership of a group.
// An installation with no site administrator before the change refuses
// nothing. The store counts and changes in one transaction, and it serializes
// these changes with each other and with access.Store.RevokeGrant: two
// concurrent changes that each end one of the last two site administrators
// must not both commit. The store takes the lock that serializes them itself,
// so a caller does not take it.
type Store interface {
	RecordStore
	NamespaceRecordStore
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
	// principal with the ID p.ID. The subject, the kind, the break-glass flag
	// and the creation time do not change. Today no write sets the
	// break-glass flag on an existing person. The rule below guards a later
	// write that does: it locks the row of the principal and returns
	// ErrBreakGlassPassword when the person has a local password, in the same
	// transaction. It returns ErrNotFound when no such principal
	// exists.
	UpdatePrincipal(ctx context.Context, p Principal, e audit.Entry) error
	// DeletePrincipal ends the principal with the ID id and each membership
	// of it, at e.At. The rows stay for the history. It returns ErrNotFound
	// when no such principal exists, and access.ErrLastSiteAdministrator when
	// it ends the last site administrator (see Store).
	DeletePrincipal(ctx context.Context, id string, e audit.Entry) error
	// EnablePrincipal clears the disable time of the principal with the ID
	// id and sets its update time to e.At. It returns ErrNotFound when no
	// such principal exists, and ErrNotDisabled when it is not disabled.
	EnablePrincipal(ctx context.Context, id string, e audit.Entry) error
	// DisablePrincipal sets the disable time of the principal with the ID id
	// to e.At and sets its update time to e.At. It only narrows access: the
	// grants, memberships and tokens of the principal stay, and the principal
	// cannot use them until EnablePrincipal clears the disable time. The
	// write runs in maintenance mode, as a revocation does. It returns
	// ErrNotFound when no such principal exists, ErrDisabled when it is
	// disabled, access.ErrLastSiteAdministrator when it disables the last
	// site administrator (see Store), and ErrNoWorkingBreakGlass when it disables
	// the last enabled break-glass account. The store checks this in the
	// transaction of the write, so two concurrent disables of the last two
	// accounts disable one.
	DisablePrincipal(ctx context.Context, id string, e audit.Entry) error
}

// NamespaceRecordStore keeps the records of the principals of a namespace.
//
// Each write takes the fence of the namespace of the principal and checks it
// in the transaction of the write, and appends its audit entry in that
// namespace in the same transaction: both commit, or neither. A write returns
// ErrInvalid when the fence or the entry names another namespace, and
// namespace.ErrNotFound or namespace.ErrFenced from the fence check. The
// writes of RecordStore act on the principals of the installation only: they
// answer ErrNotFound for a principal of a namespace, and CreatePrincipal
// answers ErrInvalid for one.
type NamespaceRecordStore interface {
	// CreateNamespacePrincipal persists p, a principal of the namespace
	// p.Namespace. It returns ErrAlreadyExists when the ID is taken, or when
	// a principal that exists has the subject: a subject is unique on the
	// whole installation.
	CreateNamespacePrincipal(ctx context.Context, p Principal, fence namespace.Fence, e audit.Entry) error
	// ListNamespacePrincipals returns the principals of the namespace ns
	// that exist, ordered by subject: the rows of q, with the subject as the
	// key.
	ListNamespacePrincipals(ctx context.Context, ns namespace.Name, q Query) ([]Principal, int64, error)
	// UpdateNamespacePrincipal replaces the display name and the update time
	// of the principal with the ID p.ID in the namespace p.Namespace. It
	// returns ErrNotFound when no such principal exists in that namespace.
	UpdateNamespacePrincipal(ctx context.Context, p Principal, fence namespace.Fence, e audit.Entry) error
	// DeleteNamespacePrincipal ends the principal with the ID id in the
	// namespace of fence, at e.At, with its grants and the grants on it. It
	// returns ErrNotFound when no such principal exists in that namespace.
	DeleteNamespacePrincipal(ctx context.Context, id string, fence namespace.Fence, e audit.Entry) error
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
	// at e.At. It returns ErrNotFound when no such group exists, and
	// access.ErrLastSiteAdministrator when it ends the last site
	// administrator (see Store).
	DeleteGroup(ctx context.Context, id string, e audit.Entry) error
}

// GroupReads reads, in the transaction of a membership write, what the
// members of a group can do through the grants of the group. A store gives
// it to a MemberAudit.
type GroupReads interface {
	// ActsAs returns, sorted and each once, what the members of the group
	// groupID can act as through its current grants, as the access review
	// names it: "principal:<id>" for one service principal, the name of a
	// namespace for each service principal of that namespace, and
	// "@server" for every service principal.
	ActsAs(ctx context.Context, groupID string) ([]string, error)
}

// MemberAudit returns the changes that a membership write adds to its
// audit entry, from the reads of the transaction of the write.
type MemberAudit func(ctx context.Context, r GroupReads) ([]audit.Change, error)

// MemberStore keeps the memberships of principals in groups. A membership
// starts at AddedAt and ends at a removal. An ended membership stays for
// the history, and the principal can join the group again.
type MemberStore interface {
	// AddMember persists m. It returns ErrNotFound when the group or the
	// principal does not exist, ErrAlreadyExists when the principal is a
	// member of the group, and ErrManagedByDirectory when the source of the
	// group is not SourceLocal. When more is not nil, it runs more in the
	// transaction of the write, after the write holds the lock that the
	// writes of grants and roles take, and adds the changes that more
	// returns to e before it appends e. An error from more refuses the
	// write.
	AddMember(ctx context.Context, m Membership, e audit.Entry, more MemberAudit) error
	// RemoveMember ends the membership of the principal principalID in the
	// group groupID, at e.At. It returns ErrNotFound when no such
	// membership exists, ErrManagedByDirectory when the source of the
	// group is not SourceLocal, and access.ErrLastSiteAdministrator when it
	// ends the last site administrator (see Store).
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

// Guard checks that a caller may change who holds a set of grants.
// A membership change gives or takes the grants of the group, the delete
// of a principal or a group revokes its grants, and the enable of a principal
// restores its grants. So the caller must hold, on
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

// AdminOnlyGuard is the Guard of an installation that keeps no grants. It
// allows an administrator and refuses every other caller, because without
// grants nobody else holds an action on any object.
type AdminOnlyGuard struct{}

var _ Guard = AdminOnlyGuard{}

// CheckGroupGrants allows only an administrator.
func (AdminOnlyGuard) CheckGroupGrants(_ context.Context, caller identity.Principal, _ string) error {
	return adminOnly(caller)
}

// CheckPrincipalGrants allows only an administrator.
func (AdminOnlyGuard) CheckPrincipalGrants(_ context.Context, caller identity.Principal, _ string) error {
	return adminOnly(caller)
}

func adminOnly(caller identity.Principal) error {
	if caller.Admin {
		return nil
	}

	return fmt.Errorf(
		"%w: only an administrator changes who holds grants when the installation keeps none",
		identity.ErrDenied,
	)
}
