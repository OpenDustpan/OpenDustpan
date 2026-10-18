package storetest

import (
	"errors"
	"fmt"
	"testing"

	"opendustpan.org/opendustpan/pkg/namespace"
)

// getByID reads the namespace with the ID id through the IDReader port of
// s. Every store that runs the suite must implement the port.
func getByID(t *testing.T, s Store, id namespace.ID) (namespace.Namespace, error) {
	t.Helper()

	reader, ok := s.(namespace.IDReader)
	if !ok {
		t.Fatalf("store %T does not implement namespace.IDReader", s)
	}

	ns, err := reader.GetNamespaceByID(t.Context(), id)
	if err != nil {
		return namespace.Namespace{}, fmt.Errorf("get namespace by ID: %w", err)
	}

	return ns, nil
}

// testNamespaceByID proves that the store keeps the ID of a namespace and
// finds the namespace by it.
func testNamespaceByID(t *testing.T, s Store) {
	t.Helper()

	want := newNamespace(t, s, "acme")
	newNamespace(t, s, "globex")

	got, err := getByID(t, s, want.ID)
	if err != nil {
		t.Fatalf("get namespace by ID: %v", err)
	}

	if got != want {
		t.Fatalf("get namespace by ID = %+v, want %+v", got, want)
	}

	listed, err := s.ListNamespaces(t.Context())
	if err != nil {
		t.Fatalf("list namespaces: %v", err)
	}

	if len(listed) != 2 || listed[0] != want {
		t.Fatalf("list namespaces = %+v, want %+v first of 2", listed, want)
	}
}

// testMissingNamespaceID proves that an unknown ID is ErrNotFound.
func testMissingNamespaceID(t *testing.T, s Store) {
	t.Helper()

	newNamespace(t, s, "acme")

	if _, err := getByID(t, s, namespace.NewID()); !errors.Is(err, namespace.ErrNotFound) {
		t.Fatalf("get namespace by unknown ID: err = %v, want ErrNotFound", err)
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
			CreatedAt:       now(),
			ID:              tc.id,
			Name:            "acme",
			HomeRegion:      "eu-west",
			FailoverVersion: 1,
			Overrides:       namespace.Overrides{Forced: false, Unlocked: false},
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

	if _, err := getByID(t, s, sameName.ID); !errors.Is(err, namespace.ErrNotFound) {
		t.Fatalf("get namespace by refused ID: err = %v, want ErrNotFound", err)
	}
}
