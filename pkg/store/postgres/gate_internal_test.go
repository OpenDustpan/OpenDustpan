package postgres

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/gatestore"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

func gateRecordRow(id string) gatestore.RecordRow {
	return gatestore.RecordRow{
		ID: id, Point: "", ChangeKind: "access", ChangeID: "", AtMs: 1, LastAtMs: 1, Count: 1, RefusalKey: "",
		Body: "{}",
	}
}

// White-box: only a race between RecordExists and the INSERT reaches the unique
// violation, and a black-box test cannot make that race happen every time.
//
// TestInsertGateRecordUsedID checks that a second insert of one record ID
// returns gate.ErrConflict and not the error of the driver.
func TestInsertGateRecordUsedID(t *testing.T) {
	t.Parallel()

	s, err := Open(t.Context(), membershipURL(t))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(s.Close)

	err = s.inTx(t.Context(), func(q *db.Queries) error {
		tx := newGateTx(q)

		if first := tx.InsertRecord(t.Context(), audit.ServerScope, gateRecordRow("gdr-used")); first != nil {
			t.Fatalf("InsertRecord(first) = %v", first)
		}

		return tx.InsertRecord(t.Context(), audit.ServerScope, gateRecordRow("gdr-used"))
	})
	if !errors.Is(err, gate.ErrConflict) {
		t.Fatalf("InsertRecord(second) = %v, want gate.ErrConflict", err)
	}
}

// TestGateNamespaceID checks the namespace of a gate row: the empty name is
// invalid, audit.ServerScope is the installation, and an unknown namespace
// does not exist.
func TestGateNamespaceID(t *testing.T) {
	t.Parallel()

	s, err := Open(t.Context(), membershipURL(t))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(s.Close)

	tests := []struct {
		name   string
		ns     namespace.Name
		exists bool
		want   error
	}{
		{"empty name", "", false, gate.ErrInvalid},
		{"installation", audit.ServerScope, true, nil},
		{"unknown namespace", "ghost", false, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			id, ok, err := gateNamespaceIDOf(t, s, tt.ns)
			if !errors.Is(err, tt.want) {
				t.Fatalf("namespaceID(%q) = %v, want %v", tt.ns, err, tt.want)
			}

			if err == nil && (ok != tt.exists || id != "") {
				t.Fatalf("namespaceID(%q) = %q, %v, want an empty ID and %v", tt.ns, id, ok, tt.exists)
			}
		})
	}
}

// gateNamespaceIDOf reads the namespace ID of ns in a transaction of its own.
func gateNamespaceIDOf(t *testing.T, s *Store, ns namespace.Name) (string, bool, error) {
	t.Helper()

	var (
		id string
		ok bool
	)

	err := s.inTx(t.Context(), func(q *db.Queries) error {
		var nerr error

		id, ok, nerr = newGateTx(q).namespaceID(t.Context(), ns)

		return nerr
	})

	return id, ok, err
}

// TestInsertNewRecordChecksTheIDAndTheNamespace checks the one statement that
// inserts a record of a namespace, as if each other check had failed: it
// stores a new record, stores nothing for a used ID or for a namespace that
// does not exist, and refuses the installation and the empty name.
func TestInsertNewRecordChecksTheIDAndTheNamespace(t *testing.T) {
	t.Parallel()

	s := openGateRecordStore(t)

	ns := namespace.Namespace{
		ID:        namespace.NewID(),
		CreatedAt: time.UnixMilli(1_700_000_000_000).UTC(), Name: "acme", PrimaryInstallation: "eu-west",
		FailoverVersion: 1, Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}
	if err := s.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	err := s.inTx(t.Context(), func(q *db.Queries) error {
		checkInsertNewRecordID(t, newGateTx(q))
		checkInsertNewRecordNamespace(t, newGateTx(q))

		return nil
	})
	if err != nil {
		t.Fatalf("transaction: %v", err)
	}
}

// checkInsertNewRecordID checks that the insert stores a record with a new ID
// in acme, and stores nothing for a used ID.
func checkInsertNewRecordID(t *testing.T, tx *gateTx) {
	t.Helper()

	ctx := t.Context()
	id := string(gate.NewRecordID())

	if inserted, err := tx.InsertNewRecord(ctx, "acme", gateRecordRow(id)); err != nil || !inserted {
		t.Fatalf("InsertNewRecord(new ID) = %v, %v, want true", inserted, err)
	}

	used := gateRecordRow(id)
	used.Body = `{"second":true}`

	if inserted, err := tx.InsertNewRecord(ctx, "acme", used); err != nil || inserted {
		t.Fatalf("InsertNewRecord(used ID) = %v, %v, want false and no error", inserted, err)
	}

	if body, ok, err := tx.Record(ctx, "acme", id); err != nil || !ok || body != "{}" {
		t.Fatalf("Record() = %q, %v, %v, want the first body", body, ok, err)
	}
}

// checkInsertNewRecordNamespace checks that the insert stores nothing for a
// namespace that does not exist, and refuses the installation and the empty
// name.
func checkInsertNewRecordNamespace(t *testing.T, tx *gateTx) {
	t.Helper()

	ctx := t.Context()
	other := string(gate.NewRecordID())

	if inserted, err := tx.InsertNewRecord(ctx, "ghost", gateRecordRow(other)); err != nil || inserted {
		t.Fatalf("InsertNewRecord(unknown namespace) = %v, %v, want false and no error", inserted, err)
	}

	for _, bad := range []namespace.Name{"", audit.ServerScope} {
		if _, err := tx.InsertNewRecord(ctx, bad, gateRecordRow(other)); !errors.Is(err, gate.ErrInvalid) {
			t.Fatalf("InsertNewRecord(%q) = %v, want gate.ErrInvalid", bad, err)
		}
	}

	if exists, err := tx.RecordExists(ctx, other); err != nil || exists {
		t.Fatalf("RecordExists(refused ID) = %v, %v, want false", exists, err)
	}
}

// openGateRecordStore opens a store on a fresh migrated schema.
func openGateRecordStore(t *testing.T) *Store {
	t.Helper()

	s, err := Open(t.Context(), membershipURL(t))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(s.Close)

	return s
}
