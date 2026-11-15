package postgres

import (
	"context"
	"fmt"
	"strings"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/identity"
)

// stagedMembershipRow holds the group and the principal of one staged
// membership that the check of a principal of a namespace reads.
type stagedMembershipRow struct {
	groupID     string
	principalID string
}

// checkStagedNamespacePrincipalMemberships checks each staged membership
// whose principal belongs to a namespace: a membership gives the grants of
// its group, and a group belongs to the whole installation, so a principal of
// a namespace may not be a member of one. A row that fails makes the backup
// corrupt, current or removed, so a restore never admits a membership that
// the principal service refuses. It returns backup.ErrCorrupt.
//
// Defense in depth: the principal service refuses the membership on write,
// and the Authorizer drops the group subjects of a principal of a namespace.
// This check stays so that a crafted backup never puts the row in the
// database at all.
func checkStagedNamespacePrincipalMemberships(
	ctx context.Context, pool pgQuerier, staging string,
) error {
	query := "SELECT m.group_id, m.principal_id " +
		"FROM " + stagingTable(staging, backup.TableGroupMembers) + " AS m " +
		"JOIN " + stagingTable(staging, backup.TablePrincipals) + " AS p ON p.id = m.principal_id " +
		"WHERE p.namespace <> ''"

	rows, err := pool.Query(ctx, query)
	if err != nil {
		return fmt.Errorf("read staged memberships: %w", backup.ErrExportRead)
	}

	defer rows.Close()

	for rows.Next() {
		var row stagedMembershipRow

		scanErr := rows.Scan(&row.groupID, &row.principalID)
		if scanErr != nil {
			return fmt.Errorf("read staged memberships: %w", backup.ErrExportRead)
		}

		if rowErr := checkStagedNamespacePrincipalMembership(row); rowErr != nil {
			return rowErr
		}
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return fmt.Errorf("read staged memberships: %w", backup.ErrExportRead)
	}

	return nil
}

// checkStagedNamespacePrincipalMembership refuses one staged membership whose
// principal belongs to a namespace.
func checkStagedNamespacePrincipalMembership(row stagedMembershipRow) error {
	return fmt.Errorf("staged membership of group %q has namespace principal %q: %w",
		row.groupID, row.principalID, backup.ErrCorrupt)
}

// stagedGrantRow holds the columns of one staged grant that the check of a
// principal of a namespace reads.
type stagedGrantRow struct {
	id                 string
	subject            string
	object             string
	namespace          string
	target             string
	targetNamespace    string
	principalNamespace string
}

// checkStagedNamespacePrincipalGrants checks each staged grant whose subject
// is a principal of a namespace: the object of the grant must be that
// namespace or an object inside it. The namespace column of the row must name
// that namespace too, and a unit or collection target must belong to it. A row
// that fails makes the backup corrupt, so a restore never admits a grant that
// the API refuses and that the store would not have written. It returns
// backup.ErrCorrupt.
//
// Defense in depth: the store refuses such a grant in the transaction of its
// write, and the Authorizer honors no grant outside the namespace of its
// subject. This check stays so that a crafted backup never puts the row in
// the database at all.
func checkStagedNamespacePrincipalGrants(ctx context.Context, pool pgQuerier, staging string) error {
	query := "SELECT g.id, g.subject, g.object, g.namespace, g.target, " +
		"COALESCE(s.namespace, w.namespace, c.namespace, '') AS target_namespace, p.namespace " +
		"FROM " + stagingTable(staging, backup.TableGrants) + " AS g " +
		"JOIN " + stagingTable(staging, backup.TablePrincipals) + " AS p ON p.id = g.subject_id " +
		"LEFT JOIN " + stagingTable(staging, "stacks") + " AS s ON g.target = 'unit:' || s.id " +
		"LEFT JOIN " + stagingTable(staging, "workspaces") + " AS w ON g.target = 'unit:' || w.id " +
		"LEFT JOIN " + stagingTable(staging, "collections") + " AS c ON g.target = 'collection:' || c.id " +
		"WHERE g.subject_kind = $1 AND p.namespace <> ''"

	rows, err := pool.Query(ctx, query, string(access.SubjectPrincipal))
	if err != nil {
		return fmt.Errorf("read staged grants: %w", backup.ErrExportRead)
	}

	defer rows.Close()

	for rows.Next() {
		var row stagedGrantRow

		scanErr := rows.Scan(
			&row.id, &row.subject, &row.object, &row.namespace, &row.target, &row.targetNamespace,
			&row.principalNamespace,
		)
		if scanErr != nil {
			return fmt.Errorf("read staged grants: %w", backup.ErrExportRead)
		}

		if rowErr := checkStagedNamespacePrincipalGrant(row); rowErr != nil {
			return rowErr
		}
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return fmt.Errorf("read staged grants: %w", backup.ErrExportRead)
	}

	return nil
}

// checkStagedNamespacePrincipalGrant checks one staged grant of a principal of
// a namespace: the namespace of its object, the namespace column of the row
// and the target of a unit or collection must all name the namespace of the
// principal, so that a grant on the installation, on another namespace or on
// an object inside another namespace fails. An object or a target that does
// not parse fails the check too: no port writes one, so the row never enters
// the database. A unit or collection target that the backup does not hold
// names no live object, so it gives no power and passes.
func checkStagedNamespacePrincipalGrant(row stagedGrantRow) error {
	outside := func() error {
		return fmt.Errorf("staged grant %q gives namespace principal %q an object outside its namespace: %w",
			row.id, row.subject, backup.ErrCorrupt)
	}

	obj, err := identity.ParseObject(row.object)
	if err != nil {
		return fmt.Errorf("staged grant %q has an object that does not parse: %w: %w",
			row.id, err, backup.ErrCorrupt)
	}

	if obj.Namespace().String() != row.principalNamespace {
		return outside()
	}

	if row.namespace != row.principalNamespace {
		return outside()
	}

	if strings.HasPrefix(row.target, unitTargetPrefix) || strings.HasPrefix(row.target, collectionTargetPrefix) {
		if row.targetNamespace != "" && row.targetNamespace != row.principalNamespace {
			return outside()
		}

		return nil
	}

	parsed, err := identity.ParseObject(row.target)
	if err != nil {
		return fmt.Errorf("staged grant %q has a target that does not parse: %w: %w",
			row.id, err, backup.ErrCorrupt)
	}

	if parsed.Namespace().String() != row.principalNamespace {
		return outside()
	}

	return nil
}
