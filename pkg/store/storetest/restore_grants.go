package storetest

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/stack"
)

// namespacePrincipalGrantRow returns a grants row of the service principal
// prn-ci-acme of acme on obj, with the target target. No port writes a grant
// of a principal of a namespace on an object outside it, so the tests of the
// stage write the row into the export by hand.
func namespacePrincipalGrantRow(id, role string, obj identity.Object, target string) map[string]any {
	kind, _ := access.KindOf(obj)

	return map[string]any{
		"id": id, "subject": "principal:prn-ci-acme", "subject_kind": "principal",
		"subject_id": "prn-ci-acme", "role": role, "object": obj.String(), "target": target,
		"object_kind": string(kind), "namespace": obj.Namespace().String(), "condition": nil,
		"created_at_ms": 1, "created_by": "admin", "revoked_at_ms": nil, "revoked_by": "",
	}
}

// namespacePrincipalRow returns a principals row of the service principal
// ci-acme of the namespace acme.
func namespacePrincipalRow() map[string]any {
	row := DevPrincipalRow("prn-ci-acme", "ci-acme")
	row["kind"] = "service"
	row["namespace"] = "acme"

	return row
}

// namespaceGrantCase is one staged grant row with the outcome of its stage:
// refuse says whether the stage must report ErrCorrupt, and grantID is the
// grant that the error must name.
type namespaceGrantCase struct {
	name    string
	row     map[string]any
	refuse  bool
	grantID string
}

// namespaceGrantStage holds the export of the source, with the principal row
// of the grants, and the live rows of the destination.
type namespaceGrantStage struct {
	ps     portableStores
	files  map[string][]byte
	descs  []backup.PortableFile
	sum    backup.Summary
	before backup.Summary
}

// stageNamespaceGrant stages the export with the grant row of tc and checks
// the outcome: a refused stage reports ErrCorrupt and names the grant, an
// accepted stage reports no error, and the live rows of the destination stay
// in both cases.
func stageNamespaceGrant(t *testing.T, stage namespaceGrantStage, tc namespaceGrantCase) {
	t.Helper()

	edited, editedDescs, editedSum := AddPortableRows(
		t, stage.files, stage.descs, stage.sum, backup.TableGrants, tc.row,
	)

	_, stageErr := stage.ps.dst.StagePortable(
		t.Context(), PortableOpener(edited), editedSum, editedDescs, StageAs(t, stage.ps.dst),
	)

	switch {
	case tc.refuse && !errors.Is(stageErr, backup.ErrCorrupt):
		t.Fatalf("stage a backup with %s: err = %v, want ErrCorrupt", tc.name, stageErr)
	case tc.refuse && !strings.Contains(stageErr.Error(), tc.grantID):
		t.Fatalf("stage a backup with %s: err = %q, want the grant %q", tc.name, stageErr, tc.grantID)
	case !tc.refuse && stageErr != nil:
		t.Fatalf("stage a backup with %s: %v", tc.name, stageErr)
	default:
	}

	assertSameSummary(t, portableSummaryOf(t, stage.ps.dst), stage.before)
	discardStaging(t, stage.ps.dst)
}

// testRestoreStagingRefusesNamespacePrincipalGrants stages a portable backup
// that holds a grant of a principal of a namespace on an object outside that
// namespace: the stage fails with ErrCorrupt, the error names the grant, and
// the live rows stay. A grant of the same principal inside its namespace
// stages. An ended grant is refused too: no state of the API holds a row of
// that shape, so the row never enters the database. The object, the namespace
// column and the target of a row must agree: a row that names the namespace of
// the principal while its object is the installation is refused, and so is a
// row whose object is inside but whose target or namespace column names
// another namespace.
func testRestoreStagingRefusesNamespacePrincipalGrants(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	ps := portableRestoreStores(t, newStore(t), newStore(t))

	seedBackupStore(t, ps.src)

	otherProd := mustGetStack(t, ps.src, stack.Ref{Namespace: "other", Project: "web", Stack: "prod"})

	files, sum, descs := PortableFiles(t, ps.src)

	// The principal that the grants name. No port writes the principal of a
	// namespace and a grant of it outside the namespace together, so the
	// test writes the row into the export by hand.
	files, descs, sum = AddPortableRows(t, files, descs, sum, backup.TablePrincipals, namespacePrincipalRow())

	seedBackupStore(t, ps.dst)

	stage := namespaceGrantStage{
		ps: ps, files: files, descs: descs, sum: sum, before: portableSummaryOf(t, ps.dst),
	}

	dev, err := identity.UnitObject("pulumi", "acme", "web/dev")
	if err != nil {
		t.Fatalf("unit object of acme: %v", err)
	}

	prod, err := identity.UnitObject("pulumi", "other", "web/prod")
	if err != nil {
		t.Fatalf("unit object of other: %v", err)
	}

	revoked := namespacePrincipalGrantRow(
		"grt-ended", access.RoleSiteAdministrator, identity.ServerObject(), "@server",
	)
	revoked["revoked_at_ms"] = 2

	hidden := namespacePrincipalGrantRow(
		"grt-hidden", access.RoleSiteAdministrator, identity.ServerObject(), "@server",
	)
	hidden["namespace"] = "acme"

	rowNamespace := namespacePrincipalGrantRow("grt-row-ns", access.RoleViewer, dev, "unit:stk-acme-dev")
	rowNamespace["namespace"] = "other"

	cases := []namespaceGrantCase{
		{
			name: "a grant on the installation",
			row: namespacePrincipalGrantRow(
				"grt-server", access.RoleSiteAdministrator, identity.ServerObject(), "@server",
			),
			refuse: true, grantID: "grt-server",
		},
		{
			name: "a grant on another namespace",
			row: namespacePrincipalGrantRow(
				"grt-other", access.RoleSiteAdministrator, identity.NamespaceObject("other"), "other",
			),
			refuse: true, grantID: "grt-other",
		},
		{
			name:    "a grant on a unit of another namespace",
			row:     namespacePrincipalGrantRow("grt-unit", access.RoleViewer, prod, "unit:stk-other-prod"),
			refuse:  true,
			grantID: "grt-unit",
		},
		{
			name: "a grant inside the namespace with a target of another namespace",
			row: namespacePrincipalGrantRow(
				"grt-target", access.RoleViewer, dev, "unit:"+otherProd.ID.String(),
			),
			refuse: true, grantID: "grt-target",
		},
		{
			name:    "a grant inside the namespace with a namespace target of another namespace",
			row:     namespacePrincipalGrantRow("grt-target-ns", access.RoleViewer, dev, "other"),
			refuse:  true,
			grantID: "grt-target-ns",
		},
		{
			name:    "a grant inside the namespace with the namespace column of another namespace",
			row:     rowNamespace,
			refuse:  true,
			grantID: "grt-row-ns",
		},
		{
			name:   "a revoked grant on the installation",
			row:    revoked,
			refuse: true, grantID: "grt-ended",
		},
		{
			name:   "a grant that hides the installation behind the namespace column",
			row:    hidden,
			refuse: true, grantID: "grt-hidden",
		},
		{
			name:    "a grant inside the namespace of the principal",
			row:     namespacePrincipalGrantRow("grt-inside", access.RoleViewer, dev, "unit:stk-acme-dev"),
			refuse:  false,
			grantID: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stageNamespaceGrant(t, stage, tc)
		})
	}
}

// namespaceMembershipCase is one staged membership row with the outcome of its
// stage.
type namespaceMembershipCase struct {
	name   string
	row    map[string]any
	refuse bool
}

// namespaceMembershipStage holds the export of the source, with the principal
// rows of the memberships, and the live rows of the destination.
type namespaceMembershipStage struct {
	ps     portableStores
	files  map[string][]byte
	descs  []backup.PortableFile
	sum    backup.Summary
	before backup.Summary
}

// stageNamespaceMembership stages the export with the membership row of tc and
// checks the outcome: a refused stage reports ErrCorrupt and names the group
// and the principal, an accepted stage reports no error, and the live rows of
// the destination stay in both cases.
func stageNamespaceMembership(t *testing.T, stage namespaceMembershipStage, tc namespaceMembershipCase) {
	t.Helper()

	edited, editedDescs, editedSum := AddPortableRows(
		t, stage.files, stage.descs, stage.sum, backup.TableGroupMembers, tc.row,
	)

	_, stageErr := stage.ps.dst.StagePortable(
		t.Context(), PortableOpener(edited), editedSum, editedDescs, StageAs(t, stage.ps.dst),
	)

	switch {
	case tc.refuse && !errors.Is(stageErr, backup.ErrCorrupt):
		t.Fatalf("stage a backup with %s: err = %v, want ErrCorrupt", tc.name, stageErr)
	case tc.refuse && !strings.Contains(stageErr.Error(), "grp-team"):
		t.Fatalf("stage a backup with %s: err = %q, want the group %q", tc.name, stageErr, "grp-team")
	case tc.refuse && !strings.Contains(stageErr.Error(), "prn-ci-acme"):
		t.Fatalf("stage a backup with %s: err = %q, want the principal %q", tc.name, stageErr, "prn-ci-acme")
	case !tc.refuse && stageErr != nil:
		t.Fatalf("stage a backup with %s: %v", tc.name, stageErr)
	default:
	}

	assertSameSummary(t, portableSummaryOf(t, stage.ps.dst), stage.before)
	discardStaging(t, stage.ps.dst)
}

// testRestoreStagingRefusesNamespacePrincipalMemberships stages a portable
// backup that holds a membership of a principal of a namespace in a group: the
// stage fails with ErrCorrupt, the error names the group and the principal,
// and the live rows stay. A removed membership is refused too: no state of the
// API holds a row of that shape, so the row never enters the database. A
// membership of a principal that is not in a namespace stages.
func testRestoreStagingRefusesNamespacePrincipalMemberships(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	ps := portableRestoreStores(t, newStore(t), newStore(t))

	seedBackupStore(t, ps.src)

	files, sum, descs := PortableFiles(t, ps.src)

	// The principal of a namespace and the group that the memberships name.
	// No port writes a principal of a namespace as a member of a group, so
	// the test writes the rows into the export by hand.
	files, descs, sum = AddPortableRows(
		t, files, descs, sum, backup.TablePrincipals, namespacePrincipalRow(),
	)
	files, descs, sum = AddPortableRows(
		t, files, descs, sum, backup.TablePrincipalGroups, DevGroupRow("grp-team"),
	)

	seedBackupStore(t, ps.dst)

	stage := namespaceMembershipStage{
		ps: ps, files: files, descs: descs, sum: sum, before: portableSummaryOf(t, ps.dst),
	}

	removed := DevMemberRow("grp-team", "prn-ci-acme")
	removed["removed_at_ms"] = 2

	cases := []namespaceMembershipCase{
		{
			name:   "a current membership of a namespace principal",
			row:    DevMemberRow("grp-team", "prn-ci-acme"),
			refuse: true,
		},
		{
			name:   "a removed membership of a namespace principal",
			row:    removed,
			refuse: true,
		},
		{
			name:   "a membership of a principal that is not in a namespace",
			row:    DevMemberRow("grp-team", "prn-deploy"),
			refuse: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stageNamespaceMembership(t, stage, tc)
		})
	}
}
