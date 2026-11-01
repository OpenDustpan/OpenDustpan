package settingsstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/settingsstore"
	"opendustpan.org/opendustpan/pkg/unitsettings"
)

// fakeTx is a settingsstore.Tx over fixed answers. It counts the writes.
type fakeTx struct {
	row     *settingsstore.Row
	unitID  string
	wrote   int
	deleted int
	found   bool
}

func newFake(unitID string, found bool, row *settingsstore.Row) *fakeTx {
	return &fakeTx{row: row, unitID: unitID, wrote: 0, deleted: 0, found: found}
}

func (f *fakeTx) UnitID(context.Context, identity.Object) (string, bool, error) {
	return f.unitID, f.found, nil
}

func (f *fakeTx) Get(context.Context, namespace.Name, string) (settingsstore.Row, bool, error) {
	if f.row == nil {
		return zeroRow(), false, nil
	}

	return *f.row, true, nil
}

func (f *fakeTx) Insert(context.Context, settingsstore.Row) (bool, error) {
	f.wrote++

	return true, nil
}

func (f *fakeTx) Update(context.Context, settingsstore.Row, int64) (bool, error) {
	f.wrote++

	return true, nil
}

func (f *fakeTx) Delete(context.Context, namespace.Name, string, int64) (bool, error) {
	f.deleted++

	return true, nil
}

func (f *fakeTx) Defaults(context.Context, namespace.Name) (settingsstore.DefaultsRow, bool, error) {
	return settingsstore.DefaultsRow{Namespace: "", Pool: "", Version: 0, UpdatedAtMs: 0}, false, nil
}

func (f *fakeTx) InsertDefaults(context.Context, settingsstore.DefaultsRow) (bool, error) {
	f.wrote++

	return true, nil
}

func (f *fakeTx) UpdateDefaults(context.Context, settingsstore.DefaultsRow, int64) (bool, error) {
	f.wrote++

	return true, nil
}

func (f *fakeTx) NamespaceExists(context.Context, namespace.Name) (bool, error) { return false, nil }

func zeroRow() settingsstore.Row {
	return settingsstore.Row{UnitID: "", Namespace: "", Body: "", Version: 0, UpdatedAtMs: 0}
}

func storedRow(unitID, ns, body string, version int64) *settingsstore.Row {
	return &settingsstore.Row{UnitID: unitID, Namespace: ns, Body: body, Version: version, UpdatedAtMs: 1}
}

func settingsOf(unit identity.Object) unitsettings.Settings {
	return unitsettings.Settings{
		Unit: unit, Executor: "", Pool: "",
		CodeLocation:   unitsettings.CodeLocation{Connection: "", Repository: "", Ref: "", Directory: ""},
		WriteBack:      unitsettings.WriteBack{Delivery: "", Files: nil},
		ReviewRequests: unitsettings.ReviewRequests{Enabled: false}, Steps: nil, Version: 0, UpdatedAt: time.Time{},
	}
}

func testUnit(t *testing.T) identity.Object {
	t.Helper()

	u, err := identity.UnitObject("pulumi", "acme", "web/prod")
	if err != nil {
		t.Fatal(err)
	}

	return u.WithUnitID("stk-1")
}

func entry() audit.Entry {
	return audit.Entry{
		At: time.UnixMilli(1_700_000_000_000).UTC(), Namespace: "acme", Subject: "alice", IDPSubject: "",
		TokenID: "", Action: "x", Object: "acme", Version: "", ValuePath: "", ValueCount: nil, Changes: nil,
		SourceIP: "192.0.2.1", UserAgent: "", Channel: audit.ChannelAPI, Kind: audit.KindChange,
		Outcome: audit.OutcomeSuccess, RequestID: "", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0,
		FenceVersion: 0, PrincipalKind: "", JobID: "", Reason: "",
	}
}

func TestCheckEntryRefusesAnotherNamespace(t *testing.T) {
	t.Parallel()

	e := entry()

	err := settingsstore.CheckEntry(&e, "beta")
	if !errors.Is(err, unitsettings.ErrInvalid) || !errors.Is(err, audit.ErrInvalidEntry) {
		t.Fatalf("CheckEntry = %v, want ErrInvalid and ErrInvalidEntry", err)
	}

	bad := entry()
	bad.Subject = ""

	if badErr := settingsstore.CheckEntry(&bad, "acme"); !errors.Is(badErr, unitsettings.ErrInvalid) {
		t.Fatalf("CheckEntry of an invalid entry = %v, want ErrInvalid", badErr)
	}

	if okErr := settingsstore.CheckEntry(&e, "acme"); okErr != nil {
		t.Fatalf("CheckEntry = %v, want nil", okErr)
	}
}

func TestCheckFenceRefusesAnotherNamespace(t *testing.T) {
	t.Parallel()

	err := settingsstore.CheckFence("acme", namespace.Fence{Namespace: "beta", FailoverVersion: 1})
	if !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("CheckFence = %v, want ErrFenced", err)
	}

	if okErr := settingsstore.CheckFence("acme", namespace.Fence{Namespace: "acme", FailoverVersion: 1}); okErr != nil {
		t.Fatalf("CheckFence = %v, want nil", okErr)
	}
}

func TestWriteRefusesAUnitThatIsNotTheLiveUnit(t *testing.T) {
	t.Parallel()

	for name, tx := range map[string]*fakeTx{
		"unit does not exist": newFake("", false, nil),
		"another unit ID":     newFake("stk-other", true, nil),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			s := settingsOf(testUnit(t))

			_, _, err := settingsstore.Write(t.Context(), tx, &s, 0, entry())
			if !errors.Is(err, unitsettings.ErrUnknownUnit) {
				t.Fatalf("Write = %v, want ErrUnknownUnit", err)
			}

			if tx.wrote != 0 {
				t.Fatalf("Write stored %d rows, want 0", tx.wrote)
			}
		})
	}
}

func TestWriteChecksTheVersionBeforeItStores(t *testing.T) {
	t.Parallel()

	row := storedRow("stk-1", "acme", `{}`, 3)

	for name, tc := range map[string]struct {
		row      *settingsstore.Row
		expected int64
	}{
		"row at another version":  {row: row, expected: 2},
		"create over a row":       {row: row, expected: 0},
		"update of a missing row": {row: nil, expected: 1},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tx := newFake("stk-1", true, tc.row)
			s := settingsOf(testUnit(t))

			_, _, err := settingsstore.Write(t.Context(), tx, &s, tc.expected, entry())
			if !errors.Is(err, unitsettings.ErrConflict) {
				t.Fatalf("Write = %v, want ErrConflict", err)
			}

			if tx.wrote != 0 {
				t.Fatalf("Write stored %d rows, want 0", tx.wrote)
			}
		})
	}
}

func TestWriteRepairsAStoredRowThatFailsTheChecks(t *testing.T) {
	t.Parallel()

	tx := newFake("stk-1", true, storedRow("stk-1", "acme", `{"executor":"shell"}`, 1))
	s := settingsOf(testUnit(t))

	got, e, err := settingsstore.Write(t.Context(), tx, &s, 1, entry())
	if err != nil || e == nil || got.Version != 2 || tx.wrote != 1 {
		t.Fatalf("Write = %+v, %v, %v with %d writes, want version 2 and one write", got, e, err, tx.wrote)
	}
}

func TestReadFailsClosedOnAStoredRowThatFailsTheChecks(t *testing.T) {
	t.Parallel()

	for name, row := range map[string]*settingsstore.Row{
		"body that fails the checks": storedRow("stk-1", "acme", `{"executor":"shell"}`, 1),
		"row of another unit":        storedRow("stk-2", "acme", `{}`, 1),
		"row of another namespace":   storedRow("stk-1", "beta", `{}`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tx := newFake("stk-1", true, row)

			if _, err := settingsstore.Read(t.Context(), tx, testUnit(t)); err == nil {
				t.Fatal("Read gave a value for a row that fails the checks")
			}
		})
	}
}

func TestRemoveNeedsTheVersionThatWasRead(t *testing.T) {
	t.Parallel()

	tx := newFake("stk-1", true, nil)

	_, err := settingsstore.Remove(t.Context(), tx, testUnit(t), 0, entry())
	if !errors.Is(err, unitsettings.ErrInvalid) {
		t.Fatalf("Remove at version 0 = %v, want ErrInvalid", err)
	}

	if tx.deleted != 0 {
		t.Fatalf("Remove deleted %d rows, want 0", tx.deleted)
	}
}

func TestWriteDefaultsRefusesAPoolThatIsNotAPoolID(t *testing.T) {
	t.Parallel()

	tx := newFake("", false, nil)
	d := unitsettings.NamespaceDefaults{Namespace: "acme", Pool: "x; y", Version: 0, UpdatedAt: time.Time{}}

	_, _, err := settingsstore.WriteDefaults(t.Context(), tx, d, 0, entry())
	if !errors.Is(err, unitsettings.ErrInvalid) {
		t.Fatalf("WriteDefaults = %v, want ErrInvalid", err)
	}

	if tx.wrote != 0 {
		t.Fatalf("WriteDefaults stored %d rows, want 0", tx.wrote)
	}
}
