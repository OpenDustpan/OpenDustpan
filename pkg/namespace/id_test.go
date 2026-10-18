package namespace_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/namespace"
)

// idFake is a namespace.Store with the IDReader port. It keeps the
// namespaces that CreateNamespace receives.
type idFake struct {
	storeOnly

	created []namespace.Namespace
}

var _ namespace.IDReader = (*idFake)(nil)

func (f *idFake) CreateNamespace(_ context.Context, ns namespace.Namespace) error {
	f.created = append(f.created, ns)

	return nil
}

func (f *idFake) GetNamespaceByID(_ context.Context, id namespace.ID) (namespace.Namespace, error) {
	for _, ns := range f.created {
		if ns.ID == id {
			return ns, nil
		}
	}

	return namespace.Namespace{}, namespace.ErrNotFound
}

func TestParseID(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in   string
		want bool
	}{
		{in: "ns-abcdefghij234567klmn", want: true},
		{in: "ns-a", want: true},
		{in: "ns-" + strings.Repeat("a", 97), want: true},
		{in: "ns-" + strings.Repeat("a", 98), want: false},
		{in: "", want: false},
		{in: "ns-", want: false},
		{in: "acme", want: false},
		{in: "stk-abc", want: false},
		{in: "ns-a/b", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()

			id, err := namespace.ParseID(tc.in)
			if tc.want && (err != nil || id.String() != tc.in) {
				t.Fatalf("ParseID(%q) = %q, %v, want the ID", tc.in, id, err)
			}

			if !tc.want && !errors.Is(err, namespace.ErrInvalidID) {
				t.Fatalf("ParseID(%q): err = %v, want ErrInvalidID", tc.in, err)
			}
		})
	}
}

func TestNewIDIsValidAndUnique(t *testing.T) {
	t.Parallel()

	seen := make(map[namespace.ID]bool)

	for range 100 {
		id := namespace.NewID()
		if _, err := namespace.ParseID(id.String()); err != nil {
			t.Fatalf("NewID() = %q: %v", id, err)
		}

		if seen[id] {
			t.Fatalf("NewID() = %q twice", id)
		}

		seen[id] = true
	}
}

func TestCreateGivesANewIDThatFindsTheNamespace(t *testing.T) {
	t.Parallel()

	store := &idFake{storeOnly: storeOnly{}, created: nil}
	svc := openService(t, store)

	first, err := svc.Create(t.Context(), "acme")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	second, err := svc.Create(t.Context(), "acme")
	if err != nil {
		t.Fatalf("create again: %v", err)
	}

	if first.ID == second.ID {
		t.Fatalf("two creates of %q gave the same ID %q", "acme", first.ID)
	}

	got, err := svc.GetByID(t.Context(), first.ID)
	if err != nil || got != first {
		t.Fatalf("get by ID = %+v, %v, want %+v", got, err, first)
	}

	if _, err := svc.GetByID(t.Context(), namespace.NewID()); !errors.Is(err, namespace.ErrNotFound) {
		t.Fatalf("get by unknown ID: err = %v, want ErrNotFound", err)
	}
}

func TestGetByIDNeedsIDReader(t *testing.T) {
	t.Parallel()

	svc := openService(t, &storeOnly{})

	if _, err := svc.GetByID(t.Context(), namespace.NewID()); !errors.Is(err, namespace.ErrNoIDReader) {
		t.Fatalf("get by ID without the port: err = %v, want ErrNoIDReader", err)
	}
}
