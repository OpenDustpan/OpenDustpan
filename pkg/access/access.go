// Package access decides what a principal can do on an object, from roles
// and grants (ADR 52).
//
// A grant is a tuple: one subject, one role and one object. The subject is a
// principal, or a group, which stands for each of its members. The object is
// the installation, a namespace, a collection, a unit or a service principal.
// A grant applies to
// its object and to every object that its object holds, along the fixed
// chain installation > namespace > collection > unit (ADR 52, decision 5).
// Nothing else inherits: groups do not nest, and roles do not hold roles.
//
// A role is a named set of actions. Each action is in exactly one family:
//
//   - operator: the operation of the installation (backups, restores,
//     maintenance, metrics, the installation audit log),
//   - administration: the principals, groups, roles and grants of the
//     installation, and namespace create,
//   - data: every action inside a namespace, its access included,
//   - delegation: the issue of the access tokens of a service principal.
//
// The installation holds every principal. A grant on a service principal
// gives the delegation actions of its role on that principal only.
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
	// ErrCollectionMissing reports that the collection of a grant check or a
	// move does not exist. It always comes with ErrNotFound.
	ErrCollectionMissing = errors.New("collection missing")
	// ErrUnitMissing reports that the unit of a grant check or a move does not
	// exist. It always comes with ErrNotFound.
	ErrUnitMissing = errors.New("unit missing")
	// ErrAlreadyExists reports a role name or a grant that exists.
	ErrAlreadyExists = errors.New("already exists")
	// ErrInvalid reports a role or a grant that fails validation.
	ErrInvalid = errors.New("invalid role or grant")
	// ErrBuiltIn reports a change of a built-in role.
	ErrBuiltIn = errors.New("built-in role")
	// ErrInUse reports a delete of a custom role that a grant holds.
	ErrInUse = errors.New("role in use")
	// ErrLastSiteAdministrator reports a change that would leave the
	// installation with no site administrator: the delete or the disable of
	// the last one, or the end of the last grant, membership or group that
	// gives the role.
	ErrLastSiteAdministrator = errors.New(
		"cannot remove the last site administrator: give the site-administrator role to a second person first",
	)
	// ErrEscalation reports a grant that gives more than the granter holds
	// on its object.
	ErrEscalation = errors.New("grant gives more than the granter holds")
	// ErrUnitMoved reports a grant or a revoke on a unit that moved to
	// another collection after the Service read the collection of the unit.
	// The request can be sent again.
	ErrUnitMoved = errors.New("the unit moved to another collection")
	// ErrMissingDependency reports a Config without a required port.
	ErrMissingDependency = errors.New("missing dependency")
	// ErrUnknownAction reports an action that no family classifies. The
	// protected check of a group returns it, so that a new action fails
	// closed until it is classified.
	ErrUnknownAction = errors.New("unknown action")
)

// Family names the group of actions that an action belongs to.
type Family string

// Families of actions. No action is in two families.
const (
	// FamilyOperator holds the operation of the installation, and its audit log.
	FamilyOperator Family = "operator"
	// FamilyAdministration holds the principals, groups, roles and grants
	// of the installation, and namespace create.
	FamilyAdministration Family = "administration"
	// FamilyData holds every action inside a namespace.
	FamilyData Family = "data"
	// FamilyDelegation holds the actions that let a principal act as a
	// service principal: the issue of its access tokens.
	FamilyDelegation Family = "delegation"
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
	identity.ActionServerBackup:  FamilyOperator,
	identity.ActionServerRestore: FamilyOperator,
	// The deep check of the blob store: it adds load to the store and writes
	// throwaway objects under a prefix of its own.
	identity.ActionInstallationStoreCheck: FamilyOperator,
	identity.ActionServerMaintenance:      FamilyOperator,
	identity.ActionServerObserve:          FamilyOperator,
	// The version rows of the server processes: the list, and the delete of
	// the row of a process that died.
	identity.ActionServerReadInstances:  FamilyOperator,
	identity.ActionServerDeleteInstance: FamilyOperator,
	identity.ActionServerReadAudit:      FamilyOperator,
	// The settings resource of the installation.
	identity.ActionServerReadSettings:   FamilyOperator,
	identity.ActionServerManageSettings: FamilyOperator,
	// A change of the audit chain. Off also needs the administration
	// action below.
	identity.ActionServerSetAuditChain: FamilyOperator,
	// The GitHub App of the installation and the map of its installations: the
	// operator registers the app and assigns each installation to a namespace.
	identity.ActionServerReadGitHost:   FamilyOperator,
	identity.ActionServerManageGitHost: FamilyOperator,

	identity.ActionServerReadPrincipals:    FamilyAdministration,
	identity.ActionServerManagePrincipals:  FamilyAdministration,
	identity.ActionServerManageGroups:      FamilyAdministration,
	identity.ActionServerManageRoles:       FamilyAdministration,
	identity.ActionServerManageAccess:      FamilyAdministration,
	identity.ActionServerCreateNamespace:   FamilyAdministration,
	identity.ActionServerReadAccess:        FamilyAdministration,
	identity.ActionServerDisableAuditChain: FamilyAdministration,
	// The access tokens of other principals (ADR 53, decision 3).
	identity.ActionServerReadTokens:   FamilyAdministration,
	identity.ActionServerManageTokens: FamilyAdministration,
	// A custom role may still hold this deprecated action, so it keeps its
	// family. No route declares it.
	identity.ActionServerManageServiceTokens:  FamilyAdministration, //nolint:staticcheck // Kept classified for stored roles.
	identity.ActionServerManageTokenPolicy:    FamilyAdministration,
	identity.ActionServerManageSignIn:         FamilyAdministration,
	identity.ActionServerRestoreAccess:        FamilyAdministration,
	identity.ActionNamespaceManageIPAllowlist: FamilyAdministration,

	identity.ActionPrincipalManageTokens: FamilyDelegation,

	identity.ActionNamespaceRead:                  FamilyData,
	identity.ActionNamespaceAdmin:                 FamilyData,
	identity.ActionNamespaceReadAudit:             FamilyData,
	identity.ActionNamespaceSetOverrides:          FamilyData,
	identity.ActionNamespaceManageAccess:          FamilyData,
	identity.ActionNamespaceReadAccess:            FamilyData,
	identity.ActionNamespaceManagePrincipals:      FamilyData,
	identity.ActionNamespaceManagePrincipalTokens: FamilyData,
	identity.ActionNamespaceManageTrustRules:      FamilyData,
	identity.ActionNamespaceReadGates:             FamilyData,
	identity.ActionNamespaceManageGates:           FamilyData,
	identity.ActionNamespaceReadSettings:          FamilyData,
	identity.ActionNamespaceManageSettings:        FamilyData,
	// The connections of a namespace to a Git host.
	identity.ActionNamespaceReadGitHostConnections:   FamilyData,
	identity.ActionNamespaceManageGitHostConnections: FamilyData,
	identity.ActionNamespaceUseGitHostConnection:     FamilyData,
	identity.ActionNamespaceReadPlacementRules:       FamilyData,
	identity.ActionNamespaceManagePlacementRules:     FamilyData,
	identity.ActionCollectionRead:                    FamilyData,
	identity.ActionCollectionCreate:                  FamilyData,
	identity.ActionCollectionUpdate:                  FamilyData,
	identity.ActionCollectionDelete:                  FamilyData,
	identity.ActionCollectionCreateUnit:              FamilyData,
	identity.ActionCollectionManageAccess:            FamilyData,
	identity.ActionCollectionReadAccess:              FamilyData,
	identity.ActionUnitRead:                          FamilyData,
	identity.ActionUnitRunPlan:                       FamilyData,
	identity.ActionUnitWrite:                         FamilyData,
	identity.ActionUnitManage:                        FamilyData,
	identity.ActionUnitEncrypt:                       FamilyData,
	identity.ActionUnitDecrypt:                       FamilyData,
	identity.ActionUnitWriteForced:                   FamilyData,
	identity.ActionUnitWriteUnlocked:                 FamilyData,
	identity.ActionUnitSetOverrides:                  FamilyData,
	identity.ActionUnitReadSecrets:                   FamilyData,
	identity.ActionUnitReadOutputs:                   FamilyData,
	identity.ActionUnitManageSharing:                 FamilyData,
	identity.ActionUnitMove:                          FamilyData,
	identity.ActionUnitManageAccess:                  FamilyData,
	identity.ActionUnitRestore:                       FamilyData,
	identity.ActionUnitLock:                          FamilyData,
	identity.ActionUnitPurge:                         FamilyData,
	identity.ActionUnitReadAccess:                    FamilyData,
	identity.ActionUnitSetContext:                    FamilyData,
	identity.ActionNamespaceSetContext:               FamilyData,
	identity.ActionProfileRead:                       FamilyData,
	identity.ActionProfileWrite:                      FamilyData,
	identity.ActionProfileOpen:                       FamilyData,
	identity.ActionProfileManage:                     FamilyData,
	identity.ActionNamespaceManageNotifications:      FamilyData,
	identity.ActionViewCreate:                        FamilyData,
	identity.ActionViewRead:                          FamilyData,
	identity.ActionViewDelete:                        FamilyData,
	identity.ActionRegistryRead:                      FamilyData,
	identity.ActionRegistryPublish:                   FamilyData,
	identity.ActionRegistryManageKeys:                FamilyData,
	identity.ActionNamespaceManageIssuer:             FamilyData,

	identity.ActionCallerRead:               FamilySelf,
	identity.ActionRoleRead:                 FamilySelf,
	identity.ActionTokenManageOwn:           FamilySelf,
	identity.ActionCallerManagePreferences:  FamilySelf,
	identity.ActionCallerManageSignIn:       FamilySelf,
	identity.ActionCallerManageGitHostLinks: FamilySelf,
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

// SensitiveActions returns every sensitive action, sorted: each action of
// the operator, the administration and the delegation families, except the
// read-only server actions: each server:read-* action. A read-only action
// cannot raise a privilege, so it is not sensitive. server:observe and each
// manage, backup, restore and maintenance action stay sensitive. The list
// comes from the families, so a new action of one of them is in the list
// with no other change.
func SensitiveActions() []identity.Action {
	var out []identity.Action

	for _, family := range []Family{FamilyOperator, FamilyAdministration, FamilyDelegation} {
		for _, action := range Actions(family) {
			if readOnlyServer(action) {
				continue
			}

			out = append(out, action)
		}
	}

	sortActions(out)

	return out
}

// IsSensitive reports whether action is a sensitive action: its family is
// the operator family, the administration family or the delegation family,
// and it is not a read-only server action: each server:read-* action. A
// read-only action cannot raise a privilege, so it is not sensitive. It
// returns false for an action that no family classifies: an unknown action
// is not sensitive. A caller that must fail closed on an unknown action
// reads FamilyOf itself, as the protected check of a group does.
func IsSensitive(action identity.Action) bool {
	family, ok := FamilyOf(action)

	return ok && sensitiveFamily(family) && !readOnlyServer(action)
}

// sensitiveFamily reports whether a family holds sensitive actions: the
// operator family, the administration family and the delegation family.
func sensitiveFamily(family Family) bool {
	return family == FamilyOperator || family == FamilyAdministration ||
		family == FamilyDelegation
}

// readOnlyServer reports whether action is a read-only server action: it
// reads the principals, the access review, the access tokens, the audit
// log, the settings, the server instances or the Git host registration of
// the installation. A read-only action cannot raise a privilege, so it is
// not sensitive: a group that holds one is not protected, and the identity
// provider can still manage the group. server:observe and each manage,
// backup, restore and maintenance action stay sensitive.
func readOnlyServer(action identity.Action) bool {
	return slices.Contains([]identity.Action{
		identity.ActionServerReadPrincipals,
		identity.ActionServerReadAccess,
		identity.ActionServerReadTokens,
		identity.ActionServerReadAudit,
		identity.ActionServerReadSettings,
		identity.ActionServerReadInstances,
		identity.ActionServerReadGitHost,
	}, action)
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

// Object kinds. The installation holds every namespace and every
// principal. Each of namespace, collection and unit holds the next.
const (
	ObjectInstallation ObjectKind = "installation"
	ObjectNamespace    ObjectKind = "namespace"
	ObjectCollection   ObjectKind = "collection"
	ObjectUnit         ObjectKind = "unit"
	// ObjectPrincipal is one service principal.
	ObjectPrincipal ObjectKind = "principal"
)

// KindOf returns the object kind of obj, and false for the caller object.
func KindOf(obj identity.Object) (ObjectKind, bool) {
	switch {
	case obj.IsServer():
		return ObjectInstallation, true
	case obj.IsCaller():
		return "", false
	case obj.IsPrincipal():
		return ObjectPrincipal, true
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
// order of subject then role, at most Limit of them. Subject, when it is not
// empty, keeps the grants of that subject (the text form). Role, when it is
// not empty, keeps the grants of that role.
type GrantQuery struct {
	AfterSubject string
	AfterRole    string
	Subject      string
	Role         string
	Limit        int
}

// Holder kinds of a listed grant, next to the principal kinds.
const (
	// HolderGroup is the holder kind of a grant to a group.
	HolderGroup = "group"
)

// Holder names the holder of a grant for a list: the display name of a
// principal (its subject when it has no display name) and its kind (person
// or service), or the name of a group and the kind HolderGroup. Both are
// empty when the record does not exist.
type Holder struct {
	Name string
	Kind string
}

// ListedGrant is one grant of a list, with its holder.
type ListedGrant struct {
	Holder Holder
	Grant  Grant
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
	// taken, and ErrInvalid for an object that takes no grant, a unit with
	// no stable ID, or a subject that is a principal of a namespace and an
	// object outside that namespace. It reads the namespace of the subject
	// in its own transaction. It returns ErrNotFound when the record of the subject,
	// or the custom role, has ended: the write reads them in its own
	// transaction, so a delete that commits first wins. A subject or a role
	// with no record at all is the Service's to check. It runs check, when
	// it is not nil, as a GrantCheck runs, and returns its error.
	CreateGrant(ctx context.Context, g Grant, fence namespace.Fence, e audit.Entry, check GrantCheck) error
	// GetGrant returns the current grant with the ID id, or ErrNotFound.
	GetGrant(ctx context.Context, id string) (Grant, error)
	// RevokeGrant ends the current grant with the ID id at e.At. fence
	// names the namespace of the grant (the zero fence for the
	// installation). It returns ErrNotFound when no such grant exists in
	// that namespace. It runs check, when it is not nil, as a GrantCheck
	// runs, and returns its error. It returns ErrLastSiteAdministrator,
	// and changes nothing, when the end of the grant would leave the
	// installation with no site administrator. The store counts and
	// revokes in one transaction, serialized with the changes of
	// principal.Store that end a site administrator (see the doc of
	// principal.Store).
	RevokeGrant(ctx context.Context, id string, fence namespace.Fence, e audit.Entry, check GrantCheck) error
	// ListGrantsOn returns one page of the current grants on obj that
	// match the filters of q, ordered by subject then role, each with its
	// holder, and the count of every current grant on obj that matches the
	// filters. It reads the holders in the query of the page. A unit obj
	// matches by its stable ID, and returns ErrInvalid with none.
	ListGrantsOn(ctx context.Context, obj identity.Object, q GrantQuery) ([]ListedGrant, int64, error)
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
	// returns namespace.ErrAlreadyExists when the name is taken. With a grant
	// it keeps the entry e in the scope audit.ServerScope and appends the same
	// grant entry in the chain of the new namespace, so the chain starts with
	// the grant of its creator.
	CreateNamespaceWithGrant(ctx context.Context, ns namespace.Namespace, g Grant, e audit.Entry) error
}

// GrantLookups reads the current grants that a change of who holds them
// must check: the grants of one subject or of many, and where a role is
// granted.
type GrantLookups interface {
	// GrantsOf returns every current grant of subject, on any object, in
	// one query. It serves the checks of a membership change and of a
	// delete.
	GrantsOf(ctx context.Context, subject Subject) ([]Grant, error)
	// GrantsOfSubjects returns every current grant of each subject of
	// subjects, on any object, in one query: for each subject, the grants
	// that GrantsOf returns. It returns no grant of a subject that is not in
	// subjects, and none for an empty list. It returns ErrInvalid for more
	// than principal.MaxLimit subjects, the size of one page of a list
	// route. It serves the protected flag of a page of groups.
	GrantsOfSubjects(ctx context.Context, subjects []Subject) ([]Grant, error)
	// RoleGrantedBelowInstallation reports whether a current grant gives
	// the role with the ID role on a namespace, a collection or a unit.
	RoleGrantedBelowInstallation(ctx context.Context, role string) (bool, error)
	// RoleGrantedOnPrincipal reports whether a current grant gives the
	// role with the ID role on a principal.
	RoleGrantedOnPrincipal(ctx context.Context, role string) (bool, error)
}

// GrantReads reads what decides the actions that a principal holds, and
// where a role is granted, in the transaction of a write. A store gives it
// to a GrantCheck. Each method reads as the method of the same name of
// Callers, GrantStore, RoleStore or GrantLookups does.
type GrantReads interface {
	Callers
	GrantsOn(ctx context.Context, subjects []Subject, objects []identity.Object) ([]Grant, error)
	GrantsOf(ctx context.Context, subject Subject) ([]Grant, error)
	GetRoles(ctx context.Context, ids []string) ([]Role, error)
	RoleGrantedBelowInstallation(ctx context.Context, role string) (bool, error)
	RoleGrantedOnPrincipal(ctx context.Context, role string) (bool, error)
	// UnitCollectionID returns the stable ID of the collection that the
	// unit with the stable ID unitID is in now, and false when the unit is
	// in no collection or does not exist. A store that keeps the
	// collections of units in another place must answer it from the same
	// source as the UnitCollections port of the Authorizer.
	UnitCollectionID(ctx context.Context, unitID string) (string, bool, error)
}

// GrantCheck is a rule that a write of a grant or of a role runs in its own
// transaction, before the write changes anything. The store runs it after
// the write holds its locks, after the fence check: each write that can
// change the result of the check (a grant, a revoke, a role, a membership
// or a principal) takes one of those locks too. r reads in that
// transaction: a change that committed before the check is visible to it,
// and a change that starts after the check waits until the write ends. An
// error refuses the write: the write changes nothing and returns that
// error.
type GrantCheck func(ctx context.Context, r GrantReads) error

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
	// another role has the name without regard to case. It runs check,
	// when it is not nil, as a GrantCheck runs, and returns its error.
	UpdateRole(ctx context.Context, r Role, e audit.Entry, check GrantCheck) error
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
