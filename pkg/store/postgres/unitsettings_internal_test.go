package postgres

import (
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/unitsettings"
)

// settingsRig is a store with one namespace and one stack, and the settings
// of that stack.
type settingsRig struct {
	s     *Store
	ns    namespace.Namespace
	ref   stack.Ref
	id    stack.ID
	unit  identity.Object
	store unitsettings.Store
}

func newSettingsRig(t *testing.T) settingsRig {
	t.Helper()

	at := time.UnixMilli(1_700_000_000_000).UTC()

	s, err := Open(t.Context(), membershipURL(t))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(s.Close)

	ns := namespace.Namespace{
		ID: namespace.NewID(), CreatedAt: at, Name: "acme", PrimaryInstallation: "eu-west", FailoverVersion: 1,
		Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}
	if createErr := s.CreateNamespace(t.Context(), ns); createErr != nil {
		t.Fatalf("create namespace: %v", createErr)
	}

	ref, err := stack.ParseRef("acme", "web", "dev")
	if err != nil {
		t.Fatalf("parse stack reference: %v", err)
	}

	id := stack.ID(ident.NewID("stk-"))
	st := stack.Stack{
		NamespaceID: "", CreatedAt: at, UpdatedAt: at, Tags: map[string]string{}, Ref: ref, ID: id, Version: 0,
	}

	if createErr := s.CreateStack(t.Context(), st, ns.Fence()); createErr != nil {
		t.Fatalf("create stack: %v", createErr)
	}

	unit, err := identity.UnitObject("pulumi", ns.Name, "web/dev")
	if err != nil {
		t.Fatalf("unit reference: %v", err)
	}

	return settingsRig{s: s, ns: ns, ref: ref, id: id, unit: unit.WithUnitID(id.String()), store: s.UnitSettings()}
}

func (r settingsRig) settings() unitsettings.Settings {
	return unitsettings.Settings{
		Unit: r.unit, Executor: "", Pool: "", Steps: nil, Version: 0, UpdatedAt: time.Time{},
		CodeLocation:   unitsettings.CodeLocation{Connection: "", Repository: "acme/infra", Ref: "main", Directory: ""},
		WriteBack:      unitsettings.WriteBack{Delivery: "", Files: nil},
		ReviewRequests: unitsettings.ReviewRequests{Enabled: false},
	}
}

func (r settingsRig) put(t *testing.T, s *unitsettings.Settings, expected int64) error {
	t.Helper()

	_, err := r.store.Put(t.Context(), s, expected, r.ns.Fence(),
		membershipEntry(time.UnixMilli(1_700_000_100_000).UTC(), r.ns.Name, "unit:manage"))

	return err
}

// seedDefaults stores a default pool for the namespace of the rig.
func (r settingsRig) seedDefaults(t *testing.T) {
	t.Helper()

	d := unitsettings.NamespaceDefaults{
		Namespace: r.ns.Name, Pool: "rp-0123456789abcdef", Version: 0, UpdatedAt: time.Time{},
	}
	e := membershipEntry(time.UnixMilli(1_700_000_100_000).UTC(), r.ns.Name, "namespace:manage")

	if _, err := r.store.SetDefaults(t.Context(), d, 0, r.ns.Fence(), e); err != nil {
		t.Fatalf("SetDefaults: %v", err)
	}
}

// seedSettings stores the settings of the unit of the rig.
func (r settingsRig) seedSettings(t *testing.T) {
	t.Helper()

	s := r.settings()
	if err := r.put(t, &s, 0); err != nil {
		t.Fatalf("Put: %v", err)
	}
}

func (r settingsRig) rows(t *testing.T) int {
	t.Helper()

	var n int
	if err := r.s.pool.QueryRow(t.Context(), "SELECT count(*) FROM unit_settings").Scan(&n); err != nil {
		t.Fatalf("count unit settings: %v", err)
	}

	return n
}

// TestPurgeDeletesUnitSettingsInTheFencedTransaction checks that the purge of
// a unit removes its settings only when the fence is current.
func TestPurgeDeletesUnitSettingsInTheFencedTransaction(t *testing.T) {
	t.Parallel()

	r := newSettingsRig(t)
	s := r.settings()

	if err := r.put(t, &s, 0); err != nil {
		t.Fatalf("Put: %v", err)
	}

	e := membershipEntry(time.UnixMilli(1_700_000_200_000).UTC(), r.ns.Name, "stack:delete")
	d := inventory.Deletion{Entry: e, PurgeAfter: e.At.Add(time.Hour)}

	if err := r.s.DeleteStack(t.Context(), r.ref, r.ns.Fence(), stack.EmptyGuard(), d); err != nil {
		t.Fatalf("delete stack: %v", err)
	}

	if got := r.rows(t); got != 1 {
		t.Fatalf("settings rows after the soft delete = %d, want 1: the record waits for the purge", got)
	}

	stale := namespace.Fence{Namespace: r.ns.Name, FailoverVersion: r.ns.FailoverVersion + 1}

	if err := r.s.PurgeStack(t.Context(), r.id, stale, time.Time{}, e); !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("purge with a stale fence = %v, want ErrFenced", err)
	}

	if got := r.rows(t); got != 1 {
		t.Fatalf("settings rows after a refused purge = %d, want 1", got)
	}

	if err := r.s.PurgeStack(t.Context(), r.id, r.ns.Fence(), time.Time{}, e); err != nil {
		t.Fatalf("purge: %v", err)
	}

	if got := r.rows(t); got != 0 {
		t.Fatalf("settings rows after the purge = %d, want 0", got)
	}
}

// TestRenameKeepsUnitSettings checks that a rename of the unit keeps its
// record, because the record is keyed by the stable unit ID.
func TestRenameKeepsUnitSettings(t *testing.T) {
	t.Parallel()

	r := newSettingsRig(t)
	s := r.settings()

	if err := r.put(t, &s, 0); err != nil {
		t.Fatalf("Put: %v", err)
	}

	e := membershipEntry(time.UnixMilli(1_700_000_300_000).UTC(), r.ns.Name, "unit:manage")
	if err := r.s.RenameStack(t.Context(), r.ref, stack.Name("prod"), r.ns.Fence(), e); err != nil {
		t.Fatalf("rename stack: %v", err)
	}

	renamed, err := identity.UnitObject("pulumi", r.ns.Name, "web/prod")
	if err != nil {
		t.Fatalf("unit reference: %v", err)
	}

	got, err := r.store.Get(t.Context(), renamed.WithUnitID(r.id.String()))
	if err != nil || got.CodeLocation.Repository != "acme/infra" {
		t.Fatalf("Get after the rename = %+v, %v, want the record of the unit", got, err)
	}

	if _, err := r.store.Get(t.Context(), r.unit); !errors.Is(err, unitsettings.ErrUnknownUnit) {
		t.Fatalf("Get under the old name = %v, want ErrUnknownUnit", err)
	}
}

// TestStoredSettingsThatFailTheChecksAreInvalid checks that a stored body that
// the checks refuse reads as ErrInvalid and not as a value, as a row from
// another version or a restore can be.
func TestStoredSettingsThatFailTheChecksAreInvalid(t *testing.T) {
	t.Parallel()

	for name, body := range map[string]string{
		"unknown executor":  `{"executor":"shell"}`,
		"directory escapes": `{"codeLocation":{"directory":"../x"}}`,
		"unknown field":     `{"extra":true}`,
		"not json":          `nope`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			checkCorruptBody(t, body)
		})
	}
}

// TestParallelWritersWithOneVersionGiveOneWinner starts writers that all read
// version 0. One wins and the others get ErrConflict.
func TestParallelWritersWithOneVersionGiveOneWinner(t *testing.T) {
	t.Parallel()

	r := newSettingsRig(t)

	const writers = 8

	var (
		wins, conflicts atomic.Int32
		wg              sync.WaitGroup
	)

	for i := range writers {
		wg.Go(func() {
			s := r.settings()
			s.CodeLocation.Ref = "branch-" + strconv.Itoa(i)

			err := r.put(t, &s, 0)

			switch {
			case err == nil:
				wins.Add(1)
			case errors.Is(err, unitsettings.ErrConflict):
				conflicts.Add(1)
			default:
				t.Errorf("Put: %v", err)
			}
		})
	}

	wg.Wait()

	if wins.Load() != 1 || conflicts.Load() != writers-1 {
		t.Fatalf("wins = %d, conflicts = %d, want 1 and %d", wins.Load(), conflicts.Load(), writers-1)
	}

	if got := r.rows(t); got != 1 {
		t.Fatalf("settings rows = %d, want 1", got)
	}
}

// TestCheckStagedUnitSettings proves the restore check: it accepts the rows
// that the store wrote, and it refuses a row that a new write would not pass.
func TestCheckStagedUnitSettings(t *testing.T) {
	t.Parallel()

	t.Run("rows that the store wrote pass", func(t *testing.T) {
		t.Parallel()

		r := newSettingsRig(t)
		r.seedSettings(t)
		r.seedDefaults(t)

		if err := checkStagedUnitSettings(t.Context(), r.s.pool, currentSchema(t, r.s)); err != nil {
			t.Fatalf("check = %v, want nil", err)
		}
	})

	for name, update := range map[string]string{
		"a body with an unknown executor": `UPDATE unit_settings SET body = '{"executor":"shell"}'`,
		"a body that is not JSON":         `UPDATE unit_settings SET body = 'nope'`,
		"a directory that leaves":         `UPDATE unit_settings SET body = '{"codeLocation":{"directory":"/etc"}}'`,
		"defaults with a bad pool":        `UPDATE unit_defaults SET pool = 'x y'`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r := newSettingsRig(t)
			r.seedSettings(t)
			r.seedDefaults(t)

			if _, err := r.s.pool.Exec(t.Context(), update); err != nil {
				t.Fatalf("change the row: %v", err)
			}

			err := checkStagedUnitSettings(t.Context(), r.s.pool, currentSchema(t, r.s))
			if !errors.Is(err, backup.ErrCorrupt) {
				t.Fatalf("check = %v, want ErrCorrupt", err)
			}
		})
	}
}

// checkCorruptBody stores settings, replaces the stored body with body, and
// checks that a read refuses it and that a valid write repairs it.
func checkCorruptBody(t *testing.T, body string) {
	t.Helper()

	r := newSettingsRig(t)
	s := r.settings()

	if err := r.put(t, &s, 0); err != nil {
		t.Fatalf("Put: %v", err)
	}

	if _, err := r.s.pool.Exec(t.Context(), "UPDATE unit_settings SET body = $1", body); err != nil {
		t.Fatalf("corrupt the row: %v", err)
	}

	if _, err := r.store.Get(t.Context(), r.unit); !errors.Is(err, unitsettings.ErrInvalid) {
		t.Fatalf("Get = %v, want ErrInvalid", err)
	}

	// A valid write repairs the row.
	if err := r.put(t, &s, 1); err != nil {
		t.Fatalf("Put over a row that fails the checks: %v", err)
	}

	if _, err := r.store.Get(t.Context(), r.unit); err != nil {
		t.Fatalf("Get after the repair: %v", err)
	}
}
