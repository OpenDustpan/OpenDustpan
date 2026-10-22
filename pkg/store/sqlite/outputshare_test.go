package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/outputshare"
	"opendustpan.org/opendustpan/pkg/outputshare/outputsharetest"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// openOutputShare returns a fresh store and the path of its database.
func openOutputShare(t *testing.T) (*sqlite.Store, string) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "dustpan.db")

	copyTemplate(t, path)

	s, err := sqlite.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := s.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	return s, path
}

// outputShareHarness returns the harness of the output sharing suite over a
// fresh store. The store serves the setting and no rule yet.
func outputShareHarness(t *testing.T) outputsharetest.Harness {
	t.Helper()

	s, path := openOutputShare(t)

	return outputsharetest.Harness{
		Store: nil, Checker: nil, Attachments: nil, States: nil,
		Settings: s.OutputSharingSettings(),
		Namespace: func(t *testing.T, name string) namespace.Fence {
			t.Helper()

			return ensureNamespace(t, s, namespace.Name(name))
		},
		Unit: nil, UnitFault: nil, Authority: nil, Profile: nil, Attach: nil, SaveState: nil, Grant: nil, Job: nil,
		Disable: nil,
		Audited: func(t *testing.T, ns string) []string {
			t.Helper()

			entries := allAuditEntries(t, s, namespace.Name(ns))
			actions := make([]string, 0, len(entries))

			for i := range entries {
				actions = append(actions, entries[i].Action)
			}

			return actions
		},
		Calls: nil, StoreRaw: nil,
		SettingRaw: func(t *testing.T, ns namespace.Name, value string) {
			t.Helper()

			plantSettingRow(t, path, ns, value)
		},
	}
}

// rawSettingDB opens the database at path with direct SQL, on one
// connection, because a pragma holds for one connection only.
func rawSettingDB(t *testing.T, path string) *sql.DB {
	t.Helper()

	conn, err := openRawDB(path)
	if err != nil {
		t.Fatalf("open raw database: %v", err)
	}

	conn.SetMaxOpenConns(1)

	t.Cleanup(func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Errorf("close raw database: %v", closeErr)
		}
	})

	return conn
}

// storeSettingRow writes value as the output sharing setting of ns with direct
// SQL, with the CHECK of the column on, and returns the error of the write.
func storeSettingRow(t *testing.T, path string, ns namespace.Name, value string) error {
	t.Helper()

	_, err := rawSettingDB(t, path).ExecContext(t.Context(),
		`UPDATE namespaces SET output_sharing_default = ? WHERE name = ?`, value, ns.String())
	if err != nil {
		return fmt.Errorf("update the output sharing setting: %w", err)
	}

	return nil
}

// plantSettingRow writes value as the output sharing setting of ns with direct
// SQL and the CHECK constraints off, as a row of another version could hold
// such a value.
func plantSettingRow(t *testing.T, path string, ns namespace.Name, value string) {
	t.Helper()

	conn := rawSettingDB(t, path)

	if _, err := conn.ExecContext(t.Context(), `PRAGMA ignore_check_constraints = ON`); err != nil {
		t.Fatalf("ignore check constraints: %v", err)
	}

	_, err := conn.ExecContext(t.Context(),
		`UPDATE namespaces SET output_sharing_default = ? WHERE name = ?`, value, ns.String())
	if err != nil {
		t.Fatalf("plant the output sharing setting %q: %v", value, err)
	}
}

func TestOutputShareConformance(t *testing.T) {
	t.Parallel()

	outputsharetest.Run(t, outputShareHarness)
}

// TestOutputSharingDefaultColumnRefusesAnotherValue proves the layer of the
// table alone: the CHECK of the column refuses a value that is not a value of
// the setting, also when the store code is skipped.
func TestOutputSharingDefaultColumnRefusesAnotherValue(t *testing.T) {
	t.Parallel()

	s, path := openOutputShare(t)
	ensureNamespace(t, s, "acme")

	for _, bad := range []string{"", "everyone", "Nobody", "namespace "} {
		err := storeSettingRow(t, path, "acme", bad)
		if err == nil || !strings.Contains(err.Error(), "CHECK") {
			t.Fatalf("store the setting %q = %v, want the refusal of the CHECK of the column", bad, err)
		}
	}

	for _, good := range []string{"namespace", "nobody"} {
		if err := storeSettingRow(t, path, "acme", good); err != nil {
			t.Fatalf("store the setting %q: %v", good, err)
		}
	}
}

// settingOld returns the Old value of the one change of each entry of ns, or
// "-" for an entry with no Old value, oldest first.
func settingOld(entries []audit.Entry) []string {
	out := make([]string, 0, len(entries))

	for i := range entries {
		old := "-"
		if len(entries[i].Changes) == 1 && entries[i].Changes[0].Old != nil {
			old = *entries[i].Changes[0].Old
		}

		out = append(out, old)
	}

	return out
}

// TestSettingFenceCheckAlone proves the comparison that SetSetting makes
// before the transaction, with no store behind it: a fence of another
// namespace is refused, and a fence of the namespace passes.
func TestSettingFenceCheckAlone(t *testing.T) {
	t.Parallel()

	for _, bad := range []namespace.Fence{
		{Namespace: "globex", FailoverVersion: 1},
		{Namespace: "", FailoverVersion: 1},
		{Namespace: "acme ", FailoverVersion: 1},
		{Namespace: "ACME", FailoverVersion: 1},
	} {
		if err := sqlite.CheckSettingFence("acme", bad); !errors.Is(err, namespace.ErrFenced) {
			t.Fatalf("CheckSettingFence(acme, %+v) = %v, want namespace.ErrFenced", bad, err)
		}
	}

	if err := sqlite.CheckSettingFence("acme", namespace.Fence{Namespace: "acme", FailoverVersion: 1}); err != nil {
		t.Fatalf("CheckSettingFence with the fence of the namespace: %v", err)
	}
}

// TestSettingTransactionRefusesAForeignFence proves the layer inside the
// transaction alone. It skips the pre-check of SetSetting and gives the
// setting of acme a valid fence and a valid entry of globex. The fence check
// of the transaction accepts both, so only the comparison inside the
// transaction stops the write.
func TestSettingTransactionRefusesAForeignFence(t *testing.T) {
	t.Parallel()

	s, _ := openOutputShare(t)
	acme := ensureNamespace(t, s, "acme")
	globex := ensureNamespace(t, s, "globex")
	ctx := t.Context()

	beforeAcme, beforeGlobex := len(allAuditEntries(t, s, "acme")), len(allAuditEntries(t, s, "globex"))

	err := sqlite.WriteSettingInTx(ctx, s, sqlite.SettingWrite{
		Namespace: "acme", Value: outputshare.DefaultNamespace, Fence: globex,
		Entry: outputsharetest.EntryForSetting(globex, outputshare.DefaultNamespace),
	})
	if !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("write under the fence of globex = %v, want namespace.ErrFenced", err)
	}

	for _, ns := range []namespace.Name{"acme", "globex"} {
		got, getErr := s.OutputSharingSettings().Setting(ctx, ns)
		if getErr != nil || got != outputshare.DefaultNobody {
			t.Fatalf("Setting(%s) = %q, %v, want nobody and no error", ns, got, getErr)
		}
	}

	if n := len(allAuditEntries(t, s, "acme")); n != beforeAcme {
		t.Fatalf("the refused write added %d entries in acme", n-beforeAcme)
	}

	if n := len(allAuditEntries(t, s, "globex")); n != beforeGlobex {
		t.Fatalf("the refused write added %d entries in globex", n-beforeGlobex)
	}

	own := sqlite.SettingWrite{
		Namespace: "acme", Value: outputshare.DefaultNamespace, Fence: acme,
		Entry: outputsharetest.EntryForSetting(acme, outputshare.DefaultNamespace),
	}

	if err := sqlite.WriteSettingInTx(ctx, s, own); err != nil {
		t.Fatalf("write under the fence of acme: %v", err)
	}
}

// TestSettingAuditOldValueIsReadInTheTransaction proves that the store sets
// the Old value of the change to the value that the write replaced, whatever
// Old the caller gave, and that it does not change the entry of the caller.
func TestSettingAuditOldValueIsReadInTheTransaction(t *testing.T) {
	t.Parallel()

	s, _ := openOutputShare(t)
	fence := ensureNamespace(t, s, "acme")
	ctx := t.Context()
	lie := "namespace"

	first := outputsharetest.EntryForSetting(fence, outputshare.DefaultNamespace)
	first.Changes[0].Old = &lie

	settings := s.OutputSharingSettings()

	err := settings.SetSetting(ctx, "acme", outputshare.DefaultNamespace, fence, first)
	if err != nil {
		t.Fatalf("first SetSetting: %v", err)
	}

	if first.Changes[0].Old != &lie || lie != "namespace" {
		t.Fatal("SetSetting changed the changes of the entry of the caller")
	}

	second := outputsharetest.EntryForSetting(fence, outputshare.DefaultNobody)

	err = settings.SetSetting(ctx, "acme", outputshare.DefaultNobody, fence, second)
	if err != nil {
		t.Fatalf("second SetSetting: %v", err)
	}

	if second.Changes[0].Old != nil {
		t.Fatal("SetSetting changed the changes of the entry of the caller")
	}

	got := settingOld(allAuditEntries(t, s, "acme"))
	if want := []string{"nobody", "namespace"}; !slices.Equal(got, want) {
		t.Fatalf("Old values of the entries = %q, want %q", got, want)
	}
}

// TestOutputSharingSettingSurvivesABackupRestore proves that a restore brings
// back the value namespace of the setting, and not the value that the
// namespace holds at the restore.
func TestOutputSharingSettingSurvivesABackupRestore(t *testing.T) {
	t.Parallel()

	st, ok := openBackup(t).(*backupStore)
	if !ok {
		t.Fatal("the backup store is not a *backupStore")
	}

	ps, ok := storetest.BackupStore(st).(storetest.PortableRestoreStore)
	if !ok {
		t.Fatal("the SQLite store does not export and stage the portable format")
	}

	ctx := t.Context()
	fence := ensureNamespace(t, st.Store, "acme")
	settings := st.OutputSharingSettings()

	err := settings.SetSetting(
		ctx,
		"acme",
		outputshare.DefaultNamespace,
		fence,
		outputsharetest.EntryForSetting(fence, outputshare.DefaultNamespace),
	)
	if err != nil {
		t.Fatalf("set namespace: %v", err)
	}

	files, sum, descs := storetest.PortableFiles(t, ps)

	err = settings.SetSetting(
		ctx,
		"acme",
		outputshare.DefaultNobody,
		fence,
		outputsharetest.EntryForSetting(fence, outputshare.DefaultNobody),
	)
	if err != nil {
		t.Fatalf("set nobody: %v", err)
	}

	_, err = ps.StagePortable(ctx, storetest.PortableOpener(files), sum, descs, storetest.StageAs(t, ps))
	if err != nil {
		t.Fatalf("stage the portable backup: %v", err)
	}

	t.Cleanup(func() {
		if discardErr := ps.DiscardStaging(context.WithoutCancel(ctx)); discardErr != nil {
			t.Errorf("discard staging: %v", discardErr)
		}
	})

	err = ps.ApplyStaging(ctx)
	if err != nil {
		t.Fatalf("apply the staged backup: %v", err)
	}

	got, err := settings.Setting(ctx, "acme")
	if err != nil || got != outputshare.DefaultNamespace {
		t.Fatalf("Setting after the restore = %q, %v, want namespace", got, err)
	}
}
