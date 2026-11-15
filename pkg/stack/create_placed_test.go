package stack_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
)

// acceptAnyPlacement is a PlacedStore that makes the stack and ignores the
// placement. It stands for a store that checks nothing, so a test proves the
// check of the service alone.
type acceptAnyPlacement struct {
	stack.Store
}

func (a acceptAnyPlacement) CreateStackPlaced(
	ctx context.Context, st stack.Stack, _ stack.Placement, fence namespace.Fence,
) error {
	return a.CreateStack(ctx, st, fence)
}

// TestCreatePlacedRefusedWithoutCollection proves that CreatePlaced makes no
// stack when the placement does not name a collection in the namespace of the
// stack, even over a store that would accept any placement.
func TestCreatePlacedRefusedWithoutCollection(t *testing.T) {
	t.Parallel()

	var entry audit.Entry

	valid := stack.Placement{
		Collection:   collection.Ref{Namespace: "acme", Name: "payments"},
		CollectionID: "col-1",
		Entry:        entry,
	}

	var none stack.Placement

	var noRef collection.Ref

	tests := []struct {
		name      string
		placement stack.Placement
		wantErr   bool
	}{
		{name: "zero placement", placement: none, wantErr: true},
		{
			name:      "collection name without ID",
			placement: stack.Placement{Collection: valid.Collection, CollectionID: "", Entry: entry},
			wantErr:   true,
		},
		{
			name:      "ID without collection name",
			placement: stack.Placement{Collection: noRef, CollectionID: valid.CollectionID, Entry: entry},
			wantErr:   true,
		},
		{
			name: "collection in another namespace",
			placement: stack.Placement{
				Collection:   collection.Ref{Namespace: "other", Name: valid.Collection.Name},
				CollectionID: valid.CollectionID,
				Entry:        entry,
			},
			wantErr: true,
		},
		{name: "complete placement", placement: valid, wantErr: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			svc, ref := permissiveService(t)

			_, err := svc.CreatePlaced(t.Context(), ref, nil, tc.placement)
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("CreatePlaced = %v, want success", err)
				}

				return
			}

			requireRefused(t, svc, ref, err)
		})
	}
}

// permissiveService returns a stack service over a store that accepts any
// placement, and the reference of a stack that does not exist.
func permissiveService(t *testing.T) (*stack.Service, stack.Ref) {
	t.Helper()

	inner := wrapCrashSQL(t, nil)

	ns := namespace.Namespace{
		ID: namespace.NewID(), CreatedAt: time.Unix(1, 0), Name: "acme", PrimaryInstallation: "eu-west",
		FailoverVersion: 1, Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}
	if err := inner.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	namespaces, err := namespace.NewService(namespace.Config{Store: inner, Now: nil, Installation: "eu-west"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	svc, err := stack.NewService(stack.Config{
		Store: acceptAnyPlacement{Store: inner}, Namespaces: namespaces, Now: nil, State: nil,
		Retention: 0, ManualLocks: nil,
	})
	if err != nil {
		t.Fatalf("stack service: %v", err)
	}

	ref, err := stack.ParseRef("acme", "web", "prod")
	if err != nil {
		t.Fatalf("parse ref: %v", err)
	}

	return svc, ref
}

// requireRefused fails the test unless err is collection.ErrNoCollection and
// the stack ref does not exist.
func requireRefused(t *testing.T, svc *stack.Service, ref stack.Ref, err error) {
	t.Helper()

	if !errors.Is(err, collection.ErrNoCollection) {
		t.Fatalf("CreatePlaced = %v, want collection.ErrNoCollection", err)
	}

	if _, getErr := svc.Get(t.Context(), ref); !errors.Is(getErr, stack.ErrNotFound) {
		t.Fatalf("Get after the refused create = %v, want stack.ErrNotFound", getErr)
	}
}

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
