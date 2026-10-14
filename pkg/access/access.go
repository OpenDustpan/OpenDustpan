// Package access decides what a principal can do on an object, from roles
// and grants (ADR 52).
//
// A grant is a tuple: one subject, one role and one object. The subject is a
// principal, or a group, which stands for each of its members. The object is
// the installation, a namespace, a collection or a unit. A grant applies to
// its object and to every object that its object holds, along the fixed
// chain installation > namespace > collection > unit (ADR 52, decision 5).
// Nothing else inherits: groups do not nest, and roles do not hold roles.
//
// A role is a named set of actions. Each action is in exactly one family:
//
//   - operator: the operation of the installation (backups, restores,
//     maintenance, metrics),
//   - administration: the principals, groups, roles and grants of the
//     installation, and namespace create,
//   - data: every action inside a namespace, its access included.
//
// No family holds an action of another, so a grant of an operator role on
// the installation gives no data action in any namespace. The inputs of a
// decision are the current grants, the current memberships, the record of
// the principal and the collection of a unit. Only the administration and
// data actions that manage them change them: no label, name or tag that a
// person can set on a unit takes part.
//
// Nothing is erased: a revoke ends a grant, and the delete of a custom role
// ends the role. Each change writes its audit entry in the transaction of
// the change.
package access

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Sentinel errors returned by this package and by Store implementations.
var (
	// ErrNotFound reports a role or a grant that does not exist.
	ErrNotFound = errors.New("not found")
	// ErrAlreadyExists reports a role name or a grant that exists.
	ErrAlreadyExists = errors.New("already exists")
	// ErrInvalid reports a role or a grant that fails validation.
	ErrInvalid = errors.New("invalid role or grant")
	// ErrBuiltIn reports a change of a built-in role.
	ErrBuiltIn = errors.New("built-in role")
	// ErrInUse reports a delete of a custom role that a grant holds.
	ErrInUse = errors.New("role in use")
	// ErrEscalation reports a grant that gives more than the granter holds
	// on its object.
	ErrEscalation = errors.New("grant gives more than the granter holds")
	// ErrMissingDependency reports a Config without a required port.
	ErrMissingDependency = errors.New("missing dependency")
)

// Family names the group of actions that an action belongs to.
type Family string

// Families of actions. No action is in two families.
const (
	// FamilyOperator holds the operation of the installation.
	FamilyOperator Family = "operator"
	// FamilyAdministration holds the principals, groups, roles and grants
	// of the installation, and namespace create.
	FamilyAdministration Family = "administration"
	// FamilyData holds every action inside a namespace.
	FamilyData Family = "data"
	// FamilySelf holds the actions that every signed-in principal can do:
	// read its own record and read the role catalog. No grant gives them.
	FamilySelf Family = "self"
)

// families classifies every action that the core knows. An action that is
// not here belongs to no family: only an administrator can do it, so a new
// action fails closed until it is classified.
//
//nolint:gochecknoglobals // A constant table of actions; Go has no constant maps.
var families = map[identity.Action]Family{
	identity.ActionServerBackup:      FamilyOperator,
	identity.ActionServerRestore:     FamilyOperator,
	identity.ActionServerMaintenance: FamilyOperator,
	identity.ActionServerObserve:     FamilyOperator,

	identity.ActionServerReadPrincipals:   FamilyAdministration,
	identity.ActionServerManagePrincipals: FamilyAdministration,
	identity.ActionServerManageGroups:     FamilyAdministration,
	identity.ActionServerManageRoles:      FamilyAdministration,
	identity.ActionServerManageAccess:     FamilyAdministration,
	identity.ActionServerCreateNamespace:  FamilyAdministration,
	identity.ActionServerReadAccess:       FamilyAdministration,
	// The access tokens of other principals (ADR 53, decision 3).
	identity.ActionServerReadTokens:          FamilyAdministration,
	identity.ActionServerManageTokens:        FamilyAdministration,
	identity.ActionServerManageServiceTokens: FamilyAdministration,

	identity.ActionNamespaceRead:          FamilyData,
	identity.ActionNamespaceAdmin:         FamilyData,
	identity.ActionNamespaceReadAudit:     FamilyData,
	identity.ActionNamespaceSetOverrides:  FamilyData,
	identity.ActionNamespaceManageAccess:  FamilyData,
	identity.ActionNamespaceReadAccess:    FamilyData,
	identity.ActionCollectionRead:         FamilyData,
	identity.ActionCollectionCreate:       FamilyData,
	identity.ActionCollectionUpdate:       FamilyData,
	identity.ActionCollectionDelete:       FamilyData,
	identity.ActionCollectionCreateUnit:   FamilyData,
	identity.ActionCollectionManageAccess: FamilyData,
	identity.ActionCollectionReadAccess:   FamilyData,
	identity.ActionUnitRead:               FamilyData,
	identity.ActionUnitWrite:              FamilyData,
	identity.ActionUnitManage:             FamilyData,
	identity.ActionUnitEncrypt:            FamilyData,
	identity.ActionUnitDecrypt:            FamilyData,
	identity.ActionUnitWriteForced:        FamilyData,
	identity.ActionUnitWriteUnlocked:      FamilyData,
	identity.ActionUnitSetOverrides:       FamilyData,
	identity.ActionUnitReadSecrets:        FamilyData,
	identity.ActionUnitMove:               FamilyData,
	identity.ActionUnitManageAccess:       FamilyData,
	identity.ActionUnitRestore:            FamilyData,
	identity.ActionUnitPurge:              FamilyData,
	identity.ActionUnitReadAccess:         FamilyData,

	identity.ActionCallerRead:     FamilySelf,
	identity.ActionRoleRead:       FamilySelf,
	identity.ActionTokenManageOwn: FamilySelf,
}

// FamilyOf returns the family of action, and false when the core does not
// classify it.
func FamilyOf(action identity.Action) (Family, bool) {
	f, ok := families[action]

	return f, ok
}

// Actions returns every classified action of family f, sorted.
func Actions(f Family) []identity.Action {
	var out []identity.Action

	for a, fam := range families {
		if fam == f {
			out = append(out, a)
		}
	}

	sortActions(out)

	return out
}

// SubjectKind names what a grant subject is.
type SubjectKind string

// Subject kinds.
const (
	// SubjectPrincipal is one principal.
	SubjectPrincipal SubjectKind = "principal"
	// SubjectGroup is each member of one group.
	SubjectGroup SubjectKind = "group"
)

// Subject is the holder of a grant: a principal, or a group that stands for
// each of its members. Its text form is "principal:<id>" or
// "group:<id>#member".
type Subject struct {
	Kind SubjectKind
	ID   string
}

// String returns the text form of the subject.
func (s Subject) String() string {
	if s.Kind == SubjectGroup {
		return "group:" + s.ID + "#member"
	}

	return "principal:" + s.ID
}

// ParseSubject parses the text form of a subject: "principal:<id>" or
// "group:<id>#member". It returns ErrInvalid for any other text.
func ParseSubject(s string) (Subject, error) {
	if id, ok := strings.CutPrefix(s, "principal:"); ok && validSubjectID(id) {
		return Subject{Kind: SubjectPrincipal, ID: id}, nil
	}

	if rest, ok := strings.CutPrefix(s, "group:"); ok {
		if id, member := strings.CutSuffix(rest, "#member"); member && validSubjectID(id) {
			return Subject{Kind: SubjectGroup, ID: id}, nil
		}
	}

	return Subject{}, fmt.Errorf(`%w: subject %q: write "principal:<id>" or "group:<id>#member"`, ErrInvalid, s)
}

// validSubjectID reports whether id can be the ID of a subject: 1 to 64
// letters, digits, '-', '_' and '.'.
func validSubjectID(id string) bool {
	return ident.Valid(id, maxSubjectIDLength)
}

// maxSubjectIDLength bounds the ID of a grant subject, in bytes.
const maxSubjectIDLength = 64

// ObjectKind names the type of a grant object.
type ObjectKind string

// Object kinds, outermost first: each holds the next.
const (
	ObjectInstallation ObjectKind = "installation"
	ObjectNamespace    ObjectKind = "namespace"
	ObjectCollection   ObjectKind = "collection"
	ObjectUnit         ObjectKind = "unit"
)

// KindOf returns the object kind of obj, and false for the caller object.
func KindOf(obj identity.Object) (ObjectKind, bool) {
	switch {
	case obj.IsServer():
		return ObjectInstallation, true
	case obj.IsCaller():
		return "", false
	case obj.IsUnit():
		return ObjectUnit, true
	case obj.IsCollection():
		return ObjectCollection, true
	case obj.Namespace() != "":
		return ObjectNamespace, true
	default:
		return "", false
	}
}

// Grant is one tuple: the subject holds the role on the object, and on every
// object that the object holds. ID is opaque, with the prefix "grt-". A grant
// on a unit follows the stable ID of the unit (Object.UnitID, ADR 19), not
// its name: a new unit with the name of a deleted one does not get it. Its
// Object holds the name of the unit at the time of the grant, until the
// Service reads the current name.
// Condition is empty: it is room for the attribute conditions of ADR 52
// decision 9, and no decision reads it yet. CreatedBy names the subject of
// the principal that made the grant.
type Grant struct {
	CreatedAt time.Time
	Condition *string
	Object    identity.Object
	ID        string
	Role      string
	CreatedBy string
	Subject   Subject
}

// Role is a named set of actions. A built-in role has a fixed ID, which is
// its name. A custom role has an ID with the prefix "rol-".
type Role struct {
	CreatedAt   time.Time
	UpdatedAt   time.Time
	ID          string
	Name        string
	Description string
	Actions     []identity.Action
	BuiltIn     bool
}

// Has reports whether r holds action.
func (r Role) Has(action identity.Action) bool {
	return slices.Contains(r.Actions, action)
}

// GrantQuery is a page of the grants on one object as a store reads it:
// the grants after the subject AfterSubject and the role AfterRole, in the
// order of subject then role, at most Limit of them.
type GrantQuery struct {
	AfterSubject string
	AfterRole    string
	Limit        int
}

// GrantStore keeps the grants.
//
// Each write takes the fence of the namespace of the grant and the audit
// entry of the change, and appends the entry in the same transaction as the
// write (ADR 60, fail closed): both commit, or neither. A grant on the
// installation takes the zero fence, and its entry is in the scope
// audit.ServerScope. A grant on a namespace, a collection or a unit takes the
// fence of its namespace, and its entry is in that namespace, so the store
// checks the fence in the transaction of the write. A write returns
// audit.ErrInvalidEntry when the entry fails validation or names another
// scope, ErrInvalid when the fence is not the fence of the grant, and
// namespace.ErrNotFound or namespace.ErrFenced from the fence check. Such a
// write changes nothing. The time of the change is e.At, and its author is
// e.Subject. A revoke ends a grant and keeps its row for the history.
type GrantStore interface {
	// CreateGrant persists g. It returns ErrAlreadyExists when a current
	// grant of the same subject, role and object exists, or when the ID is
	// taken, and ErrInvalid for an object that takes no grant or a unit with
	// no stable ID. It returns ErrNotFound when the record of the subject,
	// or the custom role, has ended: the write reads them in its own
	// transaction, so a delete that commits first wins. A subject or a role
	// with no record at all is the Service's to check.
	CreateGrant(ctx context.Context, g Grant, fence namespace.Fence, e audit.Entry) error
	// GetGrant returns the current grant with the ID id, or ErrNotFound.
	GetGrant(ctx context.Context, id string) (Grant, error)
	// RevokeGrant ends the current grant with the ID id at e.At. fence
	// names the namespace of the grant (the zero fence for the
	// installation). It returns ErrNotFound when no such grant exists in
	// that namespace.
	RevokeGrant(ctx context.Context, id string, fence namespace.Fence, e audit.Entry) error
	// ListGrantsOn returns one page of the current grants on obj, ordered
	// by subject then role, and the count of every current grant on obj. A
	// unit obj matches by its stable ID, and returns ErrInvalid with none.
	ListGrantsOn(ctx context.Context, obj identity.Object, q GrantQuery) ([]Grant, int64, error)
	// GrantsOn returns every current grant that one of subjects holds on
	// one of objects, in one query. An empty list of subjects or objects
	// returns none. A unit matches by its stable ID: a unit object with none
	// matches no grant.
	GrantsOn(ctx context.Context, subjects []Subject, objects []identity.Object) ([]Grant, error)
	// GrantsWithRoles returns every current grant that one of subjects
	// holds with one of roles, on any object, in one query. It serves
	// list-objects.
	GrantsWithRoles(ctx context.Context, subjects []Subject, roles []string) ([]Grant, error)
	// CreateNamespaceWithGrant persists ns, g and e in one transaction: the
	// creator of a namespace holds the namespace-admin role on it from the
	// start. A grant with no ID stores the namespace and the entry alone. It
	// returns namespace.ErrAlreadyExists when the name is taken.
	CreateNamespaceWithGrant(ctx context.Context, ns namespace.Namespace, g Grant, e audit.Entry) error
}

// GrantLookups reads the current grants that a change of who holds them
// must check (ADR 52): the grants of one subject, and whether a role is
// granted below the installation.
type GrantLookups interface {
	// GrantsOf returns every current grant of subject, on any object, in
	// one query. It serves the checks of a membership change and of a
	// delete.
	GrantsOf(ctx context.Context, subject Subject) ([]Grant, error)
	// RoleGrantedBelowInstallation reports whether a current grant gives
	// the role with the ID role on an object that is not the installation.
	RoleGrantedBelowInstallation(ctx context.Context, role string) (bool, error)
}

// RoleStore keeps the custom roles. The built-in roles live in code. The
// writes take an audit entry as the writes of GrantStore do. A delete ends
// a role and keeps its row for the history.
type RoleStore interface {
	// CreateRole persists r. It returns ErrAlreadyExists when the ID is
	// taken, or when a role that exists has the name without regard to
	// case.
	CreateRole(ctx context.Context, r Role, e audit.Entry) error
	// GetRole returns the custom role with the ID id, or ErrNotFound.
	GetRole(ctx context.Context, id string) (Role, error)
	// GetRoles returns the custom roles with the IDs ids that exist, in one
	// query. An ID with no role is left out.
	GetRoles(ctx context.Context, ids []string) ([]Role, error)
	// ListRoles returns every custom role, ordered by name without regard
	// to case.
	ListRoles(ctx context.Context) ([]Role, error)
	// UpdateRole replaces the name, the description, the actions and the
	// update time of the custom role with the ID r.ID. It returns
	// ErrNotFound when no such role exists, and ErrAlreadyExists when
	// another role has the name without regard to case.
	UpdateRole(ctx context.Context, r Role, e audit.Entry) error
	// DeleteRole ends the custom role with the ID id at e.At. It returns
	// ErrNotFound when no such role exists, and ErrInUse when a current
	// grant holds it.
	DeleteRole(ctx context.Context, id string, e audit.Entry) error
}

// Units resolves the stable ID of a unit (ADR 19), so that a grant follows
// the unit and not its name. The SQL stores meet it from the unit tables of
// each tool. The core keeps the tool and the name of a unit opaque.
type Units interface {
	// UnitID returns the stable ID of the unit obj, and false when no such
	// unit exists or obj is not a unit.
	UnitID(ctx context.Context, obj identity.Object) (string, bool, error)
	// UnitObjects returns the current reference of each unit with an ID in
	// ids, with its ID set, in one read for each tool. An ID with no unit
	// is left out.
	UnitObjects(ctx context.Context, ids []string) (map[string]identity.Object, error)
}

// Store is the persistence port of roles and grants.
type Store interface {
	GrantStore
	GrantLookups
	RoleStore
}
