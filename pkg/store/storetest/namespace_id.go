package storetest

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/namespace"
)

// testNamespaceByID proves that the store keeps the ID of a namespace and
// returns it from GetNamespace and ListNamespaces.
func testNamespaceByID(t *testing.T, s Store) {
	t.Helper()

	want := newNamespace(t, s, "acme")
	newNamespace(t, s, "globex")

	got, err := s.GetNamespace(t.Context(), want.Name)
	if err != nil {
		t.Fatalf("get namespace: %v", err)
	}

	if got != want {
		t.Fatalf("get namespace = %+v, want %+v", got, want)
	}

	listed, err := s.ListNamespaces(t.Context())
	if err != nil {
		t.Fatalf("list namespaces: %v", err)
	}

	if len(listed) != 2 || listed[0] != want {
		t.Fatalf("list namespaces = %+v, want %+v first of 2", listed, want)
	}
}

// testInvalidNamespaceID proves that the store refuses a namespace without a
// valid ID and stores nothing.
func testInvalidNamespaceID(t *testing.T, s Store) {
	t.Helper()

	cases := []struct {
		name string
		id   namespace.ID
	}{
		{name: "empty", id: ""},
		{name: "no prefix", id: "acme"},
		{name: "prefix only", id: "ns-"},
		{name: "bad character", id: "ns-a/b"},
	}

	for _, tc := range cases {
		ns := namespace.Namespace{
			CreatedAt:           now(),
			ID:                  tc.id,
			Name:                "acme",
			PrimaryInstallation: "eu-west",
			FailoverVersion:     1,
			Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
		}

		if err := s.CreateNamespace(t.Context(), ns); !errors.Is(err, namespace.ErrInvalidID) {
			t.Fatalf("%s: create namespace with ID %q: err = %v, want ErrInvalidID", tc.name, tc.id, err)
		}
	}

	if _, err := s.GetNamespace(t.Context(), "acme"); !errors.Is(err, namespace.ErrNotFound) {
		t.Fatalf("get refused namespace: err = %v, want ErrNotFound", err)
	}
}

// testNamespaceIDTaken proves that the ID of a namespace is unique: a second
// namespace with the same ID is ErrAlreadyExists, and a second namespace
// with the same name keeps the first ID.
func testNamespaceIDTaken(t *testing.T, s Store) {
	t.Helper()

	first := newNamespace(t, s, "acme")

	sameID := first
	sameID.Name = "globex"

	if err := s.CreateNamespace(t.Context(), sameID); !errors.Is(err, namespace.ErrAlreadyExists) {
		t.Fatalf("create namespace with a taken ID: err = %v, want ErrAlreadyExists", err)
	}

	sameName := first
	sameName.ID = namespace.NewID()

	if err := s.CreateNamespace(t.Context(), sameName); !errors.Is(err, namespace.ErrAlreadyExists) {
		t.Fatalf("create namespace with a taken name: err = %v, want ErrAlreadyExists", err)
	}

	got, err := s.GetNamespace(t.Context(), first.Name)
	if err != nil {
		t.Fatalf("get namespace: %v", err)
	}

	if got.ID != first.ID {
		t.Fatalf("namespace ID = %q after a refused create, want %q", got.ID, first.ID)
	}
}
