package access

import (
	"context"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
)

// ReviewScope names the grants of an access-review export (ADR 52,
// decision 10), as a store reads them. A grant is in the scope when:
//
//   - All is true, or
//   - its object is in the namespace Namespace (the namespace itself, its
//     collections and its units), or
//   - its object is Object (a unit matches by its stable ID), or
//   - its object is one of Containers and its role is one of
//     ContainerRoles, or, in a history, any custom role (a deleted one
//     included): the actions of a role at the time of a grant are not
//     kept, so a history keeps every custom role.
//
// The service fills Containers with the objects that hold the object of
// the export, and ContainerRoles with the roles that give a data action or
// an access-management action, so that an export answers who can access
// its object, and who can give access to it. TokenNamespace is the
// namespace whose grants count with TokenNamespaceRoles: the export of a
// principal of a namespace lists the holders of the token action on its
// namespace there. Empty means none.
type ReviewScope struct {
	Object              identity.Object
	Namespace           string
	Containers          []string
	ContainerRoles      []string
	TokenNamespace      string
	TokenNamespaceRoles []string
	All                 bool
}

// ReviewKey is the position of a row in the order of an export: the time of
// the grant, the grant ID, and the start and the principal of the
// membership for a row through a group (0 and "" for any other row).
type ReviewKey struct {
	ID         string
	MemberID   string
	AtMs       int64
	MemberAtMs int64
}

// ReviewQuery is one page of an access-review export as a store reads it.
//
// Subject, when it is not empty, keeps the grants of that subject (the text
// form). Principal, when it is not empty, is the ID of the principal of
// Subject: the page then also holds the grants of each group of the
// principal, one row for each membership that overlaps the grant (and the
// window of a history). Expand, with no Principal, puts each member of a
// group in its own row in the same way. A group with no member in the
// window keeps one row with no membership.
//
// With History false, the page is a snapshot at AsOfMs: the grants and the
// memberships that were current at that time. With History true, it holds
// the grants whose life overlaps the window [FromMs, ToMs), ended ones
// included.
//
// The page holds at most Limit rows after the key After, in the order of
// ReviewKey. Count asks for the count of every row of the query as well.
type ReviewQuery struct {
	Scope     ReviewScope
	Subject   string
	Principal string
	After     ReviewKey
	AsOfMs    int64
	FromMs    int64
	ToMs      int64
	Limit     int
	History   bool
	Expand    bool
	Count     bool
}

// CallerGrantsQuery is one page of the current grants of a caller as a
// store reads them: the grants of the principal Principal, and the grants
// of each group in Groups through the membership of the principal.
//
// The page is a snapshot at AsOfMs of the grants that are not revoked: a
// grant made after AsOfMs is not in it, and neither is a grant revoked
// since. A grant of a group is in it only while the membership of the
// principal is current at AsOfMs. The page holds at most Limit rows after
// the key After, in the order of ReviewKey. Count asks for the count of
// every row of the query as well.
type CallerGrantsQuery struct {
	Groups    []string
	Principal string
	After     ReviewKey
	AsOfMs    int64
	Limit     int
	Count     bool
}

// ReviewRow is one row of an access-review export: a grant, how it ended,
// the name of its subject and, for a row through a group, the membership.
// LastAccessTracked is true on a row of a principal: on a grant whose
// subject is a principal, or on a row read through a group, for the member
// in Via. LastAccessAt is the latest last-use time over every access token
// and every console session of that principal, and nil when the principal
// never used a token or a session.
type ReviewRow struct {
	RevokedAt *time.Time
	// LastAccessAt is the latest last-use time of the access tokens and the
	// console sessions of the principal of the row, and nil when no token
	// and no session was ever used.
	LastAccessAt *time.Time
	// MemberCount is the count of the members of a group subject at the
	// time of a snapshot. It is nil for a principal subject.
	MemberCount *int64
	// Via is the membership of a principal in the group subject, for a row
	// that a principal filter or Expand reads through a group.
	Via *Membership
	// SubjectName is the subject of a principal or the name of a group. It
	// is empty when the record does not exist.
	SubjectName string
	RevokedBy   string
	Grant       Grant
	// LastAccessTracked is true on a row of a principal, and false on a
	// group row with no Via.
	LastAccessTracked bool
}

// Membership is the time that a principal was a member of a group.
// RemovedAt is nil while the membership lasts.
type Membership struct {
	AddedAt   time.Time
	RemovedAt *time.Time
	// PrincipalID and PrincipalSubject name the member.
	PrincipalID      string
	PrincipalSubject string
}

// Key returns the position of r in the order of an export.
func (r ReviewRow) Key() ReviewKey {
	k := ReviewKey{ID: r.Grant.ID, MemberID: "", AtMs: r.Grant.CreatedAt.UnixMilli(), MemberAtMs: 0}
	if r.Via != nil {
		k.MemberAtMs, k.MemberID = r.Via.AddedAt.UnixMilli(), r.Via.PrincipalID
	}

	return k
}

// UnitState is a unit that a grant names, as an export shows it: its
// current reference, or its reference at the time of its delete.
type UnitState struct {
	Object  identity.Object
	Deleted bool
}

// ReviewStore reads access-review exports.
type ReviewStore interface {
	// ReviewGrants returns one page of the rows of q and, when q.Count is
	// true, the count of every row of q without its page. It reads the
	// page and the count from one snapshot of the store.
	ReviewGrants(ctx context.Context, q ReviewQuery) ([]ReviewRow, int64, error)
	// CallerGrants returns one page of the rows of q and, when q.Count is
	// true, the count of every row of q, from one snapshot of the store,
	// as ReviewGrants does. It reads the grants of each holder of q from
	// the index of the holders of current grants, so its cost follows the
	// grants of the caller, not the grants of the installation. A row of a
	// group grant holds the membership in Via, and the member count of the
	// group at q.AsOfMs.
	CallerGrants(ctx context.Context, q CallerGrantsQuery) ([]ReviewRow, int64, error)
	// ReviewUnits returns the unit of each stable ID in ids that the store
	// keeps, a deleted one included. An ID with no unit (a purged one, or an
	// ID of no unit) is not in the map.
	ReviewUnits(ctx context.Context, ids []string) (map[string]UnitState, error)
	// LastAccess returns, for each principal ID in principalIDs, the latest
	// last-use time of its access tokens and its console sessions. A
	// principal with no used token and no session is not in the map. It
	// reads only the given IDs.
	LastAccess(ctx context.Context, principalIDs []string) (map[string]time.Time, error)
}
