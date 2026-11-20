package decisionrecord_test

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/internal/store/sqlite"
)

// TestDecisionRecordPortQueryHostileID proves that the port query takes the
// ID of a record and the name of a namespace as data, never as SQL: an ID
// with SQL metacharacters gives no row, and the stored record survives the
// attempts.
func TestDecisionRecordPortQueryHostileID(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	path := filepath.Join(t.TempDir(), "gate-records.db")

	s, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	defer func() {
		if closeErr := s.Close(); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	}()

	ns := namespace.Namespace{
		ID: namespace.NewID(), CreatedAt: time.UnixMilli(1_700_000_000_000).UTC(), Name: "acme",
		PrimaryInstallation: "test-region", FailoverVersion: 1,
		Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}
	if err = s.CreateNamespace(ctx, ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	fence := ns.Fence()

	at := time.UnixMilli(1_700_000_000_000).UTC()
	record := gate.Record{
		ID: gate.NewRecordID(), Point: gate.PointStateWrite,
		Change: gate.ChangeRef{Kind: gate.ChangeAccess, ID: ""}, Namespace: "acme",
		At: at, Subject: "bo@example.com", Schema: gate.SchemaVersion{Subject: "access", Major: 1, Minor: 0},
		PathSets: nil, Units: nil, Requirements: nil, Warnings: nil, AutoApply: false, Controls: nil,
		BreakGlass: "", Result: gate.ResultDeny, Action: "state.read", Count: 1, LastAt: at,
	}
	if _, err = s.Records().NoteRefusal(ctx, record, gate.AccessRefusalWindow, fence); err != nil {
		t.Fatalf("seed the record: %v", err)
	}

	port := s.Records()

	hostile := []string{
		"gdr-x' OR '1'='1' --",
		"gdr-a1b2c3d4e5f6' UNION SELECT body FROM gate_records --",
		"gdr-'; DROP TABLE gate_records; --",
		"x' OR '1'='1",
		"gdr-a1b2c3d4e5f6g7h8i9j\x00",
		"%' OR 1=1 --",
	}

	for _, id := range hostile {
		_, err = port.Get(ctx, "acme", gate.RecordID(id))
		if !errors.Is(err, gate.ErrNotFound) {
			t.Errorf("Get(%q) = %v, want an error that wraps gate.ErrNotFound", id, err)
		}

		// The hostile ID is a namespace name too, on the other side of the
		// predicate.
		_, err = port.Get(ctx, namespace.Name(id), record.ID)
		if err == nil {
			t.Errorf("Get(namespace %q) = the record, want no record", id)
		}
	}

	// The stored record survives the attempts, and the port reads it back.
	got, err := port.Get(ctx, "acme", record.ID)
	if err != nil {
		t.Fatalf("Get(%q) after the hostile attempts = %v, want nil", record.ID, err)
	}

	if got.ID != record.ID {
		t.Errorf("id = %q, want %q", got.ID, record.ID)
	}

	// An unknown ID in an existing namespace answers ErrNotFound.
	if _, err := port.Get(ctx, "acme", gate.NewRecordID()); !errors.Is(err, gate.ErrNotFound) {
		t.Errorf("Get(unknown ID) = %v, want an error that wraps gate.ErrNotFound", err)
	}
}
