package dustpan

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/workspace"
)

func deletedGuardEntry(action string) audit.Entry {
	return audit.Entry{
		At: time.Now(), Namespace: "acme", Subject: "root", IDPSubject: "", TokenID: "", Action: action,
		Object: "acme", Version: "", ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: "", UserAgent: "",
		Channel: audit.ChannelAPI, Kind: audit.KindChange, Outcome: audit.OutcomeSuccess, RequestID: "",
		ObjectID: "", PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0, Reason: "", PrincipalKind: "", JobID: "",
	}
}

// deletedGuard is a gate service over a real store with the deleted workspace
// web in the collection payments, and a gate that denies each write to payments.
type deletedGuard struct {
	svc  *gateService
	unit identity.Object
	p    identity.Principal
}

// seedDeletedWorkspace makes the workspace web in the collection payments and
// deletes it.
func seedDeletedWorkspace(t *testing.T, st *sqlite.Store, n namespace.Namespace) workspace.Workspace {
	t.Helper()

	ctx := t.Context()

	name, err := workspace.ParseName("web")
	if err != nil {
		t.Fatal(err)
	}

	w := workspace.Workspace{
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
		Lock:        workspace.Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""},
		LastRelease: workspace.NoRelease(),
		ID: workspace.ID(
			ident.NewID("ws-"),
		),
		NamespaceID: n.ID,
		Ref:         workspace.Ref{Namespace: "acme", Name: name},
		Overrides:   namespace.Overrides{Forced: false, Unlocked: false},
		Revision:    1, Origin: identity.StateOrigin{Job: "", Commit: ""},
	}
	if err = st.CreateWorkspace(ctx, w, n.Fence()); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	coll := collection.Collection{
		CreatedAt: time.Now(), ID: ident.NewID("col-"), Ref: collection.Ref{Namespace: "acme", Name: "payments"},
	}
	if err = st.CreateCollection(ctx, coll, n.Fence(), deletedGuardEntry("collection:create")); err != nil {
		t.Fatalf("create collection: %v", err)
	}

	move := collection.Move{
		Unit: collection.UnitRef{Namespace: "acme", Tool: "opentofu", Name: "web"}, From: "", To: coll.Ref,
		ToID: coll.ID, EndsLock: false,
	}
	if err = st.SetUnitCollection(ctx, move, n.Fence(), deletedGuardEntry("unit:set-collection")); err != nil {
		t.Fatalf("move workspace: %v", err)
	}

	err = st.DeleteWorkspace(ctx, w.Ref, n.Fence(), inventory.Deletion{
		Entry: deletedGuardEntry("unit:manage"), PurgeAfter: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("delete workspace: %v", err)
	}

	return w
}

func newDeletedGuard(t *testing.T) deletedGuard {
	t.Helper()

	ctx := t.Context()

	st, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	nss, err := namespace.NewService(namespace.Config{Store: st, Now: nil, Installation: "test"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	createPrincipals(t, st, time.Unix(0, 0), "alice")

	n, err := nss.Create(ctx, "acme")
	if err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	w := seedDeletedWorkspace(t, st, n)

	cfg := new(settings)
	cfg.namespaceStore, cfg.inventoryReader, cfg.now = st, st, time.Now

	gates, err := newStateWriteGates(cfg, nss)
	if err != nil || gates.svc == nil {
		t.Fatalf("newStateWriteGates() = %v, %v", gates.svc, err)
	}

	svc := gates.svc

	g := gate.Gate{
		Scope: gate.CollectionScope("acme", "payments"), ID: "frozen", Version: 0, DecisionPoint: gate.PointStateWrite,
		Condition: `context.unit.collection == "payments"`, Unless: "", Outcome: gate.OutcomeDeny,
		Message: "Writes to {{unit.collection}} are frozen.", Mode: gate.ModeEnforce, OnError: gate.OnErrorFail,
		Final: false, State: "", Reads: nil,
		CheckedWith: gate.CheckedWith{Engine: "", Schema: gate.SchemaVersion{Subject: "", Major: 0, Minor: 0}},
	}
	if _, err = st.Gates().Put(ctx, g, 0, n.Fence(), deletedGuardEntry("namespace:manage-gates")); err != nil {
		t.Fatalf("put gate: %v", err)
	}

	unit, err := identity.UnitObject("opentofu", "acme", "web")
	if err != nil {
		t.Fatal(err)
	}

	return deletedGuard{
		svc: svc, unit: unit.WithUnitID(w.ID.String()),
		p: gatePrincipal("alice", identity.KindPerson, identity.CredentialAccessToken),
	}
}

// guard asks the guard for the restore (rollback) or the final delete of the
// unit with the given stable ID.
func (d deletedGuard) guard(t *testing.T, unitID string, action identity.Action, op gate.OperationKind) error {
	t.Helper()

	_, err := d.svc.GuardStateWrite(t.Context(), gate.StateWrite{
		Principal: d.p, Unit: d.unit.WithUnitID(unitID), Operation: op, Action: action,
		Source: gate.SourceAPI, PlanLock: false, State: nil,
	})

	return err
}

// TestGuardReadsTheCollectionOfADeletedUnit proves, over a real store, that a
// gate on the collection of a workspace refuses the restore and the final
// delete of the deleted workspace.
func TestGuardReadsTheCollectionOfADeletedUnit(t *testing.T) {
	t.Parallel()

	d := newDeletedGuard(t)

	for _, tc := range []struct {
		name   string
		action identity.Action
		op     gate.OperationKind
	}{
		{"restore", identity.ActionUnitRestore, gate.OperationRollback},
		{"purge", identity.ActionUnitPurge, gate.OperationDelete},
	} {
		err := d.guard(t, d.unit.UnitID(), tc.action, tc.op)

		refusal, ok := errors.AsType[*gate.RefusalError](err)
		if !ok || !errors.Is(refusal, gate.ErrDenied) || len(refusal.Messages) != 1 ||
			refusal.Messages[0] != "Writes to payments are frozen." {
			t.Fatalf("%s: GuardStateWrite() = %v, want a refusal by the gate of the collection", tc.name, err)
		}
	}
}

// TestGuardFailsClosedForADeletedUnitThatItCannotFind proves that a deleted
// unit that the store does not know does not read as a unit with no
// collection: a gate on its collection would not match, and the write would go on.
func TestGuardFailsClosedForADeletedUnitThatItCannotFind(t *testing.T) {
	t.Parallel()

	d := newDeletedGuard(t)

	err := d.guard(t, "ws-unknown", identity.ActionUnitRestore, gate.OperationRollback)
	if err == nil || errors.Is(err, gate.ErrDenied) {
		t.Fatalf("GuardStateWrite(unknown deleted unit) = %v, want an error that is not a gate denial", err)
	}

	if !errors.Is(err, gate.ErrUnavailable) {
		t.Fatalf("GuardStateWrite(unknown deleted unit) = %v, want gate.ErrUnavailable", err)
	}
}
