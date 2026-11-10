package postgres

import (
	"errors"
	"os"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/internal/pgtest"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// membershipURLEnv names the variable holding the PostgreSQL URL of the
// membership tests. The tests skip when it is empty.
const membershipURLEnv = "DUSTPAN_TEST_POSTGRES_URL"

// membershipFKName is the foreign key from the membership rows to their
// collection, under the name that the schema gives it.
const membershipFKName = "collection_members_namespace_collection_key_fkey"

// TestPurgeDeletesMembershipInTheFencedTransaction checks that a purge removes
// the membership of the unit only when its fence is current. A stale fence
// refuses the purge, and the membership stays with the deleted unit.
func TestPurgeDeletesMembershipInTheFencedTransaction(t *testing.T) {
	t.Parallel()

	s, ns, id := membershipSetup(t)
	e := membershipSoftDelete(t, s, ns)

	if got := membershipRows(t, s, id); got != 1 {
		t.Fatalf("memberships after delete = %d, want 1", got)
	}

	stale := namespace.Fence{Namespace: ns.Name, FailoverVersion: ns.FailoverVersion + 1}

	if err := s.PurgeWorkspace(t.Context(), id, stale, time.Time{}, e); !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("purge with a stale fence = %v, want ErrFenced", err)
	}

	if got := membershipRows(t, s, id); got != 1 {
		t.Fatalf("memberships after a refused purge = %d, want 1", got)
	}

	if err := s.PurgeWorkspace(t.Context(), id, ns.Fence(), time.Time{}, e); err != nil {
		t.Fatalf("purge: %v", err)
	}

	if got := membershipRows(t, s, id); got != 0 {
		t.Fatalf("memberships after purge = %d, want 0", got)
	}
}

// TestRestoreRemovesMembershipOfMissingCollection checks that a restore
// removes the membership of a unit whose collection no longer exists, so the
// restored unit points at no missing collection. The test drops the foreign
// key in its own schema to build that state, because the schema refuses it.
func TestRestoreRemovesMembershipOfMissingCollection(t *testing.T) {
	t.Parallel()

	s, ns, id := membershipSetup(t)
	membershipSoftDelete(t, s, ns)

	alter := "ALTER TABLE collection_members DROP CONSTRAINT " + membershipFKName
	if _, err := s.pool.Exec(t.Context(), alter); err != nil {
		t.Fatalf("drop foreign key: %v", err)
	}

	if _, err := s.pool.Exec(t.Context(), "DELETE FROM collections WHERE name_key = 'payments'"); err != nil {
		t.Fatalf("drop collection: %v", err)
	}

	if got := membershipRows(t, s, id); got != 1 {
		t.Fatalf("orphan memberships = %d, want 1", got)
	}

	restore := membershipEntry(time.UnixMilli(1_700_000_200_000).UTC(), ns.Name, "workspace:restore")
	if _, err := s.RestoreWorkspace(t.Context(), id, workspace.Name("web"), ns.Fence(), restore); err != nil {
		t.Fatalf("restore: %v", err)
	}

	if got := membershipRows(t, s, id); got != 0 {
		t.Fatalf("memberships after restore = %d, want 0", got)
	}
}

// membershipSetup returns a store in a fresh schema, with the namespace acme,
// the workspace web in the collection payments, and the ID of the workspace.
func membershipSetup(t *testing.T) (*Store, namespace.Namespace, workspace.ID) {
	t.Helper()

	ctx := t.Context()
	at := time.UnixMilli(1_700_000_000_000).UTC()

	s, err := Open(ctx, membershipURL(t))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(s.Close)

	ns := namespace.Namespace{
		ID:        namespace.NewID(),
		CreatedAt: at, Name: "acme", PrimaryInstallation: "eu-west", FailoverVersion: 1,
		Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}
	if createErr := s.CreateNamespace(ctx, ns); createErr != nil {
		t.Fatalf("create namespace: %v", createErr)
	}

	id := workspace.ID(ident.NewID("ws-"))
	w := workspace.Workspace{
		NamespaceID: "",
		CreatedAt:   at,
		UpdatedAt:   at,
		Lock:        workspace.Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""},
		LastRelease: workspace.NoRelease(),
		ID:          id,
		Ref:         workspace.Ref{Namespace: ns.Name, Name: workspace.Name("web")},
		Revision:    1,
		Overrides: namespace.Overrides{
			Forced:   false,
			Unlocked: false,
		},
		Origin: identity.StateOrigin{Job: "", Commit: ""},
	}
	if createErr := s.CreateWorkspace(ctx, w, ns.Fence()); createErr != nil {
		t.Fatalf("create workspace: %v", createErr)
	}

	svc, svcErr := collection.NewService(collection.Config{Store: s, Namespaces: s, Now: nil})
	if svcErr != nil {
		t.Fatalf("collection service: %v", svcErr)
	}

	payments := collection.Ref{Namespace: ns.Name, Name: collection.Name("payments")}
	paymentsCol, createErr := svc.Create(ctx, payments, membershipEntry(at, ns.Name, "collection:create"))
	if createErr != nil {
		t.Fatalf("create collection: %v", createErr)
	}

	unit := collection.UnitRef{Namespace: ns.Name, Tool: "opentofu", Name: "web"}
	move := collection.Move{Unit: unit, From: "", To: payments, ToID: paymentsCol.ID, EndsLock: false}
	if moveErr := svc.SetUnit(ctx, move, membershipEntry(at, ns.Name, "unit:move")); moveErr != nil {
		t.Fatalf("move into collection: %v", moveErr)
	}

	return s, ns, id
}

// membershipSoftDelete moves the workspace web to the deleted workspaces and
// returns the audit entry of the delete.
func membershipSoftDelete(t *testing.T, s *Store, ns namespace.Namespace) audit.Entry {
	t.Helper()

	e := membershipEntry(time.UnixMilli(1_700_000_100_000).UTC(), ns.Name, "workspace:delete")
	ref := workspace.Ref{Namespace: ns.Name, Name: workspace.Name("web")}
	d := inventory.Deletion{Entry: e, PurgeAfter: e.At.Add(time.Hour)}

	if err := s.DeleteWorkspace(t.Context(), ref, ns.Fence(), d); err != nil {
		t.Fatalf("delete workspace: %v", err)
	}

	return e
}

// membershipRows returns the number of membership rows of the unit id.
func membershipRows(t *testing.T, s *Store, id workspace.ID) int {
	t.Helper()

	var n int

	err := s.pool.QueryRow(t.Context(), "SELECT COUNT(*) FROM collection_members WHERE unit_id = $1", string(id)).
		Scan(&n)
	if err != nil {
		t.Fatalf("count memberships: %v", err)
	}

	return n
}

// membershipEntry returns a valid audit entry of the action in namespace ns.
func membershipEntry(at time.Time, ns namespace.Name, action string) audit.Entry {
	return audit.Entry{
		At: at, Namespace: ns, Subject: "admin", IDPSubject: "", TokenID: "",
		Action: action, Object: string(ns) + "/web", Version: "", ValuePath: "", ValueCount: nil,
		SourceIP: "", UserAgent: "", Channel: audit.ChannelAPI, Kind: audit.KindChange,
		Outcome: audit.OutcomeSuccess, RequestID: "", PrevHash: "", Hash: "", Sequence: 0,
		Changes: nil, ObjectID: "",
		FenceVersion:  0,
		Reason:        "",
		PrincipalKind: "",
		JobID:         "",
	}
}

// membershipURL returns a URL for a migrated schema in a database of its own,
// cloned from the template database. It skips the test when the environment
// names no database.
func membershipURL(t *testing.T) string {
	t.Helper()

	base := os.Getenv(membershipURLEnv)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL membership tests", membershipURLEnv)
	}

	return pgtest.MigratedURL(t, base)
}
