package principaltest

import (
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/principal"
)

// GroupMappingStore is the store that RunGroupMappingStore checks: the
// principals and groups it maps to, and the mappings.
type GroupMappingStore interface {
	principal.Store
	principal.GroupMappingStore
}

// RunGroupMappingStore runs the conformance suite of a
// principal.GroupMappingStore against stores produced by newStore.
func RunGroupMappingStore(t *testing.T, newStore func(t *testing.T) GroupMappingStore) {
	t.Helper()

	cases := []struct {
		name string
		test func(t *testing.T, s GroupMappingStore)
	}{
		{"APutMappingIsListedWithItsCreator", testMappingRoundTrip},
		{"APutOfTheSameMappingIsErrAlreadyExists", testMappingDuplicate},
		{"AMappingToAMissingGroupIsErrNotFound", testMappingMissingGroup},
		{"AMappingToAGroupOfADirectoryIsErrManagedByDirectory", testMappingDirectoryGroup},
		{"ADeleteEndsTheMapping", testMappingDelete},
		{"ADeleteOfAMissingMappingIsErrNotFound", testMappingDeleteMissing},
		{"AListKeepsEachSourceAndSourceIDApart", testMappingListApart},
		{"AMappingThatFailsTheChecksIsErrInvalid", testMappingInvalid},
		{"AValueThatDiffersOnlyInCaseIsAnotherMapping", testMappingCase},
		{"AValueThatIsSQLTextIsStoredAsText", testMappingSQLText},
		{"EachWriteAppendsOneAuditEntry", testMappingAudit},
		{"AnEntryThatTheLogRefusesChangesNothing", testMappingRefusedEntry},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.test(t, newStore(t))
		})
	}
}

// newMapping returns a valid mapping to the group.
func newMapping(value, groupID string) principal.GroupMapping {
	return principal.GroupMapping{
		CreatedAt: at(2), Source: claimSource, SourceID: claimSourceID, ClaimValue: value, GroupID: groupID,
		CreatedBy: "admin",
	}
}

// mappingKey returns the key of the mapping of the value to the group.
func mappingKey(value string) principal.GroupMappingKey {
	return newMapping(value, "grp-eng").Key()
}

func mappingStoreWorld(t *testing.T, s GroupMappingStore) {
	t.Helper()

	mustCreateGroup(t, s, newGroup("grp-eng", "eng"))
	mustCreateGroup(t, s, newGroup("grp-ops", "ops"))
}

func testMappingRoundTrip(t *testing.T, s GroupMappingStore) {
	t.Helper()

	mappingStoreWorld(t, s)

	want := newMapping("eng-team", "grp-eng")
	if err := s.PutGroupMapping(t.Context(), want, entry()); err != nil {
		t.Fatalf("put: %v", err)
	}

	got, err := s.ListGroupMappings(t.Context(), claimSource, claimSourceID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	if len(got) != 1 || got[0] != want {
		t.Fatalf("mappings = %+v, want [%+v]", got, want)
	}
}

func testMappingDuplicate(t *testing.T, s GroupMappingStore) {
	t.Helper()

	mappingStoreWorld(t, s)

	m := newMapping("eng-team", "grp-eng")
	if err := s.PutGroupMapping(t.Context(), m, entry()); err != nil {
		t.Fatalf("put: %v", err)
	}

	wantErr(t, "second put", s.PutGroupMapping(t.Context(), m, entry()), principal.ErrAlreadyExists)

	// The same value to a second group is another mapping.
	if err := s.PutGroupMapping(t.Context(), newMapping("eng-team", "grp-ops"), entry()); err != nil {
		t.Fatalf("put to a second group: %v", err)
	}
}

func testMappingMissingGroup(t *testing.T, s GroupMappingStore) {
	t.Helper()

	err := s.PutGroupMapping(t.Context(), newMapping("eng-team", "grp-missing"), entry())
	wantErr(t, "put to a missing group", err, principal.ErrNotFound)

	mappingStoreWorld(t, s)

	err = s.DeleteGroup(t.Context(), "grp-eng", entry())
	if err != nil {
		t.Fatalf("delete group: %v", err)
	}

	err = s.PutGroupMapping(t.Context(), newMapping("eng-team", "grp-eng"), entry())
	wantErr(t, "put to a deleted group", err, principal.ErrNotFound)
}

func testMappingDirectoryGroup(t *testing.T, s GroupMappingStore) {
	t.Helper()

	g := newGroup("grp-dir", "directory")
	g.Source = principal.SourceSCIM

	mustCreateGroup(t, s, g)

	err := s.PutGroupMapping(t.Context(), newMapping("eng-team", "grp-dir"), entry())
	wantErr(t, "put to a directory group", err, principal.ErrManagedByDirectory)
}

func testMappingDelete(t *testing.T, s GroupMappingStore) {
	t.Helper()

	mappingStoreWorld(t, s)

	if err := s.PutGroupMapping(t.Context(), newMapping("eng-team", "grp-eng"), entry()); err != nil {
		t.Fatalf("put: %v", err)
	}

	if err := s.DeleteGroupMapping(t.Context(), mappingKey("eng-team"), entry()); err != nil {
		t.Fatalf("delete: %v", err)
	}

	got, err := s.ListGroupMappings(t.Context(), claimSource, claimSourceID)
	if err != nil || len(got) != 0 {
		t.Fatalf("mappings = %+v, err = %v, want none", got, err)
	}
}

func testMappingDeleteMissing(t *testing.T, s GroupMappingStore) {
	t.Helper()

	err := s.DeleteGroupMapping(t.Context(), mappingKey("eng-team"), entry())
	wantErr(t, "delete of a missing mapping", err, principal.ErrNotFound)
}

func testMappingListApart(t *testing.T, s GroupMappingStore) {
	t.Helper()

	mappingStoreWorld(t, s)

	other := newMapping("ops-team", "grp-ops")
	other.SourceID = otherSourceID

	scim := newMapping("scim-team", "grp-ops")
	scim.Source = principal.MembershipSourceSCIM

	for _, m := range []principal.GroupMapping{newMapping("eng-team", "grp-eng"), other, scim} {
		if err := s.PutGroupMapping(t.Context(), m, entry()); err != nil {
			t.Fatalf("put: %v", err)
		}
	}

	got, err := s.ListGroupMappings(t.Context(), claimSource, claimSourceID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	if len(got) != 1 || got[0].ClaimValue != "eng-team" {
		t.Fatalf("mappings = %+v, want only the one of the source and the source ID", got)
	}
}

func testMappingInvalid(t *testing.T, s GroupMappingStore) {
	t.Helper()

	mappingStoreWorld(t, s)

	base := newMapping("eng-team", "grp-eng")

	cases := map[string]func(m *principal.GroupMapping){
		"the source local":       func(m *principal.GroupMapping) { m.Source = principal.MembershipSourceLocal },
		"an unknown source":      func(m *principal.GroupMapping) { m.Source = "ldap-x" },
		"no source ID":           func(m *principal.GroupMapping) { m.SourceID = "" },
		"no value":               func(m *principal.GroupMapping) { m.ClaimValue = "" },
		"a value of 257 bytes":   func(m *principal.GroupMapping) { m.ClaimValue = strings.Repeat("a", 257) },
		"a NUL byte":             func(m *principal.GroupMapping) { m.ClaimValue = "a\x00b" },
		"a CR LF pair":           func(m *principal.GroupMapping) { m.ClaimValue = "a\r\nb" },
		"a source ID with a NUL": func(m *principal.GroupMapping) { m.SourceID = "a\x00b" },
		"no group":               func(m *principal.GroupMapping) { m.GroupID = "" },
		"no creator":             func(m *principal.GroupMapping) { m.CreatedBy = "" },
	}

	for name, mutate := range cases {
		m := base
		mutate(&m)

		wantErr(t, "put with "+name, s.PutGroupMapping(t.Context(), m, entry()), principal.ErrInvalid)
	}

	got, err := s.ListGroupMappings(t.Context(), claimSource, claimSourceID)
	if err != nil || len(got) != 0 {
		t.Fatalf("mappings = %+v, err = %v, want none after the refused puts", got, err)
	}

	_, err = s.ListGroupMappings(t.Context(), principal.MembershipSourceLocal, claimSourceID)
	wantErr(t, "list for the source local", err, principal.ErrInvalid)

	noSourceID := mappingKey("eng-team")
	noSourceID.SourceID = ""

	err = s.DeleteGroupMapping(t.Context(), noSourceID, entry())
	wantErr(t, "delete with no source ID", err, principal.ErrInvalid)
}

func testMappingCase(t *testing.T, s GroupMappingStore) {
	t.Helper()

	mappingStoreWorld(t, s)

	for _, v := range []string{"Eng-Team", "eng-team"} {
		if err := s.PutGroupMapping(t.Context(), newMapping(v, "grp-eng"), entry()); err != nil {
			t.Fatalf("put %q: %v", v, err)
		}
	}

	got, err := s.ListGroupMappings(t.Context(), claimSource, claimSourceID)
	if err != nil || len(got) != 2 {
		t.Fatalf("mappings = %+v, err = %v, want two mappings", got, err)
	}

	err = s.DeleteGroupMapping(t.Context(), mappingKey("ENG-TEAM"), entry())
	wantErr(t, "delete of a value that differs in case", err, principal.ErrNotFound)
}

func testMappingSQLText(t *testing.T, s GroupMappingStore) {
	t.Helper()

	mappingStoreWorld(t, s)

	const value = "' OR 1=1 --"

	if err := s.PutGroupMapping(t.Context(), newMapping(value, "grp-eng"), entry()); err != nil {
		t.Fatalf("put: %v", err)
	}

	got, err := s.ListGroupMappings(t.Context(), claimSource, claimSourceID)
	if err != nil || len(got) != 1 || got[0].ClaimValue != value {
		t.Fatalf("mappings = %+v, err = %v, want the value as text", got, err)
	}
}

func testMappingAudit(t *testing.T, s GroupMappingStore) {
	t.Helper()

	mappingStoreWorld(t, s)

	n := len(auditEntries(t, s))
	e := entry()
	e.Action = principal.AuditActionMappingPut
	e.Object = "group-mapping:grp-eng"

	if err := s.PutGroupMapping(t.Context(), newMapping("eng-team", "grp-eng"), e); err != nil {
		t.Fatalf("put: %v", err)
	}

	got := entriesAfter(t, s, n)
	if len(got) != 1 || got[0].Action != principal.AuditActionMappingPut {
		t.Fatalf("entries = %+v, want one put entry", got)
	}

	n = len(auditEntries(t, s))
	e.Action = principal.AuditActionMappingDelete

	if err := s.DeleteGroupMapping(t.Context(), mappingKey("eng-team"), e); err != nil {
		t.Fatalf("delete: %v", err)
	}

	got = entriesAfter(t, s, n)
	if len(got) != 1 || got[0].Action != principal.AuditActionMappingDelete {
		t.Fatalf("entries = %+v, want one delete entry", got)
	}
}

func testMappingRefusedEntry(t *testing.T, s GroupMappingStore) {
	t.Helper()

	mappingStoreWorld(t, s)

	err := s.PutGroupMapping(t.Context(), newMapping("eng-team", "grp-eng"), refused())
	wantErr(t, "put with a refused entry", err, audit.ErrInvalidEntry)

	got, listErr := s.ListGroupMappings(t.Context(), claimSource, claimSourceID)
	if listErr != nil || len(got) != 0 {
		t.Fatalf("mappings = %+v, err = %v, want none after the refused entry", got, listErr)
	}
}
