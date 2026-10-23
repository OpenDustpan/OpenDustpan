package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/outputshare"
	"opendustpan.org/opendustpan/pkg/outputshare/outputsharetest"
	"opendustpan.org/opendustpan/pkg/store/postgres"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// outputShareHarness returns the factory of the output sharing suite over
// fresh schemas of the database at base. The store serves the setting and no
// rule yet.
func outputShareHarness(base string) outputsharetest.Factory {
	return func(t *testing.T) outputsharetest.Harness {
		t.Helper()

		schemaURL := isolatedURL(t, base)
		s := openURL(t, schemaURL)

		return outputsharetest.Harness{
			Store: nil, Checker: nil, Attachments: nil, States: nil,
			Settings: s.OutputSharingSettings(),
			Namespace: func(t *testing.T, name string) namespace.Fence {
				t.Helper()

				return ensureNamespace(t, s, namespace.Name(name))
			},
			Unit: nil, UnitFault: nil, Authority: nil, Profile: nil, Attach: nil, SaveState: nil, Grant: nil,
			Job: nil, Disable: nil,
			Audited: func(t *testing.T, ns string) []string {
				t.Helper()

				return auditActionsOf(t, s, namespace.Name(ns))
			},
			Calls: nil, StoreRaw: nil,
			SettingRaw: func(t *testing.T, ns namespace.Name, value string) {
				t.Helper()

				plantSettingRow(t, schemaURL, ns, value)
			},
		}
	}
}

// auditActionsOf returns the action of each audit entry of ns, oldest first.
func auditActionsOf(t *testing.T, s *postgres.Store, ns namespace.Name) []string {
	t.Helper()

	entries := allAuditEntries(t, s, ns)
	actions := make([]string, 0, len(entries))

	for i := range entries {
		actions = append(actions, entries[i].Action)
	}

	return actions
}

// rawSettingConn connects to the schema that schemaURL names with direct SQL.
// The caller closes the connection with closeSettingConn.
func rawSettingConn(t *testing.T, schemaURL string) *pgx.Conn {
	t.Helper()

	conn, err := pgx.Connect(t.Context(), schemaURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	return conn
}

// closeSettingConn closes conn and reports an error of the close.
func closeSettingConn(t *testing.T, conn *pgx.Conn) {
	t.Helper()

	if err := conn.Close(t.Context()); err != nil {
		t.Errorf("close: %v", err)
	}
}

// storeSettingRow writes value as the output sharing setting of ns with direct
// SQL, with the CHECK of the column in place, and returns the error of the
// write.
func storeSettingRow(t *testing.T, schemaURL string, ns namespace.Name, value string) error {
	t.Helper()

	conn := rawSettingConn(t, schemaURL)
	defer closeSettingConn(t, conn)

	_, err := conn.Exec(t.Context(),
		`UPDATE namespaces SET output_sharing_default = $1 WHERE name = $2`, value, ns.String())
	if err != nil {
		return fmt.Errorf("update the output sharing setting: %w", err)
	}

	return nil
}

// plantSettingRow drops the CHECK of the column in the schema that schemaURL
// names and writes value as the output sharing setting of ns, as a row of
// another version could hold such a value.
func plantSettingRow(t *testing.T, schemaURL string, ns namespace.Name, value string) {
	t.Helper()

	conn := rawSettingConn(t, schemaURL)
	defer closeSettingConn(t, conn)

	const drop = `ALTER TABLE namespaces DROP CONSTRAINT IF EXISTS namespaces_output_sharing_default_check`
	if _, err := conn.Exec(t.Context(), drop); err != nil {
		t.Fatalf("drop the check of the output sharing setting: %v", err)
	}

	_, err := conn.Exec(t.Context(),
		`UPDATE namespaces SET output_sharing_default = $1 WHERE name = $2`, value, ns.String())
	if err != nil {
		t.Fatalf("plant the output sharing setting %q: %v", value, err)
	}
}

func TestOutputShareConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	outputsharetest.Run(t, outputShareHarness(base))
}

// TestOutputSharingDefaultColumnRefusesAnotherValue proves the layer of the
// table alone: the CHECK of the column refuses a value that is not a value of
// the setting, also when the store code is skipped.
func TestOutputSharingDefaultColumnRefusesAnotherValue(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL test", envURL)
	}

	schemaURL := isolatedURL(t, base)
	s := openURL(t, schemaURL)
	ensureNamespace(t, s, "acme")

	for _, bad := range []string{"", "everyone", "Nobody", "namespace "} {
		err := storeSettingRow(t, schemaURL, "acme", bad)
		if err == nil || !strings.Contains(err.Error(), "namespaces_output_sharing_default_check") {
			t.Fatalf("store the setting %q = %v, want the refusal of the CHECK of the column", bad, err)
		}
	}

	for _, good := range []string{"namespace", "nobody"} {
		if err := storeSettingRow(t, schemaURL, "acme", good); err != nil {
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
		if err := postgres.CheckSettingFence("acme", bad); !errors.Is(err, namespace.ErrFenced) {
			t.Fatalf("CheckSettingFence(acme, %+v) = %v, want namespace.ErrFenced", bad, err)
		}
	}

	if err := postgres.CheckSettingFence("acme", namespace.Fence{Namespace: "acme", FailoverVersion: 1}); err != nil {
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

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL test", envURL)
	}

	s := openURL(t, isolatedURL(t, base))
	acme := ensureNamespace(t, s, "acme")
	globex := ensureNamespace(t, s, "globex")
	ctx := t.Context()

	beforeAcme, beforeGlobex := len(allAuditEntries(t, s, "acme")), len(allAuditEntries(t, s, "globex"))

	err := postgres.WriteSettingInTx(ctx, s, postgres.SettingWrite{
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

	own := postgres.SettingWrite{
		Namespace: "acme", Value: outputshare.DefaultNamespace, Fence: acme,
		Entry: outputsharetest.EntryForSetting(acme, outputshare.DefaultNamespace),
	}

	if err := postgres.WriteSettingInTx(ctx, s, own); err != nil {
		t.Fatalf("write under the fence of acme: %v", err)
	}
}

// TestSettingAuditOldValueIsReadInTheTransaction proves that the store sets
// the Old value of the change to the value that the write replaced, whatever
// Old the caller gave, and that it does not change the entry of the caller.
func TestSettingAuditOldValueIsReadInTheTransaction(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL test", envURL)
	}

	s := openURL(t, isolatedURL(t, base))
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

// TestSettingPreCheckRefusesAForeignFenceAlone proves the pre-check call of
// SetSetting alone. A canceled context makes every transaction fail, so only
// the pre-check can answer namespace.ErrFenced for a fence of another
// namespace.
func TestSettingPreCheckRefusesAForeignFenceAlone(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL test", envURL)
	}

	s := openURL(t, isolatedURL(t, base))
	globex := ensureNamespace(t, s, "globex")

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := s.OutputSharingSettings().SetSetting(ctx, "acme", outputshare.DefaultNamespace, globex,
		outputsharetest.EntryForSetting(globex, outputshare.DefaultNamespace))
	if !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("SetSetting under the fence of globex with a canceled context = %v, want namespace.ErrFenced", err)
	}
}

// TestSettingRepairsAnInvalidStoredValue proves that a valid change replaces
// a stored value that fails Validate, that the audit change records the raw
// value marked invalid, and that a read of the invalid value still fails
// closed.
func TestSettingRepairsAnInvalidStoredValue(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL test", envURL)
	}

	schemaURL := isolatedURL(t, base)
	s := openURL(t, schemaURL)
	fence := ensureNamespace(t, s, "acme")
	ctx := t.Context()
	settings := s.OutputSharingSettings()

	plantSettingRow(t, schemaURL, "acme", "every one")

	if got, err := settings.Setting(ctx, "acme"); !errors.Is(err, outputshare.ErrInvalid) || got != "" {
		t.Fatalf("Setting of the invalid value = %q, %v, want ErrInvalid and no value", got, err)
	}

	err := settings.SetSetting(ctx, "acme", outputshare.DefaultNamespace, fence,
		outputsharetest.EntryForSetting(fence, outputshare.DefaultNamespace))
	if err != nil {
		t.Fatalf("SetSetting over the invalid value: %v", err)
	}

	if got, err := settings.Setting(ctx, "acme"); err != nil || got != outputshare.DefaultNamespace {
		t.Fatalf("Setting after the repair = %q, %v, want namespace", got, err)
	}

	got := settingOld(allAuditEntries(t, s, "acme"))
	if want := []string{"invalid:\"every one\""}; !slices.Equal(got, want) {
		t.Fatalf("Old values of the entries = %q, want %q", got, want)
	}
}

// TestOutputSharingSettingSurvivesABackupRestore proves that a restore brings
// back the value namespace of the setting, and not the value that the
// namespace holds at the restore.
func TestOutputSharingSettingSurvivesABackupRestore(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL test", envURL)
	}

	st := openURL(t, isolatedURL(t, base))
	ps := st

	ctx := t.Context()
	fence := ensureNamespace(t, st, "acme")
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
