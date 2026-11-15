package sqlite_test

import (
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/collection/collectiontest"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

func mustCollectionName(t *testing.T, s string) collection.Name {
	t.Helper()

	n, err := collection.ParseName(s)
	if err != nil {
		t.Fatalf("parse collection name %q: %v", s, err)
	}

	return n
}

func mustWorkspaceName(t *testing.T, s string) workspace.Name {
	t.Helper()

	n, err := workspace.ParseName(s)
	if err != nil {
		t.Fatalf("parse workspace name %q: %v", s, err)
	}

	return n
}

func TestCollectionConformance(t *testing.T) {
	t.Parallel()

	collectiontest.Run(t, func(t *testing.T) collectiontest.Store {
		t.Helper()

		return open(t)
	})
}

// TestCollectionWritesAreAudited runs the collection service on the real store
// and reads the audit log back: each change appends its entry in the same
// transaction, and a move names the source and the target (ADR 60).
func TestCollectionWritesAreAudited(t *testing.T) {
	t.Parallel()

	s := open(t)
	ctx := t.Context()

	ns := namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           time.UnixMilli(1_700_000_000_000).UTC(),
		Name:                "acme",
		PrimaryInstallation: "eu-west",
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	}
	if err := s.CreateNamespace(ctx, ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	svc, svcErr := collection.NewService(collection.Config{Store: s, Namespaces: s, Now: nil})
	if svcErr != nil {
		t.Fatalf("collection service: %v", svcErr)
	}

	w := workspace.Workspace{
		NamespaceID: "",
		CreatedAt:   ns.CreatedAt,
		UpdatedAt:   ns.CreatedAt,
		Lock:        workspace.Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""},
		LastRelease: workspace.NoRelease(),
		ID:          workspace.ID(ident.NewID("ws-")),
		Ref:         workspace.Ref{Namespace: ns.Name, Name: mustWorkspaceName(t, "web")},
		Revision:    1,
		Overrides: namespace.Overrides{
			Forced:   false,
			Unlocked: false,
		},
		Origin: identity.StateOrigin{Job: "", Commit: ""},
	}
	if err := s.CreateWorkspace(ctx, w, ns.Fence()); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	entry := func(action string) audit.Entry {
		return audit.Entry{
			At: time.Time{}, Namespace: ns.Name, Subject: "admin", IDPSubject: "", TokenID: "",
			Action: action, Object: "acme/payments", Version: "", ValuePath: "", ValueCount: nil,
			SourceIP: "", UserAgent: "", Channel: audit.ChannelAPI, Kind: audit.KindChange,
			Outcome: audit.OutcomeSuccess, RequestID: "", PrevHash: "", Hash: "", Sequence: 0,
			Changes: nil, ObjectID: "",
			FenceVersion:  0,
			Reason:        "",
			PrincipalKind: "",
			JobID:         "",
		}
	}

	payments := collection.Ref{Namespace: ns.Name, Name: mustCollectionName(t, "payments")}
	platform := collection.Ref{Namespace: ns.Name, Name: mustCollectionName(t, "platform")}
	unit := collection.UnitRef{Namespace: ns.Name, Tool: "opentofu", Name: "web"}

	paymentsCol, err := svc.Create(ctx, payments, entry("collection:create"))
	if err != nil {
		t.Fatalf("create payments: %v", err)
	}

	if _, err := svc.Create(ctx, platform, entry("collection:create")); err != nil {
		t.Fatalf("create platform: %v", err)
	}

	move := entry("unit:move")
	move.Version = "from=none;to=payments"

	m := collection.Move{Unit: unit, From: "", To: payments, ToID: paymentsCol.ID, EndsLock: false}
	if err := svc.SetUnit(ctx, m, move); err != nil {
		t.Fatalf("move into payments: %v", err)
	}

	if err := svc.Delete(ctx, platform, entry("collection:delete")); err != nil {
		t.Fatalf("delete platform: %v", err)
	}

	listing, listErr := s.List(ctx, ns.Name, audit.Page{Number: 1, Size: 50})
	if listErr != nil {
		t.Fatalf("list audit: %v", listErr)
	}

	got := map[string]bool{}
	for _, e := range listing.Entries {
		got[e.Action+" "+e.Version] = true
	}

	for _, want := range []string{
		"collection:create ",
		"unit:move from=none;to=payments",
		"collection:delete ",
	} {
		if !got[want] {
			t.Errorf("audit log lacks %q; entries: %v", want, got)
		}
	}
}

func TestPlacementConformance(t *testing.T) {
	t.Parallel()

	collectiontest.RunPlacement(t, func(t *testing.T) collectiontest.PlacementStore {
		t.Helper()

		return open(t)
	})
}
