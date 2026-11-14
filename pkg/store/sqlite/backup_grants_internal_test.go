package sqlite

import (
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
)

// stagedGrantEntry returns a valid audit entry of the scope ns for the action
// on the object.
func stagedGrantEntry(ns namespace.Name, action, object string) audit.Entry {
	return audit.Entry{
		At: time.UnixMilli(1_700_000_000_000).UTC(), Namespace: ns, Subject: "admin", IDPSubject: "",
		TokenID: "", Action: action, Object: object, Version: "", ValuePath: "",
		ValueCount: nil, Changes: nil, SourceIP: "", UserAgent: "", Channel: audit.ChannelAPI,
		Kind: audit.KindChange, Outcome: audit.OutcomeSuccess, RequestID: "", ObjectID: "", PrevHash: "",
		Hash: "", Sequence: 0, FenceVersion: 0, PrincipalKind: "", JobID: "", Reason: "",
	}
}

// seedNamespacePrincipalGrant makes the namespaces acme and other, the service
// principal ci-acme of acme, and its grant grt-inside of viewer on the
// namespace acme.
func seedNamespacePrincipalGrant(t *testing.T, s *Store) {
	t.Helper()

	ctx := t.Context()
	at := time.UnixMilli(1_700_000_000_000).UTC()

	acme := namespace.Namespace{
		ID: namespace.NewID(), CreatedAt: at, Name: "acme", PrimaryInstallation: "eu-west",
		FailoverVersion: 1, Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}
	if err := s.CreateNamespace(ctx, acme); err != nil {
		t.Fatalf("create namespace acme: %v", err)
	}

	other := namespace.Namespace{
		ID: namespace.NewID(), CreatedAt: at, Name: "other", PrimaryInstallation: "us-east",
		FailoverVersion: 1, Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}
	if err := s.CreateNamespace(ctx, other); err != nil {
		t.Fatalf("create namespace other: %v", err)
	}

	p := principal.Principal{
		CreatedAt: at, UpdatedAt: at, DisabledAt: time.Time{}, ID: "prn-ci-acme", Subject: "ci-acme",
		DisplayName: "ci-acme", Kind: principal.KindService, Source: principal.SourceLocal,
		Namespace: "acme", GroupCount: 0, BreakGlass: false,
	}

	entry := stagedGrantEntry("acme", "namespace:manage-principals", principal.Ref(p.ID))
	if err := s.CreateNamespacePrincipal(ctx, p, acme.Fence(), entry); err != nil {
		t.Fatalf("create principal: %v", err)
	}

	g := access.Grant{
		CreatedAt: at, Condition: nil, Object: identity.NamespaceObject("acme"), ID: "grt-inside",
		Role: access.RoleViewer, CreatedBy: "admin",
		Subject: access.Subject{Kind: access.SubjectPrincipal, ID: p.ID},
	}

	entry = stagedGrantEntry("acme", "namespace:manage-access", "grant:"+g.ID)
	if err := s.CreateGrant(ctx, g, acme.Fence(), entry, nil); err != nil {
		t.Fatalf("create grant: %v", err)
	}
}

// seedNamespacePrincipalMembership makes the group grp-team and the person
// prn-alice, a principal of no namespace. The tests add the membership rows.
func seedNamespacePrincipalMembership(t *testing.T, s *Store) {
	t.Helper()

	ctx := t.Context()
	at := time.UnixMilli(1_700_000_000_000).UTC()

	g := principal.Group{
		CreatedAt: at, UpdatedAt: at, ID: "grp-team", Name: "team", Description: "",
		Source: principal.SourceLocal, MemberCount: 0, ClosedToIdentityProvider: false,
	}
	err := s.CreateGroup(ctx, g, stagedGrantEntry(audit.ServerScope, "server:manage-groups", "group:"+g.ID))
	if err != nil {
		t.Fatalf("create group: %v", err)
	}

	p := principal.Principal{
		CreatedAt: at, UpdatedAt: at, DisabledAt: time.Time{}, ID: "prn-alice", Subject: "alice",
		DisplayName: "alice", Kind: principal.KindPerson, Source: principal.SourceLocal,
		Namespace: "", GroupCount: 0, BreakGlass: false,
	}
	err = s.CreatePrincipal(
		ctx, p, stagedGrantEntry(audit.ServerScope, "server:manage-principals", principal.Ref(p.ID)),
	)
	if err != nil {
		t.Fatalf("create principal: %v", err)
	}
}

// stagedMembershipStore returns a store with the namespace principal grant,
// the group and the person, after the updates ran on it.
func stagedMembershipStore(t *testing.T, updates []string) *Store {
	t.Helper()

	s := stagedGrantStore(t, nil)
	seedNamespacePrincipalMembership(t, s)

	for _, update := range updates {
		if _, err := s.conn.ExecContext(t.Context(), update); err != nil {
			t.Fatalf("change the row: %v", err)
		}
	}

	return s
}

// stagedGrantStore returns a store with the namespace principal grant, after
// the updates ran on it.
func stagedGrantStore(t *testing.T, updates []string) *Store {
	t.Helper()

	s := openInternalTemplated(t)
	seedNamespacePrincipalGrant(t, s)

	for _, update := range updates {
		if _, err := s.conn.ExecContext(t.Context(), update); err != nil {
			t.Fatalf("change the row: %v", err)
		}
	}

	return s
}

// TestCheckStagedNamespacePrincipalGrantsAccepts proves the restore check
// accepts the grant of a principal of a namespace inside its namespace, and
// its ended rows.
func TestCheckStagedNamespacePrincipalGrantsAccepts(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		updates []string
	}{
		{name: "a grant inside the namespace", updates: nil},
		{
			name:    "an ended grant inside the namespace",
			updates: []string{`UPDATE grants SET revoked_at_ms = 2, revoked_by = 'admin' WHERE id = 'grt-inside'`},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := stagedGrantStore(t, tc.updates)

			if err := checkStagedNamespacePrincipalGrants(t.Context(), s.conn); err != nil {
				t.Fatalf("check = %v, want nil", err)
			}
		})
	}
}

// TestCheckStagedNamespacePrincipalGrantsRefuses proves the restore check
// refuses each grant of a principal of a namespace on an object outside it,
// ended or not, whatever the namespace column of the row says.
func TestCheckStagedNamespacePrincipalGrantsRefuses(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		updates []string
	}{
		{
			name: "a grant on the installation",
			updates: []string{
				`UPDATE grants SET object = '@server', target = '@server', object_kind = 'installation', namespace = '' WHERE id = 'grt-inside'`,
			},
		},
		{
			name: "a grant on another namespace",
			updates: []string{
				`UPDATE grants SET object = 'other', target = 'other', object_kind = 'namespace', namespace = 'other' WHERE id = 'grt-inside'`,
			},
		},
		{
			name: "a grant on a unit of another namespace",
			updates: []string{
				`UPDATE grants SET object = 'opentofu:other/prod', target = 'unit:stk-other', object_kind = 'unit', namespace = 'other' WHERE id = 'grt-inside'`,
			},
		},
		{
			name: "a grant inside the namespace with a target of another namespace",
			updates: []string{
				`INSERT INTO stacks (namespace, project, name, version, created_at_ms, updated_at_ms, id) VALUES ('other', 'web', 'prod', 0, 1, 1, 'stk-other')`,
				`UPDATE grants SET object = 'pulumi:acme/web/dev', target = 'unit:stk-other', object_kind = 'unit', namespace = 'acme' WHERE id = 'grt-inside'`,
			},
		},
		{
			name: "a grant inside the namespace with a namespace target of another namespace",
			updates: []string{
				`UPDATE grants SET object = 'acme', target = 'other', object_kind = 'namespace', namespace = 'acme' WHERE id = 'grt-inside'`,
			},
		},
		{
			name: "a grant inside the namespace with the namespace column of another namespace",
			updates: []string{
				`UPDATE grants SET object = 'acme', target = 'acme', object_kind = 'namespace', namespace = 'other' WHERE id = 'grt-inside'`,
			},
		},
		{
			name: "an ended grant on the installation",
			updates: []string{
				`UPDATE grants SET object = '@server', target = '@server', object_kind = 'installation', namespace = '', revoked_at_ms = 2 WHERE id = 'grt-inside'`,
			},
		},
		{
			name: "a namespace column that names the namespace of the principal",
			updates: []string{
				`UPDATE grants SET object = '@server', target = '@server', object_kind = 'installation', namespace = 'acme' WHERE id = 'grt-inside'`,
			},
		},
		{
			name: "a grant of a principal of the namespace that is deleted",
			updates: []string{
				`UPDATE principals SET deleted_at_ms = 2 WHERE id = 'prn-ci-acme'`,
				`UPDATE grants SET object = '@server', target = '@server', object_kind = 'installation', namespace = '' WHERE id = 'grt-inside'`,
			},
		},
		{
			name: "an object that does not parse",
			updates: []string{
				`UPDATE grants SET object = 'not a reference' WHERE id = 'grt-inside'`,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := stagedGrantStore(t, tc.updates)

			err := checkStagedNamespacePrincipalGrants(t.Context(), s.conn)
			if !errors.Is(err, backup.ErrCorrupt) {
				t.Fatalf("check = %v, want ErrCorrupt", err)
			}

			if !strings.Contains(err.Error(), "grt-inside") {
				t.Fatalf("check = %v, want the grant ID", err)
			}
		})
	}
}

// assertMembershipRefused checks that err is a corrupt backup and names the
// group and the principal of the refused membership.
func assertMembershipRefused(t *testing.T, err error) {
	t.Helper()

	if !errors.Is(err, backup.ErrCorrupt) {
		t.Fatalf("check = %v, want ErrCorrupt", err)
	}

	for _, want := range []string{"grp-team", "prn-ci-acme"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("check = %v, want %q", err, want)
		}
	}
}

// TestCheckStagedNamespacePrincipalMemberships proves the restore check
// refuses a current or removed membership of a principal of a namespace in a
// group, and accepts a membership of a principal that is not in a namespace.
func TestCheckStagedNamespacePrincipalMemberships(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		updates []string
		refuse  bool
	}{
		{
			name: "a current membership",
			updates: []string{
				`INSERT INTO group_members (group_id, principal_id, added_at_ms) VALUES ('grp-team', 'prn-ci-acme', 1)`,
			},
			refuse: true,
		},
		{
			name: "a removed membership",
			updates: []string{
				`INSERT INTO group_members (group_id, principal_id, added_at_ms, removed_at_ms) VALUES ('grp-team', 'prn-ci-acme', 1, 2)`,
			},
			refuse: true,
		},
		{
			name: "a membership of a principal that is not in a namespace",
			updates: []string{
				`INSERT INTO group_members (group_id, principal_id, added_at_ms) VALUES ('grp-team', 'prn-alice', 1)`,
			},
			refuse: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := stagedMembershipStore(t, tc.updates)

			err := checkStagedNamespacePrincipalMemberships(t.Context(), s.conn)
			if !tc.refuse {
				if err != nil {
					t.Fatalf("check = %v, want nil", err)
				}

				return
			}

			assertMembershipRefused(t, err)
		})
	}
}
