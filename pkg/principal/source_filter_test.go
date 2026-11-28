package principal_test

import (
	"context"
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/principal"
)

// listSourceStore is a fake store that records the kind and the source of a
// list and answers one empty page.
type listSourceStore struct {
	principal.Store

	called bool
	kind   principal.Kind
	source principal.Source
}

func (s *listSourceStore) ListPrincipals(
	_ context.Context, _ principal.Query, kind principal.Kind, source principal.Source,
) ([]principal.Principal, int64, error) {
	s.called = true
	s.kind = kind
	s.source = source

	return nil, 0, nil
}

// newListSourceService returns a service over st with no guard.
func newListSourceService(t *testing.T, st principal.Store) *principal.Service {
	t.Helper()

	svc, err := principal.NewService(principal.Config{
		TempAdmins: nil, Store: st, Guard: principal.AdminOnlyGuard{}, Namespaces: nil, Tokens: nil,
		GroupReach: nil, Now: nil, NewID: nil,
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	return svc
}

// TestListPrincipalsPassesTheSourceToTheStore proves that a valid source
// filter reaches the store, and that the empty source keeps every source.
func TestListPrincipalsPassesTheSourceToTheStore(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		source principal.Source
		want   principal.Source
	}{
		{name: "every source", source: "", want: ""},
		{name: "builtin", source: principal.SourceBuiltin, want: principal.SourceBuiltin},
		{name: "local", source: principal.SourceLocal, want: principal.SourceLocal},
		{name: "a directory source", source: principal.SourceSCIM, want: principal.SourceSCIM},
	} {
		st := &listSourceStore{Store: nil, called: false, kind: "", source: ""}
		svc := newListSourceService(t, st)
		req := principal.PageRequest{Prefix: "", Cursor: "", Limit: 0}

		_, err := svc.ListPrincipals(t.Context(), req, "", tc.source)
		if err != nil {
			t.Fatalf("%s: list: %v", tc.name, err)
		}

		if !st.called || st.source != tc.want {
			t.Fatalf("%s: store source = %q (called %t), want %q", tc.name, st.source, st.called, tc.want)
		}
	}
}

// TestListPrincipalsRefusesAHostileSource proves that an unknown source is
// refused at the service, with no read of the store.
func TestListPrincipalsRefusesAHostileSource(t *testing.T) {
	t.Parallel()

	for _, source := range []principal.Source{
		"Builtin", "BUILTIN", " builtin", "builtin ", "builtin\x00", "builtin\n",
		"x' OR '1'='1", "local; DELETE FROM principals", "local--", "%00", "@server",
		"unknown", "oidc ",
	} {
		st := &listSourceStore{Store: nil, called: false, kind: "", source: ""}
		svc := newListSourceService(t, st)
		req := principal.PageRequest{Prefix: "", Cursor: "", Limit: 0}

		_, err := svc.ListPrincipals(t.Context(), req, "", source)
		if !errors.Is(err, principal.ErrInvalid) {
			t.Fatalf("list with source %q = %v, want ErrInvalid", source, err)
		}

		if st.called {
			t.Fatalf("list with source %q read the store", source)
		}
	}
}

// TestListPrincipalsRefusesAHostileKind proves that an unknown kind is
// refused at the service, with no read of the store, and that the source
// filter cannot make it valid.
func TestListPrincipalsRefusesAHostileKind(t *testing.T) {
	t.Parallel()

	for _, kind := range []principal.Kind{
		"robot", "Person", "person ", "person\x00", "x' OR '1'='1", "service;--", "group",
	} {
		st := &listSourceStore{Store: nil, called: false, kind: "", source: ""}
		svc := newListSourceService(t, st)
		req := principal.PageRequest{Prefix: "", Cursor: "", Limit: 0}

		_, err := svc.ListPrincipals(t.Context(), req, kind, principal.SourceBuiltin)
		if !errors.Is(err, principal.ErrInvalid) {
			t.Fatalf("list with kind %q = %v, want ErrInvalid", kind, err)
		}

		if st.called {
			t.Fatalf("list with kind %q read the store", kind)
		}
	}
}
