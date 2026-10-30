package storetest

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/unitimport"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
)

// ImportPorts are the ports of the import tool on one store. The Tool port
// writes the ledger rows and the targets in its own transactions, so a
// store gives the two seams for them.
type ImportPorts struct {
	Store      unitimport.Store
	Units      unitimport.UnitStore
	Uploads    unitimport.UploadStore
	SeedLedger func(ctx context.Context, ns namespace.Name, row unitimport.LedgerRow) error
	SetTarget  func(ctx context.Context, ns namespace.Name, key unitimport.UnitKey, target history.Unit) error
}

// ImportSeeder is an optional BackupStore capability: a store that keeps
// imports gives its ports, so the round trip fills and compares the four
// import tables. A store with no import port skips them: the fake of this
// package's own tests is one.
type ImportSeeder interface {
	ImportPorts() ImportPorts
}

// importTables returns the tables of the import tool in backup.Tables.
func importTables() []string {
	return []string{"imports", "imports_purged", "import_units", "import_ledger", "import_upload_commits"}
}

// seededImports names what seedBackupImports wrote.
type seededImports struct {
	open  string // An import in StatePlanning: ended_at_ms is 0.
	ended string // An import in StateCanceled: ended_at_ms is not 0.
	gone  string // A purged import: its row is in imports_purged.
}

// seedBackupImports writes three imports, so the round trip carries both
// forms of the end time, a unit with a target, ledger rows, and the ID of
// a purged import. It returns nil for a store with no import port.
func seedBackupImports(t *testing.T, s BackupStore, acme namespace.Namespace) *seededImports {
	t.Helper()

	seeder, ok := s.(ImportSeeder)
	if !ok {
		return nil
	}

	ports := seeder.ImportPorts()
	ctx := t.Context()
	fence := acme.Fence()
	e := lifecycleEntry(acme.Name, "import:run")
	ids := &seededImports{open: "imp-open", ended: "imp-ended", gone: "imp-gone"}

	for _, id := range []string{ids.open, ids.ended, ids.gone} {
		seedOneImport(t, ports, acme, id)
	}

	for _, id := range []string{ids.ended, ids.gone} {
		m := unitimport.Move{From: unitimport.StatePlanning, To: unitimport.StateCanceled, Reason: "stop"}
		if err := ports.Store.Transition(ctx, fence, id, m, e); err != nil {
			t.Fatalf("cancel import %s: %v", id, err)
		}
	}

	if err := ports.Store.Purge(ctx, fence, ids.gone, e); err != nil {
		t.Fatalf("purge import %s: %v", ids.gone, err)
	}

	return ids
}

// seedOneImport writes the import id with one unit, its target, its counts
// and two ledger rows.
func seedOneImport(t *testing.T, ports ImportPorts, acme namespace.Namespace, id string) {
	t.Helper()

	ctx := t.Context()
	fence := acme.Fence()
	e := lifecycleEntry(acme.Name, "import:run")

	if _, err := ports.Store.Create(ctx, fence, backupImport(acme.Name, id), e); err != nil {
		t.Fatalf("create import %s: %v", id, err)
	}

	unit := backupImportUnit("u-" + id)
	if err := ports.Units.PutUnits(ctx, fence, id, []unitimport.ImportUnit{unit}, e); err != nil {
		t.Fatalf("put units of %s: %v", id, err)
	}

	key := unitimport.UnitKey{ImportID: id, UnitID: unit.ID}
	target := history.Unit{Namespace: acme.Name, Tool: "opentofu", ID: "ws-" + id}

	if err := ports.SetTarget(ctx, acme.Name, key, target); err != nil {
		t.Fatalf("set target of %s: %v", id, err)
	}

	if err := ports.Units.SetCounts(ctx, fence, key, unitimport.Counts{Versions: 2, Committed: 2}, e); err != nil {
		t.Fatalf("set counts of %s: %v", id, err)
	}

	commit := unitimport.UploadCommit{
		ImportID: id, UnitDir: "0123456789abcdef0123456789abcdef", Sequence: 1,
		Facts: "0000000001-a1b2c3.json", CommittedAt: now(),
	}
	if err := ports.Uploads.CommitUploadVersion(ctx, fence, commit); err != nil {
		t.Fatalf("commit upload of %s: %v", id, err)
	}

	for n, version := range []string{"v1", "v2"} {
		row := backupLedgerRow(acme.Name, id, unit.ID, version, now().Add(time.Duration(n)*time.Second))
		if err := ports.SeedLedger(ctx, acme.Name, row); err != nil {
			t.Fatalf("seed ledger of %s: %v", id, err)
		}
	}
}

func backupImport(ns namespace.Name, id string) unitimport.Import {
	return unitimport.Import{
		ID: id, Namespace: ns, Kind: unitimport.SourceS3, Address: "s3://bucket/prefix",
		Selection:  unitimport.Selection{Names: []string{"web", "db"}},
		Credential: unitimport.CredentialProfile, Profile: "aws-read", ProfilePath: "login.s3",
		Options: unitimport.Options{
			HistoryLimit: 20, Since: now().Add(-24 * time.Hour), LockSource: true,
		},
		State: unitimport.StatePlanning, Reason: "", CreatedBy: "alice@example.com", CreatedAt: now(),
		Plan: unitimport.Plan{
			At: time.Time{}, Units: 0, Versions: 0, Bytes: 0, Capabilities: nil, Findings: nil,
			Requests: nil, Reservations: nil,
		},
		EndedAt: time.Time{},
	}
}

func backupImportUnit(id string) unitimport.ImportUnit {
	return unitimport.ImportUnit{
		ID: id, ImportID: "", Source: unitimport.SourceUnit{Tool: "opentofu", Name: "acme/" + id},
		TargetName: "t-" + id, Target: history.Unit{Namespace: "", Tool: "", ID: ""},
		State: unitimport.UnitPlanned, ErrorCode: "", Versions: 3, Committed: 0, Bytes: 1024, Encryption: "client",
		Findings: []unitimport.Finding{{
			Code: "secrets.unsupported", Severity: unitimport.SeverityWarning,
			Message: "The source holds a secret that the import cannot read.", Help: "Set the secret again.",
		}},
	}
}

func backupLedgerRow(ns namespace.Name, importID, unitID, version string, at time.Time) unitimport.LedgerRow {
	return unitimport.LedgerRow{
		ImportID: importID, ImportUnitID: unitID, Version: unitimport.VersionID(version),
		SourceSHA256: "src-" + version, StoredSHA256: "dst-" + version,
		Item: history.ItemRef{
			Unit: history.Unit{Namespace: ns, Tool: "opentofu", ID: "ws-" + importID},
			Kind: secret.ItemKindState, Item: "item-" + version,
		},
		CommittedAt: at,
	}
}

// checkImportRowCounts checks that sum holds rows of each import table,
// when the store keeps imports. The counts are one row for the purged ID,
// three imports minus the purged one, and the units and ledger rows of
// the three imports (the purge deletes the units of the purged import
// only).
func checkImportRowCounts(t *testing.T, s BackupStore, sum backup.Summary) {
	t.Helper()

	if _, ok := s.(ImportSeeder); !ok {
		return
	}

	want := map[string]int64{
		"imports":               2,
		"imports_purged":        1,
		"import_units":          2,
		"import_ledger":         6,
		"import_upload_commits": 2,
	}
	for _, table := range importTables() {
		if got := sum.RowCounts[table]; got != want[table] {
			t.Errorf("exported rows of %q = %d, want %d", table, got, want[table])
		}
	}
}

// checkRestoredImports checks that dst holds the three seeded imports with
// their units, targets and ledger rows, after a restore.
func checkRestoredImports(t *testing.T, dst RestoreStore, acme namespace.Namespace, ids *seededImports) {
	t.Helper()

	seeder, ok := dst.(ImportSeeder)
	if !ok || ids == nil {
		return
	}

	ports := seeder.ImportPorts()
	checkRestoredImportRecords(t, ports, acme.Name, ids)

	checkRestoredImportUnits(t, ports, acme.Name, ids)
	checkRestoredUploadCommits(t, ports, acme.Name, ids)

	// The ledger of each import, the purged one too, stays.
	for _, id := range []string{ids.open, ids.ended, ids.gone} {
		rows, err := ports.Units.Ledger(t.Context(), acme.Name, id, "u-"+id)
		if err != nil || len(rows) != 2 || rows[0].Version != "v1" || rows[1].Version != "v2" {
			t.Fatalf("restored ledger of %s = %+v, %v, want v1 and v2", id, rows, err)
		}
	}
}

// checkRestoredImportRecords checks the import records and the purged ID.
func checkRestoredImportRecords(t *testing.T, ports ImportPorts, ns namespace.Name, ids *seededImports) {
	t.Helper()

	ctx := t.Context()

	open, err := ports.Store.Get(ctx, ns, ids.open)
	if err != nil || open.State != unitimport.StatePlanning || !open.EndedAt.IsZero() ||
		open.ProfilePath != "login.s3" || !slices.Equal(open.Selection.Names, []string{"web", "db"}) {
		t.Fatalf("restored open import = %+v, %v, want a planning import with no end time", open, err)
	}

	ended, err := ports.Store.Get(ctx, ns, ids.ended)
	if err != nil || ended.State != unitimport.StateCanceled || ended.EndedAt.IsZero() || ended.Reason != "stop" {
		t.Fatalf("restored ended import = %+v, %v, want a canceled import with an end time", ended, err)
	}

	if _, err = ports.Store.Get(ctx, ns, ids.gone); !errors.Is(err, unitimport.ErrPurged) {
		t.Fatalf("restored purged import: error = %v, want ErrPurged", err)
	}
}

// checkRestoredUploadCommits checks that the commit row of an upload version
// survived the restore.
func checkRestoredUploadCommits(t *testing.T, ports ImportPorts, ns namespace.Name, ids *seededImports) {
	t.Helper()

	for _, id := range []string{ids.open, ids.ended} {
		rows, err := ports.Uploads.UploadCommits(t.Context(), ns, id, "0123456789abcdef0123456789abcdef")
		if err != nil || len(rows) != 1 || rows[0].Sequence != 1 || rows[0].Facts != "0000000001-a1b2c3.json" ||
			!rows[0].CommittedAt.Equal(now()) {
			t.Fatalf("restored upload commits of %s = %+v, %v, want the one seeded row", id, rows, err)
		}
	}
}

// checkRestoredImportUnits checks the units, with their targets and counts.
func checkRestoredImportUnits(t *testing.T, ports ImportPorts, ns namespace.Name, ids *seededImports) {
	t.Helper()

	for _, id := range []string{ids.open, ids.ended} {
		unit, err := ports.Units.Unit(t.Context(), ns, id, "u-"+id)
		wantTarget := history.Unit{Namespace: ns, Tool: "opentofu", ID: "ws-" + id}

		if err != nil || unit.Target != wantTarget || unit.Committed != 2 || unit.Versions != 2 ||
			len(unit.Findings) != 1 {
			t.Fatalf("restored unit of %s = %+v, %v, want its target, counts and finding", id, unit, err)
		}
	}
}
