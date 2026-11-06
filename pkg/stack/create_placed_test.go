package stack_test

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
)

// TestCreatePlacedNeedsAPlacedStore proves that a store that cannot make a
// stack and its membership in one transaction makes no stack: the service
// fails closed with ErrPlacementUnsupported and does not fall back to a stack
// with no collection. The fault wrapper does not serve the PlacedStore port.
func TestCreatePlacedNeedsAPlacedStore(t *testing.T) {
	t.Parallel()

	st := wrapCrashSQL(t, nil)

	ns := namespace.Namespace{
		ID: namespace.NewID(), CreatedAt: time.Unix(1, 0), Name: "acme", PrimaryInstallation: "eu-west",
		FailoverVersion: 1, Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}
	if err := st.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	namespaces, err := namespace.NewService(namespace.Config{Store: st, Now: nil, Installation: "eu-west"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	svc, err := stack.NewService(stack.Config{
		Store: st, Namespaces: namespaces, Now: nil, State: nil, Retention: 0, ManualLocks: nil,
	})
	if err != nil {
		t.Fatalf("stack service: %v", err)
	}

	ref, err := stack.ParseRef("acme", "web", "prod")
	if err != nil {
		t.Fatalf("parse ref: %v", err)
	}

	var entry audit.Entry

	p := stack.Placement{
		Collection:   collection.Ref{Namespace: "acme", Name: "payments"},
		CollectionID: "col-1",
		Entry:        entry,
	}

	if _, err = svc.CreatePlaced(t.Context(), ref, nil, p); !errors.Is(err, stack.ErrPlacementUnsupported) {
		t.Fatalf("CreatePlaced = %v, want ErrPlacementUnsupported", err)
	}

	if _, err = svc.Get(t.Context(), ref); !errors.Is(err, stack.ErrNotFound) {
		t.Fatalf("Get after the refusal = %v, want ErrNotFound", err)
	}
}
