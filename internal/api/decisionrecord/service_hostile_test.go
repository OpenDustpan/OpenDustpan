package decisionrecord_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/decisionrecord"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// hostileRecords serves a record of each namespace that was given, whatever it
// was asked for, and counts the calls. It stands for a store with a bug: a
// record of another scope that comes back from the load.
type hostileRecords struct {
	mu     sync.Mutex
	record gate.Record
	gets   int
}

func (h *hostileRecords) Get(_ context.Context, _ namespace.Name, _ gate.RecordID) (gate.Record, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.gets++

	return h.record, nil
}

func mustService(t *testing.T, records decisionrecord.Records) *decisionrecord.Service {
	t.Helper()

	svc, err := decisionrecord.NewService(records)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	return svc
}

// A unit scope is the widest scope of a real change. The service answers each
// other kind of scope of the path with gate.ErrInvalid.
func forgedScopes() []struct {
	name  string
	scope gate.Scope
} {
	unitScope, err := gate.UnitScope("acme", "opentofu", "web")
	if err != nil {
		panic(err)
	}

	return []struct {
		name  string
		scope gate.Scope
	}{
		{"collection scope", gate.CollectionScope("acme", "payments")},
		{"unit scope", unitScope},
		{"installation scope built from the server scope name", gate.NamespaceScope("@server")},
		{"a scope of a namespace with a separator", gate.NamespaceScope("acme/../other")},
		{"a zero scope", gate.Scope{}},
	}
}

// TestDecisionRecordServiceRejectsHostileInput sends a non-gdr- ID and a forged
// scope straight at the service, and a record of another scope back from the
// load. Each one fails with a sentinel, and the store is never asked for a
// forged scope or a malformed ID.
func TestDecisionRecordServiceRejectsHostileInput(t *testing.T) {
	t.Parallel()

	t.Run("a non-gdr- ID is rejected before the load", func(t *testing.T) {
		t.Parallel()

		cases := []string{
			"",
			"gdr-",
			"gdr-abc",
			"gdr-a1b2c3d4e5f6g7h8i9j0extra",
			"GDR-A1B2C3D4E5F6G7H8I9J0",
			"cho-a1b2c3d4e5f6g7h8i9j0",
			"gdr-a1b2c3d4e5f6g7h8i9j-",
			"gdr-a1b2c3d4e5f6g7h8i9j ",
			"x' OR '1'='1",
			"gdr-a1b2c3%00",
		}

		for _, id := range cases {
			records := &hostileRecords{}
			svc := mustService(t, records)

			_, err := svc.Read(t.Context(), gate.NamespaceScope("acme"), id)
			if !errors.Is(err, gate.ErrInvalid) {
				t.Errorf("Read(%q) = %v, want an error that wraps gate.ErrInvalid", id, err)
			}

			if records.gets != 0 {
				t.Errorf("Read(%q) asked the store %d times, want 0", id, records.gets)
			}
		}
	})

	t.Run("a forged scope is rejected before the load", func(t *testing.T) {
		t.Parallel()

		for _, tc := range forgedScopes() {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				records := &hostileRecords{}

				svc := mustService(t, records)

				_, err := svc.Read(t.Context(), tc.scope, recordID)
				if !errors.Is(err, gate.ErrInvalid) {
					t.Fatalf("Read(scope %s) = %v, want an error that wraps gate.ErrInvalid", tc.scope, err)
				}

				if records.gets != 0 {
					t.Fatalf("Read(scope %s) asked the store %d times, want 0", tc.scope, records.gets)
				}
			})
		}
	})

	t.Run("a record of another scope back from the load is rejected", func(t *testing.T) {
		t.Parallel()

		at := time.UnixMilli(1_700_000_000_000).UTC()

		cases := []struct {
			name  string
			scope gate.Scope
			record gate.Record
		}{
			{
				name:  "a namespace scope reading a record of another namespace",
				scope: gate.NamespaceScope("acme"),
				record: gate.Record{
					ID: gate.RecordID(recordID), Point: gate.PointStateWrite,
					Change: gate.ChangeRef{Kind: gate.ChangeStateWrite, ID: ""}, Namespace: "other",
					At: at, Subject: "bo@example.com", Schema: gate.SchemaVersion{Subject: "state.write", Major: 1, Minor: 0},
					Result: gate.ResultDeny, Count: 1, LastAt: at,
				},
			},
			{
				name:  "the installation scope reading a record of a namespace",
				scope: gate.InstallationScope(),
				record: gate.Record{
					ID: gate.RecordID(recordID), Point: gate.PointStateWrite,
					Change: gate.ChangeRef{Kind: gate.ChangeStateWrite, ID: ""}, Namespace: "acme",
					At: at, Subject: "bo@example.com", Schema: gate.SchemaVersion{Subject: "state.write", Major: 1, Minor: 0},
					Result: gate.ResultDeny, Count: 1, LastAt: at,
				},
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				records := &hostileRecords{mu: sync.Mutex{}, record: tc.record, gets: 0}

				svc := mustService(t, records)

				_, err := svc.Read(t.Context(), tc.scope, recordID)
				if !errors.Is(err, gate.ErrNotFound) {
					t.Fatalf("Read = %v, want an error that wraps gate.ErrNotFound", err)
				}

				if records.gets != 1 {
					t.Fatalf("the service asked the store %d times, want 1", records.gets)
				}
			})
		}
	})

	t.Run("an empty unit of the scope is rejected before the load", func(t *testing.T) {
		t.Parallel()

		// A namespace name that cannot be a namespace name, so the scope
		// handed to the service is checked once more here.
		records := &hostileRecords{}
	svc := mustService(t, records)

		_, err := svc.Read(t.Context(), gate.NamespaceScope(""), recordID)
		if !errors.Is(err, gate.ErrInvalid) {
			t.Fatalf("Read = %v, want an error that wraps gate.ErrInvalid", err)
		}
	})
}

// TestDecisionRecordServiceAcceptsAScopeOfThePath proves that the service
// accepts the scope of each route of the change: a namespace scope and the
// installation scope.
func TestDecisionRecordServiceAcceptsAScopeOfThePath(t *testing.T) {
	t.Parallel()

	at := time.UnixMilli(1_700_000_000_000).UTC()

	for _, tc := range []struct {
		name  string
		scope gate.Scope
		ns    namespace.Name
	}{
		{"namespace scope", gate.NamespaceScope("acme"), "acme"},
		{"installation scope", gate.InstallationScope(), "@server"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			record := gate.Record{
				ID: gate.RecordID(recordID), Point: gate.PointStateWrite,
				Change: gate.ChangeRef{Kind: gate.ChangeAccess, ID: ""}, Namespace: tc.ns,
				At: at, Subject: "bo@example.com", Schema: gate.SchemaVersion{Subject: "access", Major: 1, Minor: 0},
				Result: gate.ResultDeny, Count: 1, LastAt: at,
			}
			records := &hostileRecords{mu: sync.Mutex{}, record: record, gets: 0}

			svc := mustService(t, records)

			got, err := svc.Read(t.Context(), tc.scope, recordID)
			if err != nil {
				t.Fatalf("Read = %v, want nil", err)
			}

			if got.ID != record.ID {
				t.Errorf("id = %q, want %q", got.ID, record.ID)
			}
		})
	}
}

