package access

import (
	"context"
	"fmt"
	"slices"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
)

// IDs of the built-in roles. A built-in role ID is its name.
const (
	// RoleViewer reads namespaces, collections and units.
	RoleViewer = "viewer"
	// RoleAuditor reads like a viewer, and reads the audit log and the
	// access-review export of its object.
	RoleAuditor = "auditor"
	// RoleSecretViewer reads like a viewer, and reveals secret values
	// (ADR 41, decision 4).
	RoleSecretViewer = "secret-viewer"
	// RolePlanner reads like a viewer, starts a runner job that only plans,
	// and reads the masked plans and the job logs, where a Pulumi preview is.
	// It cannot apply, destroy, approve or read a secret value.
	RolePlanner = "planner"
	// RoleWriter reads, and runs updates and applies: it reads state with
	// plain secret values and writes it, sets the context of units, and
	// encrypts and decrypts secrets.
	RoleWriter = "writer"
	// RoleMaintainer writes, and makes, renames, deletes, restores, unlocks
	// and moves units, and manages collections. It cannot purge a deleted
	// unit: a purge cannot be undone, so only the admin role holds it.
	RoleMaintainer = "maintainer"
	// RoleAdmin holds every data action on its object: it also manages
	// access, the override switches, forced and unlocked writes, the purge of
	// deleted units and the namespace. The creator of a namespace holds it on the namespace.
	RoleAdmin = "admin"
	// RoleNamespaceAdmin is the role that the creator of a namespace gets on
	// it.
	RoleNamespaceAdmin = RoleAdmin
	// RoleInstallationOperator holds the operator actions only: backups,
	// restores, maintenance, metrics and the installation audit log. It reads
	// no namespace data and manages no principal.
	RoleInstallationOperator = "installation-operator"
	// RoleAccountAdministrator holds the administration, data and
	// delegation actions: the tenant side of a hosted installation. It holds
	// no operator action, and no action of siteOnlyActions. On the installation, the delegation actions let it
	// make a token for a service principal whose reach fits inside its own
	// reach, and so act as that principal, with every grant that the
	// principal has now or gets later.
	RoleAccountAdministrator = "account-administrator"
	// RoleSiteAdministrator holds the actions of every family. A
	// self-hosted installation can use it. Like RoleAccountAdministrator,
	// it can act as any service principal.
	RoleSiteAdministrator = "site-administrator"
	// RoleTokenManager makes, rotates and revokes the tokens of a service
	// principal. Granted on one service principal, it lets its holder act
	// as that principal, with every grant that the principal has now or
	// gets later.
	RoleTokenManager = "token-manager"
)

// builtInDescriptions describes each built-in role, in catalog order.
//
//nolint:gochecknoglobals // A constant table of the built-in roles.
var builtInDescriptions = []struct {
	id          string
	description string
}{
	{RoleViewer, "Reads namespaces, collections and units, and their state with secret values masked."},
	{RoleAuditor, "Reads like a viewer, and reads the audit log and the access review."},
	{RoleSecretViewer, "Reads like a viewer, and reveals secret values."},
	{
		RolePlanner,
		"Reads like a viewer, starts jobs that only plan, and reads the masked plans and the job logs. Cannot apply or read secret values.",
	},
	{RoleWriter, "Reads, runs updates, plans and applies, reads secret values, and sets the context of units."},
	{
		RoleMaintainer,
		"Writes, and makes, renames, deletes, restores, unlocks and moves units, and manages collections.",
	},
	{RoleAdmin, "Holds every action inside its object, access management included."},
	{
		RoleInstallationOperator,
		"Operates the installation: backups, restores, maintenance, metrics and the installation audit log. Reads no namespace data.",
	},
	{
		RoleAccountAdministrator,
		"Manages principals, groups, roles, grants and namespaces, makes the tokens of each service principal whose reach fits inside its own reach, and holds every data action.",
	},
	{RoleSiteAdministrator, "Holds every action of the installation."},
	{
		RoleTokenManager,
		"Makes, rotates and revokes the tokens of a service principal, and so acts as that principal.",
	},
}

// viewerActions are the actions of RoleViewer.
func viewerActions() []identity.Action {
	return []identity.Action{
		identity.ActionNamespaceRead,
		identity.ActionCollectionRead,
		identity.ActionUnitRead,
		identity.ActionUnitReadOutputs,
		identity.ActionProfileRead,
		identity.ActionNamespaceReadGates,
		identity.ActionViewCreate,
		identity.ActionViewRead,
		identity.ActionViewDelete,
	}
}

// writerActions are the actions of RoleWriter.
func writerActions() []identity.Action {
	return append(viewerActions(),
		identity.ActionUnitWrite,
		identity.ActionUnitEncrypt,
		identity.ActionUnitDecrypt,
		identity.ActionUnitReadSecrets,
		identity.ActionUnitSetContext,
	)
}

// maintainerActions are the actions of RoleMaintainer.
func maintainerActions() []identity.Action {
	return append(writerActions(),
		identity.ActionUnitManage,
		identity.ActionUnitMove,
		identity.ActionUnitRestore,
		identity.ActionUnitLock,
		identity.ActionCollectionCreate,
		identity.ActionCollectionUpdate,
		identity.ActionCollectionDelete,
		identity.ActionCollectionCreateUnit,
		identity.ActionNamespaceReadGitHostConnections,
		identity.ActionNamespaceUseGitHostConnection,
		identity.ActionNamespaceReadPlacementRules,
	)
}

// siteOnlyActions are the administration actions that, of the built-in
// roles, only RoleSiteAdministrator holds. The sign-in settings decide who
// can sign in to the whole installation, and a restore with the access
// tables replaces who has access, so the tenant side of a hosted
// installation holds neither of them.
func siteOnlyActions() []identity.Action {
	return []identity.Action{identity.ActionServerManageSignIn, identity.ActionServerRestoreAccess}
}

// accountAdministratorActions are the actions of RoleAccountAdministrator.
func accountAdministratorActions() []identity.Action {
	site := siteOnlyActions()
	out := slices.DeleteFunc(Actions(FamilyAdministration), func(a identity.Action) bool {
		return slices.Contains(site, a)
	})
	out = append(out, Actions(FamilyData)...)

	return append(out, Actions(FamilyDelegation)...)
}

// builtInActions returns the actions of the built-in role id.
func builtInActions(id string) []identity.Action {
	switch id {
	case RoleViewer:
		return viewerActions()
	case RoleWriter:
		return writerActions()
	case RoleMaintainer:
		return maintainerActions()
	case RoleAdmin:
		return Actions(FamilyData)
	case RoleInstallationOperator:
		return Actions(FamilyOperator)
	case RoleAccountAdministrator:
		return accountAdministratorActions()
	case RoleSiteAdministrator:
		out := append(Actions(FamilyOperator), Actions(FamilyAdministration)...)
		out = append(out, Actions(FamilyData)...)

		return append(out, Actions(FamilyDelegation)...)
	case RoleTokenManager:
		return Actions(FamilyDelegation)
	default:
		return readerActions(id)
	}
}

// readerActions returns the actions of a built-in role that reads like a
// viewer, and nil for another ID.
func readerActions(id string) []identity.Action {
	switch id {
	case RoleAuditor:
		return append(viewerActions(),
			identity.ActionNamespaceReadAudit,
			identity.ActionNamespaceReadAccess,
			identity.ActionCollectionReadAccess,
			identity.ActionUnitReadAccess,
		)
	case RoleSecretViewer:
		return append(viewerActions(), identity.ActionUnitReadSecrets)
	case RolePlanner:
		return append(
			viewerActions(),
			identity.ActionUnitRunPlan,
			identity.ActionJobReadPlans,
			identity.ActionJobReadLogs,
		)
	default:
		return nil
	}
}

// BuiltInRoles returns the built-in roles in catalog order.
func BuiltInRoles() []Role {
	out := make([]Role, 0, len(builtInDescriptions))

	for _, d := range builtInDescriptions {
		r, _ := BuiltInRole(d.id)
		out = append(out, r)
	}

	return out
}

// BuiltInRole returns the built-in role with the ID id, and false when no
// built-in role has it.
func BuiltInRole(id string) (Role, bool) {
	for _, d := range builtInDescriptions {
		if d.id != id {
			continue
		}

		return Role{
			CreatedAt:   epoch(),
			UpdatedAt:   epoch(),
			ID:          d.id,
			Name:        d.id,
			Description: d.description,
			Actions:     normalizeActions(builtInActions(d.id)),
			BuiltIn:     true,
		}, true
	}

	return Role{}, false //nolint:exhaustruct_v5 // The zero role answers no role.
}

// epoch is the creation time of every built-in role: the Unix epoch, UTC.
func epoch() time.Time {
	return time.Unix(0, 0).UTC()
}

// rolesByID returns the roles that grants name, by ID: the built-in roles
// from code and the custom roles in one query of store. cached answers the
// roles read before, and keep keeps each role read now. A role that no
// longer exists is left out, so its grants give nothing.
func rolesByID(
	ctx context.Context,
	grants []Grant,
	cached func(string) (Role, bool),
	keep func(Role),
	store RoleStore,
) (map[string]Role, error) {
	out := make(map[string]Role, len(grants))

	var missing []string

	for i := range grants {
		id := grants[i].Role
		if _, done := out[id]; done {
			continue
		}

		if r, ok := BuiltInRole(id); ok {
			out[id] = r

			continue
		}

		if r, ok := cached(id); ok {
			out[id] = r

			continue
		}

		if !slices.Contains(missing, id) {
			missing = append(missing, id)
		}
	}

	if len(missing) == 0 {
		return out, nil
	}

	custom, err := store.GetRoles(ctx, missing)
	if err != nil {
		return nil, fmt.Errorf("read roles: %w", err)
	}

	for i := range custom {
		out[custom[i].ID] = custom[i]
		keep(custom[i])
	}

	return out, nil
}
