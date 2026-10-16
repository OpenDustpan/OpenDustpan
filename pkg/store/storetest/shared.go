package storetest

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
)

// PairFactory returns two handles on one fresh, empty database for one
// subtest, as two servers open one database. Each handle has its own
// connections: a write through one handle must not depend on a cache or a
// lock of the other. The factory closes both handles when the subtest ends.
//
// An adapter that allows one handle per database, such as SQLite, passes
// that handle twice: one database with two concurrent callers. A PostgreSQL
// adapter opens one schema twice:
//
//	storetest.RunShared(t, func(t *testing.T) (storetest.Store, storetest.Store) {
//		url := newSchemaURL(t)
//		return openStore(t, url), openStore(t, url)
//	})
type PairFactory func(t *testing.T) (Store, Store)

// RunShared executes the cases that need two handles on one database
// against the pairs that newPair produces. Each case writes through one
// handle and reads or writes through the other.
func RunShared(t *testing.T, newPair PairFactory) {
	t.Helper()

	cases := []struct {
		test func(t *testing.T, a, b Store)
		name string
	}{
		{name: "a namespace written on one handle reads on the other", test: testSharedNamespace},
		{
			name: "a namespace name taken on one handle is ErrAlreadyExists on the other",
			test: testSharedDuplicateNamespace,
		},
		{name: "a stack written on one handle reads on the other", test: testSharedStack},
		{name: "an installation state written on one handle reads on the other", test: testSharedMaintenanceState},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			a, b := newPair(t)
			tc.test(t, a, b)
		})
	}
}

func testSharedNamespace(t *testing.T, a, b Store) {
	t.Helper()

	want := newNamespace(t, a, "acme")

	got, err := b.GetNamespace(t.Context(), want.Name)
	if err != nil {
		t.Fatalf("get namespace on the other handle: %v", err)
	}

	if got != want {
		t.Fatalf("get namespace on the other handle = %+v, want %+v", got, want)
	}
}

func testSharedDuplicateNamespace(t *testing.T, a, b Store) {
	t.Helper()

	ns := newNamespace(t, a, "acme")

	if err := b.CreateNamespace(t.Context(), ns); !errors.Is(err, namespace.ErrAlreadyExists) {
		t.Fatalf("create namespace on the other handle: err = %v, want ErrAlreadyExists", err)
	}
}

func testSharedStack(t *testing.T, a, b Store) {
	t.Helper()

	ns := newNamespace(t, a, "acme")
	want := newStack(ns.Name, "web", "prod")

	if err := a.CreateStack(t.Context(), want, ns.Fence()); err != nil {
		t.Fatalf("create stack: %v", err)
	}

	got, err := b.GetStack(t.Context(), want.Ref)
	if err != nil {
		t.Fatalf("get stack on the other handle: %v", err)
	}

	assertStack(t, got, want)

	if err := b.CreateStack(t.Context(), want, ns.Fence()); !errors.Is(err, stack.ErrAlreadyExists) {
		t.Fatalf("create stack on the other handle: err = %v, want ErrAlreadyExists", err)
	}
}

func testSharedMaintenanceState(t *testing.T, a, b Store) {
	t.Helper()

	ctx := t.Context()

	if err := a.SetState(ctx, maintenance.StateMaintenance); err != nil {
		t.Fatalf("set installation state to maintenance: %v", err)
	}

	if got, err := b.GetState(ctx); err != nil || got != maintenance.StateMaintenance {
		t.Fatalf("get installation state on the other handle = %q, %v, want maintenance without error", got, err)
	}

	if err := b.SetStateIf(ctx, maintenance.StateMaintenance, maintenance.StateNormal); err != nil {
		t.Fatalf("set installation state to normal on the other handle: %v", err)
	}

	if got, err := a.GetState(ctx); err != nil || got != maintenance.StateNormal {
		t.Fatalf("get installation state = %q, %v, want normal without error", got, err)
	}
}
