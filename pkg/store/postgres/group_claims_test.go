package postgres_test

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/principal/principaltest"
	"opendustpan.org/opendustpan/pkg/store/postgres"
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
		UserAgent:     "postgres test",
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

// openClaimsStore opens a store on an isolated schema that the case can
// change with direct SQL. It skips the case when no database is set.
func openClaimsStore(t *testing.T) (*postgres.Store, string) {
	t.Helper()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	schemaURL := isolatedURL(t, base)

	s, err := postgres.Open(t.Context(), schemaURL)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(s.Close)

	return s, schemaURL
}

// execRawSQL runs the statement in the schema at schemaURL, outside the store.
func execRawSQL(t *testing.T, schemaURL, statement string) {
	t.Helper()

	conn, err := pgx.Connect(t.Context(), schemaURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	_, execErr := conn.Exec(t.Context(), statement)
	closeErr := conn.Close(t.Context())

	if err := errors.Join(execErr, closeErr); err != nil {
		t.Fatalf("run %q: %v", statement, err)
	}
}

// groupMapperFixture returns the fixture of the mapper over s.
func groupMapperFixture(s *postgres.Store, schemaURL string) principaltest.GroupMapperFixture {
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

			e := groupClaimsEntry("server:manage-principals", "principal:prn-ns")
			e.Namespace = "acme"

			err = s.CreateNamespacePrincipal(t.Context(), principal.Principal{
				CreatedAt: at, UpdatedAt: at, DisabledAt: time.Time{}, ID: "prn-ns", Subject: "ci-deploy",
				DisplayName: "CI", Kind: principal.KindService, Source: principal.SourceLocal, Namespace: "acme",
				GroupCount: 0, BreakGlass: false,
			}, namespace.Fence{Namespace: "acme", FailoverVersion: 1}, e)
			if err != nil {
				t.Fatalf("create namespace principal: %v", err)
			}

			return "prn-ns"
		},
		BreakReads: func(t *testing.T) {
			t.Helper()

			execRawSQL(t, schemaURL, `DROP TABLE group_mappings CASCADE`)
		},
	}
}

// TestGroupMapperConformance runs the suite of the group mapper.
func TestGroupMapperConformance(t *testing.T) {
	t.Parallel()

	principaltest.RunGroupMapper(t, func(t *testing.T) principaltest.GroupMapperFixture {
		t.Helper()

		s, schemaURL := openClaimsStore(t)

		return groupMapperFixture(s, schemaURL)
	})
}

// TestGroupClaimsConformance runs the suite of the group claim applier.
func TestGroupClaimsConformance(t *testing.T) {
	t.Parallel()

	principaltest.RunGroupClaims(t, func(t *testing.T) principaltest.GroupClaimsFixture {
		t.Helper()

		s, schemaURL := openClaimsStore(t)

		return principaltest.GroupClaimsFixture{
			Applier: s.GroupClaimApplier(nil),
			Store:   s,
			Mapping: groupMapperFixture(s, schemaURL),
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

				execRawSQL(t, schemaURL, `CREATE FUNCTION break_member_writes() RETURNS trigger
LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'membership insert broken by the test'; END $$;
CREATE TRIGGER break_member_writes BEFORE INSERT ON group_members
FOR EACH ROW EXECUTE FUNCTION break_member_writes();`)
			},
			BreakAuditAppends: func(t *testing.T) {
				t.Helper()

				if err := breakAuditAppends(t.Context(), schemaURL); err != nil {
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
