package postgres

import (
	"errors"
	"testing"

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
