package sqlite

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/gatestore"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// White-box: the runner of the gate store refuses a write at a unit scope with
// no live unit before the write runs, so a black-box test never reaches the
// checks of the transaction. Each case sends the hostile input to one check,
// as if the other checks had failed.
//
// TestGateUnitChecks checks that a write of the transaction at a unit scope
// with no live unit returns gate.ErrUnitNotFound, and that a write at a unit
// scope with a unit ID, a read with an ID that names no unit, an empty unit ID
// in the inventory, a unit row that names no unit and a purge with no unit ID
// fail closed with gate.ErrInvalid.
func TestGateUnitChecks(t *testing.T) {
	t.Parallel()

	s := openInternalTemplated(t)

	gateUnitFixture(t, s)

	for _, stmt := range []string{
		// A unit row of a unit that the inventory does not hold.
		`INSERT INTO gate_objects (namespace_id, scope_key, kind, id, level, unit_id, version, body, updated_at_ms)
		 SELECT id, 'unit/ws-gone', 'gate', 'x', 'unit', 'ws-gone', 1, '{}', 1 FROM namespaces WHERE name = 'acme'`,
		// A unit of the inventory with an empty ID.
		`UPDATE inventory SET unit_id = '' WHERE namespace = 'acme' AND name = 'blank'`,
	} {
		if _, err := s.conn.ExecContext(t.Context(), stmt); err != nil {
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
		{"an insert at a unit scope with a unit ID", func(tx *gateTx) error {
			return tx.Insert(t.Context(), gateScopeByID(t, "opentofu", "net", "ws-net"), row, time.Now())
		}, gate.ErrInvalid},
		{"a read with the ID of another unit", func(tx *gateTx) error {
			_, err := tx.List(t.Context(), gateScopeByID(t, "opentofu", "net", "ws-gone"), row.Kind)

			return err
		}, gate.ErrInvalid},
		{"a read with the real ID of a unit of another namespace", func(tx *gateTx) error {
			// The workspace net of globex exists. Its ID with the scope of the
			// workspace net of acme names a unit of another namespace.
			byID := gateScopeByID(t, "opentofu", "net", "ws-globex-net")

			return tx.checkUnitID(t.Context(), gateNamespaceIDOfAcme(t, tx), byID)
		}, gate.ErrInvalid},
		{"a read with the real ID of a unit of another tool", func(tx *gateTx) error {
			// The workspace net of acme exists. Its ID with the scope of a
			// Pulumi stack names a unit of another tool.
			byID := gateScopeByID(t, "pulumi", "web/prod", "ws-net")

			return tx.checkUnitID(t.Context(), gateNamespaceIDOfAcme(t, tx), byID)
		}, gate.ErrInvalid},
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
		createGateUnitWorkspace(t, s, ns, workspace.ID("ws-"+string(name)), name)
	}

	// The namespace globex has a workspace net too, with an ID of its own.
	other := namespace.Namespace{
		ID: namespace.NewID(), CreatedAt: time.Now(), Name: "globex", PrimaryInstallation: "eu-west",
		FailoverVersion: 1, Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}
	if err := s.CreateNamespace(t.Context(), other); err != nil {
		t.Fatalf("create namespace globex: %v", err)
	}

	createGateUnitWorkspace(t, s, other, "ws-globex-net", "net")
}

// createGateUnitWorkspace makes the OpenTofu workspace name with the ID id in
// the namespace ns.
func createGateUnitWorkspace(t *testing.T, s *Store, ns namespace.Namespace, id workspace.ID, name workspace.Name) {
	t.Helper()

	w := workspace.Workspace{
		NamespaceID: "",
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
		Lock:        workspace.Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""},
		LastRelease: workspace.NoRelease(),
		ID:          id,
		Ref:         workspace.Ref{Namespace: ns.Name, Name: name},
		Overrides: namespace.Overrides{
			Forced:   false,
			Unlocked: false,
		},
		Revision: 1,
		Origin:   identity.StateOrigin{Job: "", Commit: ""},
	}
	if err := s.CreateWorkspace(t.Context(), w, ns.Fence()); err != nil {
		t.Fatalf("create workspace %s/%s: %v", ns.Name, name, err)
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

// gateObjectInsert returns an INSERT of one gate_objects row of acme.
func gateObjectInsert(id, scopeKey, level, unitID string) string {
	return "INSERT INTO gate_objects (namespace_id, scope_key, kind, id, level, unit_id, version, body, updated_at_ms) " +
		"SELECT id, '" + scopeKey + "', 'gate', '" + id + "', '" + level + "', '" + unitID + "', 1, '{}', 1 " +
		"FROM namespaces WHERE name = 'acme'"
}

// White-box: the store writes a unit row only through writeScopeOf, which
// builds the key from the ID, so a black-box test never writes a bad row. The
// cases write rows straight to the table, as if every check of the store had
// failed.
//
// TestGateObjectChecks checks that the CHECK constraints of gate_objects
// refuse a unit row with no unit ID, a unit row whose key is not the key of
// its ID, and a row of another level with a unit ID, and take a good unit row.
func TestGateObjectChecks(t *testing.T) {
	t.Parallel()

	s := openInternalTemplated(t)

	gateUnitFixture(t, s)

	tests := []struct {
		name   string
		stmt   string
		refuse bool
	}{
		{"a unit row with no unit ID", gateObjectInsert("a", "unit/", "unit", ""), true},
		{"a unit row whose key names another ID", gateObjectInsert("b", "unit/ws-blank", "unit", "ws-net"), true},
		{"a unit row whose key is a name key", gateObjectInsert("c", "unit/acme/opentofu/net", "unit", "ws-net"), true},
		{"a namespace row with a unit ID", gateObjectInsert("d", "namespace/acme", "namespace", "ws-net"), true},
		{"a good unit row", gateObjectInsert("e", "unit/ws-net", "unit", "ws-net"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			stmt := tt.stmt
			_, err := s.conn.ExecContext(t.Context(), stmt)

			if tt.refuse && err == nil {
				t.Fatalf("%s: the insert passed, want the CHECK constraint to refuse it", tt.name)
			}

			if !tt.refuse && err != nil {
				t.Fatalf("%s: %v, want the insert to pass", tt.name, err)
			}
		})
	}
}

// gateNamespaceIDOfAcme returns the stable ID of the namespace acme in the
// transaction of tx.
func gateNamespaceIDOfAcme(t *testing.T, tx *gateTx) string {
	t.Helper()

	id, ok, err := tx.namespaceID(t.Context(), "acme")
	if err != nil || !ok {
		t.Fatalf("namespace ID of acme = %q, %v, %v", id, ok, err)
	}

	return id
}

// gateScopeByID returns the scope of the unit name of tool in acme, with the
// stable ID id.
func gateScopeByID(t *testing.T, tool, name, id string) gate.Scope {
	t.Helper()

	scope, err := gate.UnitScope("acme", tool, name)
	if err != nil {
		t.Fatalf("unit scope %s: %v", name, err)
	}

	byID, err := scope.WithUnitID(id)
	if err != nil {
		t.Fatalf("unit scope %s with the ID %s: %v", name, id, err)
	}

	return byID
}
