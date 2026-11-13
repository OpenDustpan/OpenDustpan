package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// membershipSetup returns a store with the namespace acme, the workspace web
// in the collection payments, and the ID of the workspace.
func membershipSetup(t *testing.T) (*Store, namespace.Namespace, workspace.ID) {
	t.Helper()

	ctx := t.Context()
	at := time.UnixMilli(1_700_000_000_000).UTC()

	s := openInternalTemplated(t)

	ns := namespace.Namespace{
		ID:        namespace.NewID(),
		CreatedAt: at, Name: "acme", PrimaryInstallation: "eu-west", FailoverVersion: 1,
		Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}
	if createErr := s.CreateNamespace(ctx, ns); createErr != nil {
		t.Fatalf("create namespace: %v", createErr)
	}

	ref := membershipRef(ns)
	id := workspace.ID(ident.NewID("ws-"))

	w := workspace.Workspace{
		NamespaceID: "",
		CreatedAt:   at,
		UpdatedAt:   at,
		Lock:        workspace.Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""},
		LastRelease: workspace.NoRelease(),
		ID:          id,
		Ref:         ref,
		Revision:    1,
		Overrides:   namespace.Overrides{Forced: false, Unlocked: false},
		Origin:      identity.StateOrigin{Job: "", Commit: ""},
	}
	if createErr := s.CreateWorkspace(ctx, w, ns.Fence()); createErr != nil {
		t.Fatalf("create workspace: %v", createErr)
	}

	svc, svcErr := collection.NewService(collection.Config{Store: s, Namespaces: s, Now: nil})
	if svcErr != nil {
		t.Fatalf("collection service: %v", svcErr)
	}

	payments := collection.Ref{Namespace: ns.Name, Name: membershipCollection(t, "payments")}
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

// membershipRows returns the number of membership rows of the unit id.
func membershipRows(t *testing.T, s *Store, id workspace.ID) int {
	t.Helper()

	var n int

	err := s.conn.QueryRowContext(
		t.Context(), "SELECT COUNT(*) FROM collection_members WHERE unit_id = ?", string(id),
	).Scan(&n)
	if err != nil {
		t.Fatalf("count memberships: %v", err)
	}

	return n
}

// softDeleteWorkspace moves the workspace of ref to the deleted workspaces.
func softDeleteWorkspace(t *testing.T, s *Store, ns namespace.Namespace, ref workspace.Ref) audit.Entry {
	t.Helper()

	e := membershipEntry(time.UnixMilli(1_700_000_100_000).UTC(), ns.Name, "workspace:delete")
	d := inventory.Deletion{Entry: e, PurgeAfter: e.At.Add(time.Hour)}

	if err := s.DeleteWorkspace(t.Context(), ref, ns.Fence(), d); err != nil {
		t.Fatalf("delete workspace: %v", err)
	}

	return e
}

// TestPurgeDeletesMembershipInTheFencedTransaction checks that a purge removes
// the membership of the unit only when its fence is current. A stale fence
// refuses the purge, and the membership stays with the deleted unit.
func TestPurgeDeletesMembershipInTheFencedTransaction(t *testing.T) {
	t.Parallel()

	s, ns, id := membershipSetup(t)
	e := softDeleteWorkspace(t, s, ns, membershipRef(ns))

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
// restored unit points at no missing collection. The foreign key is turned
// off on one connection to build that state, because the schema refuses it.
func TestRestoreRemovesMembershipOfMissingCollection(t *testing.T) {
	t.Parallel()

	s, ns, id := membershipSetup(t)
	softDeleteWorkspace(t, s, ns, membershipRef(ns))

	conn, err := s.conn.Conn(t.Context())
	if err != nil {
		t.Fatalf("connection: %v", err)
	}

	if err := dropCollectionForTest(t.Context(), conn); err != nil {
		t.Fatalf("drop collection: %v", err)
	}

	if closeErr := conn.Close(); closeErr != nil {
		t.Fatalf("release connection: %v", closeErr)
	}

	if got := membershipRows(t, s, id); got != 1 {
		t.Fatalf("orphan memberships = %d, want 1", got)
	}

	restore := membershipEntry(time.UnixMilli(1_700_000_200_000).UTC(), ns.Name, "workspace:restore")
	if _, err := s.RestoreWorkspace(t.Context(), id, membershipName(t, "web"), ns.Fence(), restore); err != nil {
		t.Fatalf("restore: %v", err)
	}

	if got := membershipRows(t, s, id); got != 0 {
		t.Fatalf("memberships after restore = %d, want 0", got)
	}
}

// dropCollectionForTest removes the collection payments on conn with the
// foreign key off, and turns the foreign key back on before the connection
// returns to the pool.
func dropCollectionForTest(ctx context.Context, conn *sql.Conn) (err error) {
	if _, err = conn.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		return fmt.Errorf("turn off foreign keys: %w", err)
	}

	defer func() {
		if _, onErr := conn.ExecContext(ctx, "PRAGMA foreign_keys = ON"); onErr != nil && err == nil {
			err = fmt.Errorf("turn on foreign keys: %w", onErr)
		}
	}()

	if _, err = conn.ExecContext(ctx, "DELETE FROM collections WHERE name_key = 'payments'"); err != nil {
		return fmt.Errorf("delete collection: %w", err)
	}

	return nil
}

// membershipRef returns the reference of the workspace web in ns.
func membershipRef(ns namespace.Namespace) workspace.Ref {
	return workspace.Ref{Namespace: ns.Name, Name: workspace.Name("web")}
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

func membershipName(t *testing.T, s string) workspace.Name {
	t.Helper()

	n, err := workspace.ParseName(s)
	if err != nil {
		t.Fatalf("parse workspace name %q: %v", s, err)
	}

	return n
}

func membershipCollection(t *testing.T, s string) collection.Name {
	t.Helper()

	n, err := collection.ParseName(s)
	if err != nil {
		t.Fatalf("parse collection name %q: %v", s, err)
	}

	return n
}
