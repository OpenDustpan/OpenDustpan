package access

import (
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
	// RoleWriter reads, and runs updates and applies: it writes state and
	// encrypts and decrypts secrets.
	RoleWriter = "writer"
	// RoleMaintainer writes, and makes, renames, deletes, unlocks and moves
	// units, and manages collections.
	RoleMaintainer = "maintainer"
	// RoleAdmin holds every data action on its object: it also manages
	// access, the override switches, forced and unlocked writes and the
	// namespace. The creator of a namespace holds it on the namespace.
	RoleAdmin = "admin"
	// RoleNamespaceAdmin is the role that the creator of a namespace gets on
	// it.
	RoleNamespaceAdmin = RoleAdmin
	// RoleInstallationOperator holds the operator actions only: backups,
	// restores, maintenance, metrics and the installation audit log. It reads
	// no namespace data and manages no principal.
	RoleInstallationOperator = "installation-operator"
	// RoleAccountAdministrator holds the administration and data actions:
	// the tenant side of a hosted installation. It holds no operator action.
	RoleAccountAdministrator = "account-administrator"
	// RoleSiteAdministrator holds the actions of all three families. A
	// self-hosted installation can use it.
	RoleSiteAdministrator = "site-administrator"
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
	{RoleWriter, "Reads, and runs updates, plans and applies."},
	{RoleMaintainer, "Writes, and makes, renames, deletes, unlocks and moves units, and manages collections."},
	{RoleAdmin, "Holds every action inside its object, access management included."},
	{
		RoleInstallationOperator,
		"Operates the installation: backups, restores, maintenance, metrics and the installation audit log. Reads no namespace data.",
	},
	{
		RoleAccountAdministrator,
		"Manages principals, groups, roles, grants and namespaces, and holds every data action.",
	},
	{RoleSiteAdministrator, "Holds every action of the installation."},
}

// viewerActions are the actions of RoleViewer.
func viewerActions() []identity.Action {
	return []identity.Action{
		identity.ActionNamespaceRead,
		identity.ActionCollectionRead,
		identity.ActionUnitRead,
	}
}

// writerActions are the actions of RoleWriter.
func writerActions() []identity.Action {
	return append(viewerActions(),
		identity.ActionUnitWrite,
		identity.ActionUnitEncrypt,
		identity.ActionUnitDecrypt,
	)
}

// maintainerActions are the actions of RoleMaintainer.
func maintainerActions() []identity.Action {
	return append(writerActions(),
		identity.ActionUnitManage,
		identity.ActionUnitMove,
		identity.ActionCollectionCreate,
		identity.ActionCollectionUpdate,
		identity.ActionCollectionDelete,
		identity.ActionCollectionCreateUnit,
	)
}

// builtInActions returns the actions of the built-in role id.
func builtInActions(id string) []identity.Action {
	switch id {
	case RoleViewer:
		return viewerActions()
	case RoleAuditor:
		return append(viewerActions(),
			identity.ActionNamespaceReadAudit,
			identity.ActionNamespaceReadAccess,
			identity.ActionCollectionReadAccess,
			identity.ActionUnitReadAccess,
		)
	case RoleSecretViewer:
		return append(viewerActions(), identity.ActionUnitReadSecrets)
	case RoleWriter:
		return writerActions()
	case RoleMaintainer:
		return maintainerActions()
	case RoleAdmin:
		return Actions(FamilyData)
	case RoleInstallationOperator:
		return Actions(FamilyOperator)
	case RoleAccountAdministrator:
		return append(Actions(FamilyAdministration), Actions(FamilyData)...)
	case RoleSiteAdministrator:
		out := append(Actions(FamilyOperator), Actions(FamilyAdministration)...)

		return append(out, Actions(FamilyData)...)
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
