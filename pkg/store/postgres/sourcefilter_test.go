package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/source"
	"opendustpan.org/opendustpan/pkg/source/sourcetest"
	"opendustpan.org/opendustpan/pkg/store/postgres"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// sourceFilterHarness returns the factory of the source filter suite over
// fresh schemas of the database at base.
func sourceFilterHarness(base string) sourcetest.Factory {
	return func(t *testing.T) sourcetest.Harness {
		t.Helper()

		schemaURL := isolatedURL(t, base)
		s := openURL(t, schemaURL)

		return sourcetest.Harness{
			Filters: s.SourceFilters(),
			Namespace: func(t *testing.T, name string) namespace.Fence {
				t.Helper()

				return ensureNamespace(t, s, namespace.Name(name))
			},
			Audited: func(t *testing.T, ns string) []audit.Entry {
				t.Helper()

				return allAuditEntries(t, s, namespace.Name(ns))
			},
			FilterRaw: func(t *testing.T, ns namespace.Name, raw string) {
				t.Helper()

				plantSourceFilter(t, schemaURL, ns, raw)
			},
		}
	}
}

// plantSourceFilter drops the CHECK of the column in the schema that schemaURL
// names and writes raw as the stored source filter of ns, as a row of another
// version could hold such a value.
func plantSourceFilter(t *testing.T, schemaURL string, ns namespace.Name, raw string) {
	t.Helper()

	conn := rawSettingConn(t, schemaURL)
	defer closeSettingConn(t, conn)

	const drop = `ALTER TABLE namespaces DROP CONSTRAINT IF EXISTS namespaces_source_filter_check`
	if _, err := conn.Exec(t.Context(), drop); err != nil {
		t.Fatalf("drop the check of the source filter: %v", err)
	}

	_, err := conn.Exec(t.Context(), `UPDATE namespaces SET source_filter = $1 WHERE name = $2`, raw, ns.String())
	if err != nil {
		t.Fatalf("plant the source filter %q: %v", raw, err)
	}
}

// sourceFilterBase returns the URL of the test database, or skips the test.
func sourceFilterBase(t *testing.T) string {
	t.Helper()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL test", envURL)
	}

	return base
}

// emptyFilter returns the empty filter.
func emptyFilter() source.Filter {
	return source.Filter{Submodules: nil, LargeFiles: nil, FailOnDenied: false, ProtectedBranches: nil}
}

func TestSourceFilterConformance(t *testing.T) {
	t.Parallel()

	sourcetest.Run(t, sourceFilterHarness(sourceFilterBase(t)))
}

// TestSourceFilterColumnRefusesAnotherValue proves the layer of the table
// alone: the CHECK of the column refuses a text that is not JSON and a text
// above 1 MiB, also when the store code is skipped.
func TestSourceFilterColumnRefusesAnotherValue(t *testing.T) {
	t.Parallel()

	schemaURL := isolatedURL(t, sourceFilterBase(t))
	s := openURL(t, schemaURL)
	ensureNamespace(t, s, "acme")

	conn := rawSettingConn(t, schemaURL)
	defer closeSettingConn(t, conn)

	write := func(value string) error {
		_, err := conn.Exec(t.Context(), `UPDATE namespaces SET source_filter = $1 WHERE name = $2`, value, "acme")
		if err != nil {
			return fmt.Errorf("update the source filter: %w", err)
		}

		return nil
	}

	for name, bad := range map[string]string{
		"not json":  "not json",
		"truncated": `{"submodules":[`,
		"too large": `{"protectedBranches":["` + strings.Repeat("a", source.MaxFilterJSON) + `"]}`,
	} {
		if err := write(bad); err == nil || !strings.Contains(err.Error(), "namespaces_source_filter_check") {
			t.Fatalf("store the source filter %q = %v, want the refusal of the CHECK of the column", name, err)
		}
	}

	for _, good := range []string{"", "{}", `{"submodules":[]}`} {
		if err := write(good); err != nil {
			t.Fatalf("store the source filter %q: %v", good, err)
		}
	}
}

// TestSourceFilterPreCheckRefusesAForeignFenceAlone proves the pre-check call
// of SetFilter alone. A canceled context makes every transaction fail, so only
// the pre-check can answer namespace.ErrFenced for a fence of another
// namespace.
func TestSourceFilterPreCheckRefusesAForeignFenceAlone(t *testing.T) {
	t.Parallel()

	s := openURL(t, isolatedURL(t, sourceFilterBase(t)))
	globex := ensureNamespace(t, s, "globex")

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := s.SourceFilters().SetFilter(ctx, "acme", emptyFilter(), globex, sourcetest.EntryForFilter(globex, "{}"))
	if !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("SetFilter under the fence of globex with a canceled context = %v, want namespace.ErrFenced", err)
	}
}

// TestSourceFilterTransactionRefusesAForeignFence proves the layer inside the
// transaction alone. It skips the pre-check of SetFilter and gives the filter
// of acme a valid fence and a valid entry of globex. The fence check of the
// transaction accepts both, so only the comparison inside the transaction
// stops the write.
func TestSourceFilterTransactionRefusesAForeignFence(t *testing.T) {
	t.Parallel()

	s := openURL(t, isolatedURL(t, sourceFilterBase(t)))
	ensureNamespace(t, s, "acme")
	globex := ensureNamespace(t, s, "globex")

	beforeAcme, beforeGlobex := len(allAuditEntries(t, s, "acme")), len(allAuditEntries(t, s, "globex"))

	err := postgres.WriteSourceFilterInTx(t.Context(), s, postgres.SourceFilterWrite{
		Namespace: "acme", Text: `{"failOnDenied":true}`, Fence: globex, Entry: sourcetest.EntryForFilter(globex, "{}"),
	})
	if !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("write under the fence of globex = %v, want namespace.ErrFenced", err)
	}

	for _, ns := range []namespace.Name{"acme", "globex"} {
		got, getErr := s.SourceFilters().Filter(t.Context(), ns)
		if getErr != nil || got.FailOnDenied {
			t.Fatalf("Filter(%s) = %+v, %v, want the empty filter", ns, got, getErr)
		}
	}

	if n := len(allAuditEntries(t, s, "acme")); n != beforeAcme {
		t.Fatalf("the refused write added %d entries in acme", n-beforeAcme)
	}

	if n := len(allAuditEntries(t, s, "globex")); n != beforeGlobex {
		t.Fatalf("the refused write added %d entries in globex", n-beforeGlobex)
	}
}

// TestSourceFilterAuditOldValueIsReadInTheTransaction proves that the store
// sets the Old value of the change to the value that the write replaced,
// whatever Old the caller gave, and that it does not change the entry of the
// caller.
func TestSourceFilterAuditOldValueIsReadInTheTransaction(t *testing.T) {
	t.Parallel()

	s := openURL(t, isolatedURL(t, sourceFilterBase(t)))
	fence := ensureNamespace(t, s, "acme")
	filters := s.SourceFilters()
	lie := "a value that was never stored"

	first := sourcetest.EntryForFilter(fence, `{"failOnDenied":true}`)
	first.Changes[0].Old = &lie

	denied := source.Filter{Submodules: nil, LargeFiles: nil, FailOnDenied: true, ProtectedBranches: nil}

	err := filters.SetFilter(t.Context(), "acme", denied, fence, first)
	if err != nil {
		t.Fatalf("first SetFilter: %v", err)
	}

	if first.Changes[0].Old != &lie || lie != "a value that was never stored" {
		t.Fatal("SetFilter changed the changes of the entry of the caller")
	}

	second := sourcetest.EntryForFilter(fence, "{}")

	err = filters.SetFilter(t.Context(), "acme", emptyFilter(), fence, second)
	if err != nil {
		t.Fatalf("second SetFilter: %v", err)
	}

	if second.Changes[0].Old != nil {
		t.Fatal("SetFilter changed the changes of the entry of the caller")
	}

	got := settingOld(allAuditEntries(t, s, "acme"))
	want := []string{
		`{"submodules":[],"largeFiles":[],"failOnDenied":false,"protectedBranches":[]}`,
		`{"submodules":[],"largeFiles":[],"failOnDenied":true,"protectedBranches":[]}`,
	}

	if !slices.Equal(got, want) {
		t.Fatalf("Old values of the entries = %q, want %q", got, want)
	}
}

// fullSourceFilter returns a filter that sets each field.
func fullSourceFilter() source.Filter {
	return source.Filter{
		Submodules:        []source.URLPattern{"https://git.example.com/platform/*"},
		LargeFiles:        []source.URLPattern{"https://git.example.com/acme/infra"},
		FailOnDenied:      true,
		ProtectedBranches: []string{"main", "release/1"},
	}
}

// setSourceFilter stores f as the filter of the namespace of fence, and fails
// the test on an error.
func setSourceFilter(t *testing.T, s *postgres.Store, fence namespace.Fence, f source.Filter) string {
	t.Helper()

	text, err := source.EncodeFilter(f)
	if err != nil {
		t.Fatalf("encode the filter: %v", err)
	}

	err = s.SourceFilters().SetFilter(t.Context(), fence.Namespace, f, fence, sourcetest.EntryForFilter(fence, text))
	if err != nil {
		t.Fatalf("SetFilter: %v", err)
	}

	return text
}

// TestSourceFilterExportsAndStages proves that a filter that is not empty
// reaches the portable export of the namespace row, with the text that the
// store keeps, and that the stage of the export accepts it.
func TestSourceFilterExportsAndStages(t *testing.T) {
	t.Parallel()

	s := openURL(t, isolatedURL(t, sourceFilterBase(t)))
	fence := ensureNamespace(t, s, "acme")
	text := setSourceFilter(t, s, fence, fullSourceFilter())

	rows := storetest.PortableNamespaceRows(t, s)

	got, ok := rows["acme"]["source_filter"].(string)
	if !ok || got != text {
		t.Fatalf("the exported source_filter of acme = %v, want %s", rows["acme"]["source_filter"], text)
	}
}

// TestSourceFilterStageRefusesAStoredNonFilter proves that the stage of an
// export that holds a stored value that is not a filter fails with ErrCorrupt,
// so a restore never brings such a value in.
func TestSourceFilterStageRefusesAStoredNonFilter(t *testing.T) {
	t.Parallel()

	base := sourceFilterBase(t)

	for _, raw := range []string{"not json", `{"extra":1}`, `{"protectedBranches":["a/*"]}`} {
		schemaURL := isolatedURL(t, base)
		s := openURL(t, schemaURL)
		ensureNamespace(t, s, "acme")
		plantSourceFilter(t, schemaURL, "acme", raw)

		err := storetest.StagePortableExport(t, s)
		if !errors.Is(err, backup.ErrCorrupt) {
			t.Fatalf("stage of an export with the stored value %q = %v, want ErrCorrupt", raw, err)
		}
	}
}

// TestSourceFilterFailedAuditAppendRollsBackTheWrite proves that a change whose
// audit entry cannot be appended leaves the filter and the audit log as they
// were. A trigger on the audit table refuses the append after the write of the
// filter, so only the transaction can undo the write.
func TestSourceFilterFailedAuditAppendRollsBackTheWrite(t *testing.T) {
	t.Parallel()

	schemaURL := isolatedURL(t, sourceFilterBase(t))
	s := openURL(t, schemaURL)
	fence := ensureNamespace(t, s, "acme")
	first := fullSourceFilter()
	setSourceFilter(t, s, fence, first)

	entries := len(allAuditEntries(t, s, "acme"))

	conn := rawSettingConn(t, schemaURL)
	defer closeSettingConn(t, conn)

	const trigger = `CREATE FUNCTION refuse_audit() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'audit append refused'; END $$;
CREATE TRIGGER refuse_audit BEFORE INSERT ON audit_entries FOR EACH ROW EXECUTE FUNCTION refuse_audit()`
	if _, err := conn.Exec(t.Context(), trigger); err != nil {
		t.Fatalf("plant the trigger: %v", err)
	}

	changed := emptyFilter()
	changed.FailOnDenied = true

	text, err := source.EncodeFilter(changed)
	if err != nil {
		t.Fatalf("encode the filter: %v", err)
	}

	err = s.SourceFilters().SetFilter(t.Context(), "acme", changed, fence, sourcetest.EntryForFilter(fence, text))
	if err == nil {
		t.Fatal("SetFilter succeeded although the audit append failed")
	}

	got, err := s.SourceFilters().Filter(t.Context(), "acme")
	if err != nil {
		t.Fatalf("Filter: %v", err)
	}

	wantText := encodedFilter(t, &first)

	gotText := encodedFilter(t, &got)
	if gotText != wantText {
		t.Fatalf("the filter after the failed change = %s, want %s", gotText, wantText)
	}

	if n := len(allAuditEntries(t, s, "acme")); n != entries {
		t.Fatalf("the failed change left %d audit entries, want %d", n, entries)
	}
}

// encodedFilter returns the text of f, and fails the test when f is not valid.
func encodedFilter(t *testing.T, f *source.Filter) string {
	t.Helper()

	text, err := source.EncodeFilter(*f)
	if err != nil {
		t.Fatalf("encode the filter: %v", err)
	}

	return text
}
