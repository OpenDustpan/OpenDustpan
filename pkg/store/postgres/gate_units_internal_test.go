package postgres

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/gatestore"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// White-box: the runner of the gate store refuses a write at a unit scope with
// no live unit before the write runs, so a black-box test never reaches the
// checks of the transaction. Each case sends the hostile input to one check,
// as if the other checks had failed.
//
// TestGateUnitChecks checks that a write of the transaction at a unit scope
// with no live unit returns gate.ErrUnitNotFound, that an empty unit ID in the
// inventory, a unit row that names no unit and a purge with no unit ID fail
// closed with gate.ErrInvalid.
func TestGateUnitChecks(t *testing.T) {
	t.Parallel()

	s, err := Open(t.Context(), membershipURL(t))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(s.Close)

	gateUnitFixture(t, s)

	for _, stmt := range []string{
		// A unit row of a unit that the inventory does not hold.
		`INSERT INTO gate_objects (namespace_id, scope_key, kind, id, level, unit_id, version, body, updated_at_ms)
		 SELECT id, 'unit/ws-gone', 'gate', 'x', 'unit', 'ws-gone', 1, '{}', 1 FROM namespaces WHERE name = 'acme'`,
		// A unit of the inventory with an empty ID.
		`UPDATE inventory SET unit_id = '' WHERE namespace = 'acme' AND name = 'blank'`,
	} {
		if _, err := s.pool.Exec(t.Context(), stmt); err != nil {
			t.Fatalf("exec %q: %v", stmt, err)
		}
	}

	ghost := gateUnitScope(t, "ghost")
	row := gatestore.Row{Kind: gatestore.KindGate, ID: "x", Version: 1, Body: "{}"}

	tests := []struct {
		name string
		call func(tx *gateTx) error
		want error
	}{
		{"an insert at a unit with no live unit", func(tx *gateTx) error {
			return tx.Insert(t.Context(), ghost, row, time.Now())
		}, gate.ErrUnitNotFound},
		{"an update at a unit with no live unit", func(tx *gateTx) error {
			_, err := tx.Update(t.Context(), ghost, row, 1, time.Now())

			return err
		}, gate.ErrUnitNotFound},
		{"a delete at a unit with no live unit", func(tx *gateTx) error {
			_, err := tx.Delete(t.Context(), ghost, row.Kind, row.ID, 1)

			return err
		}, gate.ErrUnitNotFound},
		{"references at a unit with no live unit", func(tx *gateTx) error {
			return tx.SetReferences(t.Context(), ghost, row.Kind, row.ID, nil)
		}, gate.ErrUnitNotFound},
		{"a unit with an empty ID in the inventory", func(tx *gateTx) error {
			_, _, err := tx.Get(t.Context(), gateUnitScope(t, "blank"), row.Kind, row.ID)

			return err
		}, gate.ErrInvalid},
		{"a unit row that names no unit", func(tx *gateTx) error {
			_, err := tx.Narrower(t.Context(), gate.NamespaceScope("acme"), row.Kind, row.ID)

			return err
		}, gate.ErrInvalid},
		{"a purge with no unit ID", func(tx *gateTx) error {
			_, err := purgeUnitGate(t.Context(), tx.q, unitGate{ns: "acme", tool: "opentofu", name: "net", id: ""}, 1)

			return err
		}, gate.ErrInvalid},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := s.inTx(t.Context(), func(q *db.Queries) error { return tt.call(newGateTx(q)) })
			if !errors.Is(err, tt.want) {
				t.Fatalf("%s = %v, want %v", tt.name, err, tt.want)
			}
		})
	}
}

// gateUnitFixture makes the namespace acme and its OpenTofu workspaces net and
// blank.
func gateUnitFixture(t *testing.T, s *Store) {
	t.Helper()

	ns := namespace.Namespace{
		ID: namespace.NewID(), CreatedAt: time.Now(), Name: "acme", PrimaryInstallation: "eu-west",
		FailoverVersion: 1, Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}
	if err := s.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	for _, name := range []workspace.Name{"net", "blank"} {
		w := workspace.Workspace{
			NamespaceID: "", CreatedAt: time.Now(), UpdatedAt: time.Now(),
			Lock:        workspace.Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""},
			LastRelease: workspace.NoRelease(), ID: workspace.ID("ws-" + string(name)),
			Ref:       workspace.Ref{Namespace: "acme", Name: name},
			Overrides: namespace.Overrides{Forced: false, Unlocked: false}, Revision: 1,
		}
		if err := s.CreateWorkspace(t.Context(), w, ns.Fence()); err != nil {
			t.Fatalf("create workspace %s: %v", name, err)
		}
	}
}

// gateUnitScope returns the scope of the OpenTofu workspace name of acme.
func gateUnitScope(t *testing.T, name string) gate.Scope {
	t.Helper()

	scope, err := gate.UnitScope("acme", "opentofu", name)
	if err != nil {
		t.Fatalf("unit scope %s: %v", name, err)
	}

	return scope
}
