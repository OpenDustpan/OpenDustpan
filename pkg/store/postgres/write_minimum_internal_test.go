package postgres

import (
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/store/internal/migrate"
	"opendustpan.org/opendustpan/pkg/store/schema"
)

// contractMigration returns a migration that sets the minimum binary version
// to minimum.
func contractMigration(minimum string) migrate.Migration {
	return migrate.Migration{
		Name: "9999_contract.sql", SQL: "SELECT 1", MinBinaryVersion: minimum, SHA256: "", Version: 9999,
	}
}

// openAt opens the schema at url as a binary of version binary.
func openAt(t *testing.T, url, binary string) *Store {
	t.Helper()

	s, err := Open(t.Context(), url, WithBinaryVersion(binary))
	if err != nil {
		t.Fatalf("open the binary %q: %v", binary, err)
	}

	t.Cleanup(s.Close)

	return s
}

// applyContract applies a contract migration with the minimum v0.5.0 to the schema
// of s, as the new binary of a rolling deploy does.
func applyContract(t *testing.T, s *Store) {
	t.Helper()

	err := pgx.BeginFunc(t.Context(), s.pool, func(tx pgx.Tx) error {
		return apply(t.Context(), tx, contractMigration("v0.5.0"))
	})
	if err != nil {
		t.Fatalf("apply the contract migration: %v", err)
	}
}

// checkInstancesTx runs the migration check of a contract migration with the
// minimum in a transaction of s.
func checkInstancesTx(t *testing.T, s *Store, minimum string) error {
	t.Helper()

	err := pgx.BeginFunc(t.Context(), s.pool, func(tx pgx.Tx) error {
		return checkInstances(t.Context(), tx, []migrate.Migration{contractMigration(minimum)})
	})
	if err != nil {
		return fmt.Errorf("check instances: %w", err)
	}

	return nil
}

// writeCase is one binary version and whether a write of that binary fails
// after the minimum went up to v0.5.0.
type writeCase struct {
	name    string
	binary  string
	wantErr bool
}

// writeAttempt is one write of the store, with the name of the check that
// it passes: the state check alone, or the state and the fence.
type writeAttempt struct {
	do   func() error
	name string
}

// checkWriteResult fails when the result of a write is not what the case
// wants: success, or a MinimumError that names both versions.
func checkWriteResult(t *testing.T, tt writeCase, attempt string, err error) {
	t.Helper()

	if !tt.wantErr {
		if err != nil {
			t.Errorf("%s: write of %q = %v, want nil", attempt, tt.binary, err)
		}

		return
	}

	below, ok := errors.AsType[*schema.MinimumError](err)
	if !ok || !errors.Is(err, schema.ErrBinaryBelowMinimum) {
		t.Errorf("%s: write of %q = %v, want a MinimumError", attempt, tt.binary, err)

		return
	}

	if below.SchemaMinimum != "v0.5.0" || below.BinaryVersion != tt.binary {
		t.Errorf("%s: error = %+v, want a MinimumError that names v0.5.0 and %q", attempt, below, tt.binary)
	}
}

// TestWriteBelowMinimum proves that a write of a binary below the minimum
// fails in its own transaction, also when the binary opened before the
// minimum went up. The old handle stands for an instance that still runs
// during a rolling deploy, and a second handle stands for the new binary that
// raises the minimum.
func TestWriteBelowMinimum(t *testing.T) {
	t.Parallel()

	tests := []writeCase{
		{name: "below the minimum", binary: "v0.4.9", wantErr: true},
		{name: "far below the minimum", binary: "v0.1.0", wantErr: true},
		{name: "at the minimum", binary: "v0.5.0", wantErr: false},
		{name: "above the minimum", binary: "v0.6.0", wantErr: false},
		{name: "dev build", binary: "dev", wantErr: false},
		{name: "no version", binary: "", wantErr: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			url := membershipURL(t)
			old := openAt(t, url, tt.binary)
			ns := createAcme(t, old)

			applyContract(t, openAt(t, url, "v0.5.0"))

			for _, attempt := range writeAttempts(t, old, ns) {
				checkWriteResult(t, tt, attempt.name, attempt.do())
			}
		})
	}
}

// createAcme creates the namespace acme.
func createAcme(t *testing.T, s *Store) namespace.Namespace {
	t.Helper()

	ns := namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           time.Date(2026, time.October, 12, 12, 0, 0, 0, time.UTC),
		Name:                "acme",
		PrimaryInstallation: "eu-west",
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	}

	if err := s.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	return ns
}

// writeAttempts returns one write for each read-state statement: a namespace
// create (state check) and a stack create (state and fence check).
func writeAttempts(t *testing.T, s *Store, ns namespace.Namespace) []writeAttempt {
	t.Helper()

	other := ns
	other.ID = namespace.NewID()
	other.Name = "globex"

	ref, err := stack.ParseRef("acme", "web", "prod")
	if err != nil {
		t.Fatalf("parse ref: %v", err)
	}

	st := stack.Stack{
		NamespaceID: "", CreatedAt: ns.CreatedAt, UpdatedAt: ns.CreatedAt, Tags: map[string]string{}, Ref: ref,
		ID: stack.ID(ident.NewID("stk-")), Version: 0,
	}

	return []writeAttempt{
		{name: "state check only", do: func() error { return s.CreateNamespace(t.Context(), other) }},
		{name: "state and fence check", do: func() error { return s.CreateStack(t.Context(), st, ns.Fence()) }},
	}
}

// TestControlWriteIgnoresMinimum proves that the control path takes no
// minimum check: an instance that the minimum left behind can still delete
// its own version row at a clean stop.
func TestControlWriteIgnoresMinimum(t *testing.T) {
	t.Parallel()

	url := membershipURL(t)
	old := openAt(t, url, "v0.4.0")

	row := installation.Instance{StartedAt: time.UnixMilli(1_700_000_000_000), ID: "old", BinaryVersion: "v0.4.0"}
	if err := old.RegisterInstance(t.Context(), row); err != nil {
		t.Fatalf("register: %v", err)
	}

	applyContract(t, openAt(t, url, "v0.5.0"))

	if err := old.UnregisterInstance(t.Context(), "old"); err != nil {
		t.Fatalf("unregister after the minimum went up: %v", err)
	}
}

// waitCase is one set of instance rows, the minimum that a contract
// migration sets, and the IDs that the refusal names.
type waitCase struct {
	name      string
	minimum   string
	rows      []installation.Instance
	wantNamed []string
}

// waitCases returns the cases of TestContractMigrationWaitsForInstances.
func waitCases() []waitCase {
	start := time.UnixMilli(1_700_000_000_000)

	return []waitCase{
		{name: "no rows", minimum: "v0.5.0", rows: nil, wantNamed: nil},
		{
			name: "row below", minimum: "v0.5.0", wantNamed: []string{"old"},
			rows: []installation.Instance{{StartedAt: start, ID: "old", BinaryVersion: "v0.4.0"}},
		},
		{
			name: "only the rows below are named", minimum: "v0.5.0", wantNamed: []string{"old-1", "old-2"},
			rows: []installation.Instance{
				{StartedAt: start, ID: "old-1", BinaryVersion: "v0.4.9"},
				{StartedAt: start.Add(time.Second), ID: "old-2", BinaryVersion: "v0.1.0"},
				{StartedAt: start.Add(2 * time.Second), ID: "new", BinaryVersion: "v0.5.0"},
			},
		},
		{
			name: "rows at or above", minimum: "v0.5.0", wantNamed: nil,
			rows: []installation.Instance{
				{StartedAt: start, ID: "at", BinaryVersion: "v0.5.0"},
				{StartedAt: start, ID: "above", BinaryVersion: "v0.6.1"},
				{StartedAt: start, ID: "dev", BinaryVersion: "dev"},
			},
		},
		{
			name: "no minimum in the migration", minimum: "", wantNamed: nil,
			rows: []installation.Instance{{StartedAt: start, ID: "old", BinaryVersion: "v0.1.0"}},
		},
	}
}

// namedRows returns the IDs that an InstancesBelowMinimumError names, or
// false when err is no such error.
func namedRows(err error) ([]string, bool) {
	below, ok := errors.AsType[*schema.InstancesBelowMinimumError](err)
	if !ok || !errors.Is(err, schema.ErrInstancesBelowMinimum) {
		return nil, false
	}

	named := make([]string, 0, len(below.Instances))
	for _, row := range below.Instances {
		named = append(named, row.ID)
	}

	return named, true
}

// checkWait proves one case: check refuses with the wanted rows, and passes
// once delete removed them. A case that wants no rows passes at once.
func checkWait(t *testing.T, tt waitCase, check func() error, remove func(id string) error) {
	t.Helper()

	err := check()
	if len(tt.wantNamed) == 0 {
		if err != nil {
			t.Fatalf("check = %v, want nil", err)
		}

		return
	}

	named, ok := namedRows(err)
	if !ok || !slices.Equal(named, tt.wantNamed) {
		t.Fatalf("check = %v, want a refusal that names %v", err, tt.wantNamed)
	}

	// The migration waits for the rows: once an administrator deletes them,
	// the same check passes.
	for _, id := range tt.wantNamed {
		if delErr := remove(id); delErr != nil {
			t.Fatalf("delete %s: %v", id, delErr)
		}
	}

	if after := check(); after != nil {
		t.Fatalf("check after the delete = %v, want nil", after)
	}
}

// TestContractMigrationWaitsForInstances proves that the migration check
// refuses a migration that raises the minimum while an instance row is below
// it, names the rows, and passes once the rows are gone.
func TestContractMigrationWaitsForInstances(t *testing.T) {
	t.Parallel()

	for _, tt := range waitCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := openAt(t, membershipURL(t), "v0.5.0")

			for _, row := range tt.rows {
				if err := s.RegisterInstance(t.Context(), row); err != nil {
					t.Fatalf("register %s: %v", row.ID, err)
				}
			}

			checkWait(t, tt,
				func() error { return checkInstancesTx(t, s, tt.minimum) },
				func(id string) error { return s.DeleteInstance(t.Context(), id) },
			)
		})
	}
}

// TestMigrateRefusesContractWhileInstancesBelow proves the whole migration
// step: with an instance row below the new minimum, migrateTx applies
// nothing and names the row, and with no such row it records the migration.
func TestMigrateRefusesContractWhileInstancesBelow(t *testing.T) {
	t.Parallel()

	s := openAt(t, membershipURL(t), "v0.5.0")

	row := installation.Instance{StartedAt: time.UnixMilli(1_700_000_000_000), ID: "old", BinaryVersion: "v0.4.0"}
	if err := s.RegisterInstance(t.Context(), row); err != nil {
		t.Fatalf("register: %v", err)
	}

	all, err := migrate.Load(migrations, "migrations")
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}

	all = append(all, contractMigration("v0.5.0"))
	rules := migrateRules{binaryVersion: "v0.5.0", kind: "", noMigrate: false}

	run := func() error {
		return pgx.BeginFunc(t.Context(), s.pool, func(tx pgx.Tx) error {
			return migrateTx(t.Context(), tx, all, rules)
		})
	}

	named, ok := namedRows(run())
	if !ok || !slices.Equal(named, []string{"old"}) {
		t.Fatalf("migrate with a row below = %v, want a refusal that names the row old", named)
	}

	requireNotApplied(t, s, 9999)

	if delErr := s.DeleteInstance(t.Context(), "old"); delErr != nil {
		t.Fatalf("delete the row: %v", delErr)
	}

	if runErr := run(); runErr != nil {
		t.Fatalf("migrate after the row is deleted: %v", runErr)
	}

	requireApplied(t, s, 9999)
}

// newestApplied returns the newest applied migration of s.
func newestApplied(t *testing.T, s *Store) schema.AppliedMigration {
	t.Helper()

	applied, err := s.AppliedMigrations(t.Context())
	if err != nil || len(applied) == 0 {
		t.Fatalf("applied migrations = %v, %v", applied, err)
	}

	return applied[len(applied)-1]
}

// requireNotApplied fails when the newest applied migration of s is the one
// with version.
func requireNotApplied(t *testing.T, s *Store, version int64) {
	t.Helper()

	if last := newestApplied(t, s); last.Version == version {
		t.Fatalf("newest applied migration = %+v, want migration %d not applied", last, version)
	}
}

// requireApplied fails when the newest applied migration of s is not the one
// with version and the minimum v0.5.0.
func requireApplied(t *testing.T, s *Store, version int64) {
	t.Helper()

	if last := newestApplied(t, s); last.Version != version || last.MinBinaryVersion != "v0.5.0" {
		t.Fatalf("newest applied migration = %+v, want %d with the minimum v0.5.0", last, version)
	}
}
