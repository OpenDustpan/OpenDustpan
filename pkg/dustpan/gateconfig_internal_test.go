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

	"opendustpan.org/opendustpan/internal/pgtest"
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

	if _, err := dir.ResolveGroup(t.Context(), "platform"); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("ResolveGroup of a valid name = %v, want gate.ErrNotFound", err)
	}

	if _, err := dir.ResolveGroup(t.Context(), "<b>x</b>"); !errors.Is(err, gate.ErrInvalid) {
		t.Fatalf("ResolveGroup of a name that is not valid = %v, want gate.ErrInvalid", err)
	}

	if _, _, err := dir.GroupByID(t.Context(), "group-1"); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("GroupByID with no groups = %v, want gate.ErrNotFound", err)
	}
}

// TestGroupSchemaName proves that the name of a schema that the tests make
// keeps the prefix of the test binary, and that it fails without one.
func TestGroupSchemaName(t *testing.T) {
	t.Parallel()

	const prefix = "grp_deadbeef_"
	suffix := []byte{0xde, 0xad, 0xbe, 0xef}

	tests := []struct {
		name    string
		prefix  string
		suffix  []byte
		want    string
		wantErr error
	}{
		{
			name:    "prefix and suffix",
			prefix:  prefix,
			suffix:  suffix,
			want:    prefix + hex.EncodeToString(suffix),
			wantErr: nil,
		},
		{
			name:    "empty prefix",
			prefix:  "",
			suffix:  suffix,
			want:    "",
			wantErr: errEmptyGroupSchemaPrefix,
		},
		{
			name:    "empty suffix",
			prefix:  prefix,
			suffix:  nil,
			want:    prefix,
			wantErr: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := groupSchemaName(tc.prefix, tc.suffix)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("groupSchemaName(%q, %x) error = %v, want %v", tc.prefix, tc.suffix, err, tc.wantErr)
			}

			if got != tc.want {
				t.Fatalf("groupSchemaName(%q, %x) = %q, want %q", tc.prefix, tc.suffix, got, tc.want)
			}
		})
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
		GroupReach: nil,
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
	del, rename := groupEditors(svc, entry, admin)

	return approverdirectorytest.Fixture{
		Directory: groupDirectory{groups: svc},
		Group: func(t *testing.T, name string) string {
			t.Helper()

			g, err := svc.CreateGroup(t.Context(), entry(identity.ActionServerManageGroups),
				principal.GroupInput{Name: name, Description: ""})
			if err != nil {
				t.Fatalf("create group %q: %v", name, err)
			}

			groups[name] = g.ID

			return g.ID
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
		Delete: del,
		Rename: rename,
	}
}

// groupEditors returns the functions of the fixture that delete and rename a
// group through the principal service.
func groupEditors(
	svc *principal.Service, entry func(identity.Action) audit.Entry, admin identity.Principal,
) (func(t *testing.T, id string), func(t *testing.T, id, name string)) {
	del := func(t *testing.T, id string) {
		t.Helper()

		err := svc.DeleteGroup(t.Context(), entry(identity.ActionServerManageGroups), admin, id)
		if err != nil {
			t.Fatalf("delete group %q: %v", id, err)
		}
	}
	rename := func(t *testing.T, id, name string) {
		t.Helper()

		_, err := svc.UpdateGroup(t.Context(), entry(identity.ActionServerManageGroups), admin, id,
			principal.GroupUpdate{Name: &name, Description: nil, ClosedToIdentityProvider: nil})
		if err != nil {
			t.Fatalf("rename group %q to %q: %v", id, name, err)
		}
	}

	return del, rename
}

// groupPostgresEnv names the variable with a PostgreSQL URL for the
// PostgreSQL case.
const groupPostgresEnv = "DUSTPAN_TEST_POSTGRES_URL"

// groupSchemaPrefixEnv names the variable that holds the start of the name of
// each schema that newGroupSchema makes. TestMain sets it to a value that is
// unique for the test binary, and lists the schemas with that start after the
// tests.
const groupSchemaPrefixEnv = "DUSTPAN_TEST_GROUP_SCHEMA_PREFIX"

// errEmptyGroupSchemaPrefix reports an empty prefix for the name of a schema
// that the tests make.
var errEmptyGroupSchemaPrefix = errors.New("empty group schema prefix")

// groupSchemaName returns prefix + hex.EncodeToString(suffix). It fails when
// prefix is empty, because a name without a prefix is not unique to the test
// binary. The check is a defense in depth layer: TestMain sets the prefix too.
func groupSchemaName(prefix string, suffix []byte) (string, error) {
	if prefix == "" {
		return "", errEmptyGroupSchemaPrefix
	}

	return prefix + hex.EncodeToString(suffix), nil
}

// newGroupSchema makes a schema for one case on the database at base, drops it
// when the case ends, and returns the URL that uses it. The schemas of the
// cases fill the disk otherwise.
func newGroupSchema(t *testing.T, base string) string {
	t.Helper()

	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatalf("random schema name: %v", err)
	}

	name, err := groupSchemaName(os.Getenv(groupSchemaPrefixEnv), suffix)
	if err != nil {
		t.Fatalf("schema name: %v", err)
	}

	execGroupSQL(t, base, "CREATE SCHEMA "+pgx.Identifier{name}.Sanitize())
	pgtest.DropSchemaAtCleanup(t, base, name)

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
