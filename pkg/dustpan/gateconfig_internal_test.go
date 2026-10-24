package dustpan

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/gate/approverdirectorytest"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/postgres"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// TestGroupDirectoryConformanceSQLite runs the suite of gate.ApproverDirectory
// on groupDirectory over the SQLite store.
func TestGroupDirectoryConformanceSQLite(t *testing.T) {
	t.Parallel()

	approverdirectorytest.Run(t, func(t *testing.T) approverdirectorytest.Fixture {
		t.Helper()

		st, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
		if err != nil {
			t.Fatalf("open store: %v", err)
		}

		t.Cleanup(func() {
			if closeErr := st.Close(); closeErr != nil {
				t.Errorf("close store: %v", closeErr)
			}
		})

		return groupFixture(t, st)
	})
}

// TestGroupDirectoryConformancePostgres runs the same suite over the
// PostgreSQL store, on a schema of its own for each case.
func TestGroupDirectoryConformancePostgres(t *testing.T) {
	t.Parallel()

	base := os.Getenv(groupPostgresEnv)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL case", groupPostgresEnv)
	}

	approverdirectorytest.Run(t, func(t *testing.T) approverdirectorytest.Fixture {
		t.Helper()

		schemaURL := newGroupSchema(t, base)

		st, err := postgres.Open(t.Context(), schemaURL)
		if err != nil {
			t.Fatalf("open store: %v", err)
		}

		t.Cleanup(st.Close)

		return groupFixture(t, st)
	})
}

// TestGroupDirectoryWithNoGroups proves a server that keeps no principals
// refuses each group name that is valid, and still tells a name that is not
// valid from a name that no group has.
func TestGroupDirectoryWithNoGroups(t *testing.T) {
	t.Parallel()

	dir := groupDirectory{groups: nil}

	if _, err := dir.GroupMembers(t.Context(), "platform"); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("GroupMembers of a valid name = %v, want gate.ErrNotFound", err)
	}

	if _, err := dir.GroupMembers(t.Context(), "<b>x</b>"); !errors.Is(err, gate.ErrInvalid) {
		t.Fatalf("GroupMembers of a name that is not valid = %v, want gate.ErrInvalid", err)
	}
}

// groupFixture returns the fixture of the suite over st: groups and people go
// through the principal service, as the API makes them.
func groupFixture(t *testing.T, st principal.Store) approverdirectorytest.Fixture {
	t.Helper()

	svc, err := principal.NewService(principal.Config{
		TempAdmins: nil,
		Store:      st,
		Guard:      principal.AdminOnlyGuard{},
		Namespaces: nil,
		Tokens:     nil,
		Now:        nil,
		NewID:      nil,
	})
	if err != nil {
		t.Fatalf("principal service: %v", err)
	}

	admin := identity.Principal{
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Subject: "admin", DisplayName: "",
		Admin: true, TokenID: "", TokenExpiresAt: time.Time{}, Kind: "", Credential: "", JobID: "",
	}
	entry := func(action identity.Action) audit.Entry {
		e := storetest.ServerChange(time.Time{}, "")
		e.Subject, e.Action = admin.Subject, string(action)

		return e
	}
	groups := map[string]string{}

	return approverdirectorytest.Fixture{
		Directory: groupDirectory{groups: svc},
		Group: func(t *testing.T, name string) {
			t.Helper()

			g, err := svc.CreateGroup(t.Context(), entry(identity.ActionServerManageGroups),
				principal.GroupInput{Name: name, Description: ""})
			if err != nil {
				t.Fatalf("create group %q: %v", name, err)
			}

			groups[name] = g.ID
		},
		Member: func(t *testing.T, group, subject string, kind principal.Kind, disabled bool) string {
			t.Helper()

			p, err := svc.CreatePrincipal(t.Context(), entry(identity.ActionServerManagePrincipals),
				principal.Input{Subject: subject, DisplayName: "", Kind: kind})
			if err != nil {
				t.Fatalf("create %s %q: %v", kind, subject, err)
			}

			_, err = svc.AddMember(t.Context(), entry(identity.ActionServerManageGroups), admin, groups[group], p.ID)
			if err != nil {
				t.Fatalf("add %q to %q: %v", subject, group, err)
			}

			if disabled {
				err = svc.DisablePrincipal(t.Context(), entry(identity.ActionServerManagePrincipals), admin, p.ID)
				if err != nil {
					t.Fatalf("disable %q: %v", subject, err)
				}
			}

			return p.ID
		},
		Remove: func(t *testing.T, group, id string) {
			t.Helper()

			err := svc.RemoveMember(t.Context(), entry(identity.ActionServerManageGroups), admin, groups[group], id)
			if err != nil {
				t.Fatalf("remove %q from %q: %v", id, group, err)
			}
		},
	}
}

// groupPostgresEnv names the variable with a PostgreSQL URL for the
// PostgreSQL case.
const groupPostgresEnv = "DUSTPAN_TEST_POSTGRES_URL"

// newGroupSchema makes a schema for one case on the database at base, drops it
// when the case ends, and returns the URL that uses it. The schemas of the
// cases fill the disk otherwise.
func newGroupSchema(t *testing.T, base string) string {
	t.Helper()

	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatalf("random schema name: %v", err)
	}

	name := "test_" + hex.EncodeToString(suffix)
	quoted := pgx.Identifier{name}.Sanitize()

	execGroupSQL(t, base, "CREATE SCHEMA "+quoted)
	t.Cleanup(func() { execGroupSQL(t, base, "DROP SCHEMA "+quoted+" CASCADE") })

	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("parse %s: %v", groupPostgresEnv, err)
	}

	q := u.Query()
	q.Set("search_path", name)
	u.RawQuery = q.Encode()

	return u.String()
}

// execGroupSQL runs one statement on the database at base.
func execGroupSQL(t *testing.T, base, statement string) {
	t.Helper()

	conn, err := pgx.Connect(context.Background(), base)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(context.Background()); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	}()

	if _, execErr := conn.Exec(context.Background(), statement); execErr != nil {
		t.Fatalf("%s: %v", statement, execErr)
	}
}
