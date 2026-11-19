package stack_test

import (
	"context"
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/stack/stacktest"
)

var errMembershipStore = errors.New("membership store down")

// TestUpdatesRefuseNoMembership proves that the update service cannot be built
// without the port that tells it which collection holds a stack.
func TestUpdatesRefuseNoMembership(t *testing.T) {
	t.Parallel()

	f := newUpdates(t)
	cfg := updatesConfigFor(f, f.blobs, f.refusals)
	cfg.Membership = nil

	if _, err := stack.NewUpdates(cfg); !errors.Is(err, stack.ErrMissingDependency) {
		t.Fatalf("NewUpdates without Membership: err = %v, want ErrMissingDependency", err)
	}
}

// TestStartRefusesAStackInNoCollection is the hostile table of the service
// layer: every answer of the membership port that is not "placed" must refuse
// the start, and the update must stay not started.
func TestStartRefusesAStackInNoCollection(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		membership stack.Membership
		want       error
	}{
		{
			name:       "stack in no collection",
			membership: stacktest.Membership{Err: nil, Collection: "", Found: false},
			want:       collection.ErrNoCollection,
		},
		{
			name:       "membership store error",
			membership: stacktest.Membership{Err: errMembershipStore, Collection: "c-test", Found: true},
			want:       errMembershipStore,
		},
		{
			name:       "error with no collection",
			membership: stacktest.Membership{Err: errMembershipStore, Collection: "", Found: false},
			want:       errMembershipStore,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newUpdates(t)
			u, startErr := startWithMembership(t, f, tc.membership)

			if !errors.Is(startErr, tc.want) {
				t.Fatalf("Start: err = %v, want %v", startErr, tc.want)
			}

			got, err := f.svc.Get(t.Context(), f.ref, u.ID)
			if err != nil {
				t.Fatalf("get: %v", err)
			}

			if got.Status != stack.StatusNotStarted {
				t.Fatalf("status after a refused start = %s, want %s", got.Status, stack.StatusNotStarted)
			}
		})
	}
}

// startWithMembership creates an update with the placed service of f and starts
// it with a service that asks membership. It returns the update and the error
// of the start.
func startWithMembership(t *testing.T, f updatesFixture, membership stack.Membership) (stack.Update, error) {
	t.Helper()

	u, err := f.svc.Create(t.Context(), f.ref, stack.Plan{
		Kind: stack.KindUpdate, DryRun: false, CreatedBy: "", RestoredFrom: 0, GitHead: "",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	cfg := updatesConfigFor(f, f.blobs, f.refusals)
	cfg.Membership = membership

	svc, err := stack.NewUpdates(cfg)
	if err != nil {
		t.Fatalf("NewUpdates: %v", err)
	}

	_, startErr := svc.Start(t.Context(), f.ref, u.ID)

	return u, startErr
}

// fakeUnits is the UnitCollections port with a fixed answer.
type fakeUnits struct {
	err  error
	got  collection.UnitRef
	name collection.Name
}

func (u *fakeUnits) UnitCollection(_ context.Context, unit collection.UnitRef) (collection.Ref, error) {
	u.got = unit

	return collection.Ref{Namespace: unit.Namespace, Name: u.name}, u.err
}

// TestCollectionMembership proves how the adapter turns the answers of the
// collection store into the answers of the port.
func TestCollectionMembership(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		err       error
		wantFound bool
		wantErr   bool
	}{
		{name: "in a collection", err: nil, wantFound: true, wantErr: false},
		{name: "in no collection", err: collection.ErrNoCollection, wantFound: false, wantErr: false},
		{name: "unknown unit", err: collection.ErrUnitNotFound, wantFound: false, wantErr: false},
		{name: "store error", err: errMembershipStore, wantFound: false, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newUpdates(t)
			units := &fakeUnits{
				err:  tc.err,
				got:  collection.UnitRef{Namespace: "", Tool: "", Name: ""},
				name: "payments",
			}

			_, found, err := stack.NewCollectionMembership(units).CollectionOf(t.Context(), f.ref)
			if found != tc.wantFound || (err != nil) != tc.wantErr {
				t.Fatalf("CollectionOf = (%v, %v), want found %v, error %v", found, err, tc.wantFound, tc.wantErr)
			}

			if units.got.Tool != stack.InventoryTool ||
				units.got.Name != f.ref.Project.String()+"/"+f.ref.Stack.String() {
				t.Fatalf("asked for unit %+v, want the Pulumi unit of %s", units.got, f.ref)
			}
		})
	}

	f := newUpdates(t)

	if _, found, err := stack.NewCollectionMembership(nil).CollectionOf(t.Context(), f.ref); found || err == nil {
		t.Fatalf("CollectionOf with no collections = (%v, %v), want not found and an error", found, err)
	}
}
