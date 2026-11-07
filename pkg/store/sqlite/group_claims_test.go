package sqlite_test

import (
	"path/filepath"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/principal/principaltest"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/tempadmin"
)

// groupClaimsEntry returns a valid audit entry in the scope @server.
func groupClaimsEntry(action, object string) audit.Entry {
	return audit.Entry{
		At:            time.Date(2026, time.October, 13, 21, 9, 0, 0, time.UTC),
		Namespace:     audit.ServerScope,
		Subject:       "admin",
		IDPSubject:    "",
		TokenID:       "",
		Action:        action,
		Object:        object,
		Version:       "",
		ValuePath:     "",
		ValueCount:    nil,
		Changes:       nil,
		SourceIP:      "192.0.2.1",
		UserAgent:     "sqlite test",
		Channel:       audit.ChannelAPI,
		Kind:          audit.KindChange,
		Outcome:       audit.OutcomeSuccess,
		RequestID:     "",
		ObjectID:      "",
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		FenceVersion:  0,
		PrincipalKind: "",
		JobID:         "",
		Reason:        "",
	}
}

// openClaimsStore opens a store on a file that the case can change with
// direct SQL.
func openClaimsStore(t *testing.T) (*sqlite.Store, string) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "dustpan.db")

	copyTemplate(t, path)

	return openPath(t, path), path
}

// groupMapperFixture returns the fixture of the mapper over s.
func groupMapperFixture(s *sqlite.Store, path string) principaltest.GroupMapperFixture {
	return principaltest.GroupMapperFixture{
		Mapper: s.GroupMapper(),
		Store:  s,
		SetMapping: func(t *testing.T, source principal.MembershipSource, sourceID, value string, groupIDs ...string) {
			t.Helper()

			for _, id := range groupIDs {
				err := s.PutGroupMapping(t.Context(), principal.GroupMapping{
					CreatedAt: time.Date(2026, time.October, 13, 21, 2, 0, 0, time.UTC),
					Source:    source, SourceID: sourceID, ClaimValue: value, GroupID: id, CreatedBy: "admin",
				}, groupClaimsEntry(principal.AuditActionMappingPut, "group-mapping:"+id))
				if err != nil {
					t.Fatalf("put the mapping of %q to %q: %v", value, id, err)
				}
			}
		},
		NamespacePrincipal: func(t *testing.T) string {
			t.Helper()

			err := s.CreateNamespace(t.Context(), namespace.Namespace{
				ID: namespace.NewID(), CreatedAt: time.Unix(0, 0), Name: "acme", PrimaryInstallation: "",
				Overrides: namespace.Overrides{Forced: false, Unlocked: false}, FailoverVersion: 1,
			})
			if err != nil {
				t.Fatalf("create namespace: %v", err)
			}

			at := time.Date(2026, time.October, 13, 21, 0, 0, 0, time.UTC)
			err = s.CreateNamespacePrincipal(t.Context(), principal.Principal{
				CreatedAt: at, UpdatedAt: at, DisabledAt: time.Time{}, ID: "prn-ns", Subject: "ci-deploy",
				DisplayName: "CI", Kind: principal.KindService, Source: principal.SourceLocal, Namespace: "acme",
				GroupCount: 0, BreakGlass: false,
			}, namespace.Fence{Namespace: "acme", FailoverVersion: 1}, nsGroupClaimsEntry())
			if err != nil {
				t.Fatalf("create namespace principal: %v", err)
			}

			return "prn-ns"
		},
		BreakReads: func(t *testing.T) {
			t.Helper()

			execRaw(t, path, `DROP TABLE group_mappings`)
		},
	}
}

// nsGroupClaimsEntry returns a valid audit entry in the namespace acme.
func nsGroupClaimsEntry() audit.Entry {
	e := groupClaimsEntry("server:manage-principals", "principal:prn-ns")
	e.Namespace = "acme"

	return e
}

// TestGroupMapperConformance runs the suite of the group mapper.
func TestGroupMapperConformance(t *testing.T) {
	t.Parallel()

	principaltest.RunGroupMapper(t, func(t *testing.T) principaltest.GroupMapperFixture {
		t.Helper()

		s, path := openClaimsStore(t)

		return groupMapperFixture(s, path)
	})
}

// TestGroupClaimsConformance runs the suite of the group claim applier.
func TestGroupClaimsConformance(t *testing.T) {
	t.Parallel()

	principaltest.RunGroupClaims(t, func(t *testing.T) principaltest.GroupClaimsFixture {
		t.Helper()

		s, path := openClaimsStore(t)

		return principaltest.GroupClaimsFixture{
			Applier: s.GroupClaimApplier(nil),
			Store:   s,
			Mapping: groupMapperFixture(s, path),
			WithMapper: func(m principal.GroupMapper) principal.GroupClaimApplier {
				return s.GroupClaimApplier(m)
			},
			MakeTempAdmin: func(t *testing.T, principalID string) {
				t.Helper()

				now := time.Date(2026, time.October, 13, 21, 0, 0, 0, time.UTC)

				err := s.CreateTempAdmin(t.Context(), tempadmin.TempAdmin{
					CreatedAt: now, ExpiresAt: now.Add(time.Hour), EndedAt: time.Time{}, ReviewOpenedAt: time.Time{},
					PrincipalID: principalID, Subject: "alice", CreatedBy: "admin",
					Reason: "restore access during an outage",
				}, groupClaimsEntry("server:manage-temp-admins", "principal:"+principalID))
				if err != nil {
					t.Fatalf("create temporary administrator: %v", err)
				}
			},
			GrantSiteAdministrator: func(t *testing.T, subject access.Subject) {
				t.Helper()

				err := s.CreateGrant(t.Context(), access.Grant{
					CreatedAt: time.Date(2026, time.October, 13, 21, 0, 0, 0, time.UTC), Condition: nil,
					Object: identity.ServerObject(), ID: "grt-adm-" + subject.ID, Role: access.RoleSiteAdministrator,
					CreatedBy: "admin", Subject: subject,
				}, namespace.Fence{Namespace: "", FailoverVersion: 0},
					groupClaimsEntry("server:manage-access", "grant:grt-adm-"+subject.ID), nil)
				if err != nil {
					t.Fatalf("grant the site-administrator role: %v", err)
				}
			},
			BreakMemberWrites: func(t *testing.T) {
				t.Helper()

				execRaw(t, path, `CREATE TRIGGER break_member_writes BEFORE INSERT ON group_members
BEGIN SELECT RAISE(ABORT, 'membership insert broken by the test'); END`)
			},
			BreakAuditAppends: func(t *testing.T) {
				t.Helper()

				if err := breakAuditAppends(t.Context(), path); err != nil {
					t.Fatalf("break audit appends: %v", err)
				}
			},
		}
	})
}

// TestGroupMappingStoreConformance runs the suite of the group mapping store.
func TestGroupMappingStoreConformance(t *testing.T) {
	t.Parallel()

	principaltest.RunGroupMappingStore(t, func(t *testing.T) principaltest.GroupMappingStore {
		t.Helper()

		s, _ := openClaimsStore(t)

		return s
	})
}
