package principaltest

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
)

// Names and IDs that the cases of the group claims use.
const (
	claimSource   = principal.MembershipSourceClaim
	claimSourceID = "oidc-1"
	otherSourceID = "oidc-2"
	claimPerson   = "prn-claim"
	engGroup      = "grp-eng"
	opsGroup      = "grp-ops"
)

// GroupMapperFixture is an implementation of principal.GroupMapper over a
// fresh store, with a way to set the mapping of a source.
type GroupMapperFixture struct {
	// Mapper is the implementation under test.
	Mapper principal.GroupMapper
	// Store is the store of the principals and the groups. It also keeps the
	// audit log, so that a case can read the entries.
	Store principal.Store
	// SetMapping stores the mapping of value to the group IDs for the
	// source and the source ID, in the form that the implementation uses.
	SetMapping func(t *testing.T, source principal.MembershipSource, sourceID, value string, groupIDs ...string)
	// NamespacePrincipal makes a principal of a namespace and returns its
	// ID. The groups of the installation are not in its home.
	NamespacePrincipal func(t *testing.T) string
	// BreakReads makes each later read of the mappings fail.
	BreakReads func(t *testing.T)
}

// RunGroupMapper runs the conformance suite of a principal.GroupMapper. The
// cases hold for each form of a mapping. They use the explicit map from a
// value to a group.
func RunGroupMapper(t *testing.T, fixture func(t *testing.T) GroupMapperFixture) {
	t.Helper()

	cases := []struct {
		name string
		test func(t *testing.T, fx GroupMapperFixture)
	}{
		{"AMappedValueReturnsItsGroups", testMapperReturnsGroups},
		{"AValueCanMapToMoreThanOneGroup", testMapperMoreThanOneGroup},
		{"AnUnmappedValueHasNoKey", testMapperUnmappedHasNoKey},
		{"ASourceWithNoMappingIsErrNoGroupMapping", testMapperNoMapping},
		{"TheMappingOfOneSourceIDNeverAppliesToAnother", testMapperOtherSourceID},
		{"TheMappingOfOneSourceNeverAppliesToAnother", testMapperOtherSource},
		{"AGroupOfAnotherHomeIsNeverReturned", testMapperOtherHome},
		{"AGroupRenameDoesNotChangeTheMapping", testMapperRename},
		{"ANewGroupDoesNotJoinAMapping", testMapperNewGroup},
		{"ADeletedGroupIsNeverReturned", testMapperDeletedGroup},
		{"AValueThatDiffersOnlyInCaseIsAnotherValue", testMapperCase},
		{"AFailedReadIsAnErrorAndNotAnEmptyMapping", testMapperFailedRead},
		{"AValueThatIsSQLTextMapsToNothing", testMapperSQLText},
		{"AHostileValueIsErrInvalidAtTheMapper", testMapperHostileValue},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.test(t, fixture(t))
		})
	}
}

// mapperWorld makes a person and the groups "eng" and "ops" for a mapper
// case.
func mapperWorld(t *testing.T, s principal.Store) string {
	t.Helper()

	mustCreatePrincipal(t, s, newPrincipal(claimPerson, "alice", principal.KindPerson))
	mustCreateGroup(t, s, newGroup(engGroup, "eng"))
	mustCreateGroup(t, s, newGroup(opsGroup, "ops"))

	return claimPerson
}

// mapFrom names the source and the source ID that a mapper case asks.
type mapFrom struct {
	source   principal.MembershipSource
	sourceID string
}

// claimFrom returns the source and the source ID of the cases.
func claimFrom() mapFrom {
	return mapFrom{source: claimSource, sourceID: claimSourceID}
}

func mapGroups(
	t *testing.T, fx GroupMapperFixture, pid string, from mapFrom, values ...string,
) (map[string][]string, error) {
	t.Helper()

	return fx.Mapper.MapGroups(t.Context(), pid, from.source, from.sourceID, values)
}

func wantMapping(t *testing.T, got, want map[string][]string) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("mapping = %v, want %v", got, want)
	}

	for k, w := range want {
		g := slices.Clone(got[k])
		slices.Sort(g)
		slices.Sort(w)

		if !slices.Equal(g, w) {
			t.Fatalf("mapping[%q] = %v, want %v", k, g, w)
		}
	}
}

func testMapperReturnsGroups(t *testing.T, fx GroupMapperFixture) {
	t.Helper()

	pid, eng := mapperWorld(t, fx.Store), engGroup
	fx.SetMapping(t, claimSource, claimSourceID, "eng-team", eng)

	got, err := mapGroups(t, fx, pid, claimFrom(), "eng-team")
	if err != nil {
		t.Fatalf("MapGroups: %v", err)
	}

	wantMapping(t, got, map[string][]string{"eng-team": {eng}})
}

func testMapperMoreThanOneGroup(t *testing.T, fx GroupMapperFixture) {
	t.Helper()

	pid, eng, ops := mapperWorld(t, fx.Store), engGroup, opsGroup
	fx.SetMapping(t, claimSource, claimSourceID, "staff", eng, ops)

	got, err := mapGroups(t, fx, pid, claimFrom(), "staff")
	if err != nil {
		t.Fatalf("MapGroups: %v", err)
	}

	wantMapping(t, got, map[string][]string{"staff": {eng, ops}})
}

func testMapperUnmappedHasNoKey(t *testing.T, fx GroupMapperFixture) {
	t.Helper()

	pid, eng := mapperWorld(t, fx.Store), engGroup
	fx.SetMapping(t, claimSource, claimSourceID, "eng-team", eng)

	got, err := mapGroups(t, fx, pid, claimFrom(), "eng-team", "unknown")
	if err != nil {
		t.Fatalf("MapGroups: %v", err)
	}

	if _, ok := got["unknown"]; ok {
		t.Fatalf("mapping = %v, want no key for the unmapped value", got)
	}

	wantMapping(t, got, map[string][]string{"eng-team": {eng}})
}

func testMapperNoMapping(t *testing.T, fx GroupMapperFixture) {
	t.Helper()

	pid := mapperWorld(t, fx.Store)

	got, err := mapGroups(t, fx, pid, claimFrom(), "eng-team")
	wantErr(t, "MapGroups with no mapping", err, principal.ErrNoGroupMapping)

	if len(got) != 0 {
		t.Fatalf("mapping = %v, want none", got)
	}
}

func testMapperOtherSourceID(t *testing.T, fx GroupMapperFixture) {
	t.Helper()

	pid, eng := mapperWorld(t, fx.Store), engGroup
	fx.SetMapping(t, claimSource, claimSourceID, "eng-team", eng)

	_, err := mapGroups(t, fx, pid, mapFrom{source: claimSource, sourceID: otherSourceID}, "eng-team")
	wantErr(t, "MapGroups for another source ID", err, principal.ErrNoGroupMapping)

	// Another source ID with a mapping of its own never gets the value of
	// the first.
	fx.SetMapping(t, claimSource, otherSourceID, "ops-team", "grp-ops")

	got, err := mapGroups(t, fx, pid, mapFrom{source: claimSource, sourceID: otherSourceID}, "eng-team")
	if err != nil {
		t.Fatalf("MapGroups: %v", err)
	}

	wantMapping(t, got, map[string][]string{})
}

func testMapperOtherSource(t *testing.T, fx GroupMapperFixture) {
	t.Helper()

	pid, eng := mapperWorld(t, fx.Store), engGroup
	fx.SetMapping(t, claimSource, claimSourceID, "eng-team", eng)

	_, err := mapGroups(
		t,
		fx,
		pid,
		mapFrom{source: principal.MembershipSourceSCIM, sourceID: claimSourceID},
		"eng-team",
	)
	wantErr(t, "MapGroups for another source", err, principal.ErrNoGroupMapping)
}

func testMapperOtherHome(t *testing.T, fx GroupMapperFixture) {
	t.Helper()

	mapperWorld(t, fx.Store)

	eng := engGroup
	fx.SetMapping(t, claimSource, claimSourceID, "eng-team", eng)

	got, err := mapGroups(t, fx, fx.NamespacePrincipal(t), claimFrom(), "eng-team")
	if err != nil {
		t.Fatalf("MapGroups: %v", err)
	}

	if len(got) != 0 {
		t.Fatalf("mapping = %v, want no group in the home of a principal of a namespace", got)
	}
}

func testMapperRename(t *testing.T, fx GroupMapperFixture) {
	t.Helper()

	pid, eng := mapperWorld(t, fx.Store), engGroup
	fx.SetMapping(t, claimSource, claimSourceID, "eng-team", eng)

	renamed := newGroup(eng, "engineering")
	renamed.UpdatedAt = at(5)

	if err := fx.Store.UpdateGroup(t.Context(), renamed, entry()); err != nil {
		t.Fatalf("rename: %v", err)
	}

	got, err := mapGroups(t, fx, pid, claimFrom(), "eng-team")
	if err != nil {
		t.Fatalf("MapGroups: %v", err)
	}

	wantMapping(t, got, map[string][]string{"eng-team": {eng}})
}

func testMapperNewGroup(t *testing.T, fx GroupMapperFixture) {
	t.Helper()

	pid, eng := mapperWorld(t, fx.Store), engGroup
	fx.SetMapping(t, claimSource, claimSourceID, "eng-team", eng)

	// A group that is made later, even with the name of the value, joins no
	// mapping.
	mustCreateGroup(t, fx.Store, newGroup("grp-new", "eng-team"))

	got, err := mapGroups(t, fx, pid, claimFrom(), "eng-team")
	if err != nil {
		t.Fatalf("MapGroups: %v", err)
	}

	wantMapping(t, got, map[string][]string{"eng-team": {eng}})
}

func testMapperDeletedGroup(t *testing.T, fx GroupMapperFixture) {
	t.Helper()

	pid, eng := mapperWorld(t, fx.Store), engGroup
	fx.SetMapping(t, claimSource, claimSourceID, "eng-team", eng)

	if err := fx.Store.DeleteGroup(t.Context(), eng, entry()); err != nil {
		t.Fatalf("delete group: %v", err)
	}

	got, err := mapGroups(t, fx, pid, claimFrom(), "eng-team")
	if err != nil {
		t.Fatalf("MapGroups: %v", err)
	}

	if len(got) != 0 {
		t.Fatalf("mapping = %v, want no deleted group", got)
	}
}

func testMapperCase(t *testing.T, fx GroupMapperFixture) {
	t.Helper()

	pid, eng := mapperWorld(t, fx.Store), engGroup
	fx.SetMapping(t, claimSource, claimSourceID, "Eng-Team", eng)

	got, err := mapGroups(t, fx, pid, claimFrom(), "eng-team", "ENG-TEAM")
	if err != nil {
		t.Fatalf("MapGroups: %v", err)
	}

	if len(got) != 0 {
		t.Fatalf("mapping = %v, want a value that differs in case to map to nothing", got)
	}

	got, err = mapGroups(t, fx, pid, claimFrom(), "Eng-Team")
	if err != nil {
		t.Fatalf("MapGroups: %v", err)
	}

	wantMapping(t, got, map[string][]string{"Eng-Team": {eng}})
}

func testMapperFailedRead(t *testing.T, fx GroupMapperFixture) {
	t.Helper()

	pid, eng := mapperWorld(t, fx.Store), engGroup
	fx.SetMapping(t, claimSource, claimSourceID, "eng-team", eng)
	fx.BreakReads(t)

	got, err := mapGroups(t, fx, pid, claimFrom(), "eng-team")
	if err == nil {
		t.Fatalf("MapGroups with a failed read = %v, want an error", got)
	}

	if errors.Is(err, principal.ErrNoGroupMapping) {
		t.Fatalf("MapGroups with a failed read = %v, want an error that is not ErrNoGroupMapping", err)
	}

	if len(got) != 0 {
		t.Fatalf("mapping = %v, want none with an error", got)
	}
}

func testMapperSQLText(t *testing.T, fx GroupMapperFixture) {
	t.Helper()

	pid, eng := mapperWorld(t, fx.Store), engGroup
	fx.SetMapping(t, claimSource, claimSourceID, "eng-team", eng)

	got, err := mapGroups(t, fx, pid, claimFrom(), "' OR 1=1 --", "x' OR '1'='1")
	if err != nil {
		t.Fatalf("MapGroups: %v", err)
	}

	if len(got) != 0 {
		t.Fatalf("mapping = %v, want text for SQL to map to nothing", got)
	}
}

func testMapperHostileValue(t *testing.T, fx GroupMapperFixture) {
	t.Helper()

	pid, eng := mapperWorld(t, fx.Store), engGroup
	fx.SetMapping(t, claimSource, claimSourceID, "eng-team", eng)

	for name, values := range hostileValues() {
		_, err := fx.Mapper.MapGroups(t.Context(), pid, claimSource, claimSourceID, values)
		wantErr(t, "MapGroups with "+name, err, principal.ErrInvalid)
	}

	_, err := fx.Mapper.MapGroups(t.Context(), pid, principal.MembershipSourceLocal, claimSourceID, []string{"x"})
	wantErr(t, "MapGroups for the source local", err, principal.ErrInvalid)

	_, err = fx.Mapper.MapGroups(t.Context(), pid, claimSource, "", []string{"x"})
	wantErr(t, "MapGroups with no source ID", err, principal.ErrInvalid)
}

// hostileValues returns lists of group values that fail the limits.
func hostileValues() map[string][]string {
	return map[string][]string{
		"1001 values":            slices.Repeat([]string{"v"}, principal.MaxGroupClaimValues+1),
		"a value of 257 bytes":   {strings.Repeat("a", principal.MaxGroupClaimValueBytes+1)},
		"a NUL byte":             {"a\x00b"},
		"a CR LF pair":           {"a\r\nb"},
		"an empty value":         {""},
		"text that is not UTF-8": {"a\xffb"},
	}
}

// GroupClaimsFixture is an implementation of principal.GroupClaimApplier
// over a fresh store and a mapper, with the store so that a case can make
// principals and groups and read the memberships and the audit entries.
type GroupClaimsFixture struct {
	// Applier is the implementation under test.
	Applier principal.GroupClaimApplier
	// Store is the store of the principals and the groups. It also keeps the
	// audit log, so that a case can read the entries.
	Store principal.Store
	// Mapping sets the mapping of the mapper that Applier uses.
	Mapping GroupMapperFixture
	// WithMapper returns an applier over the same store that reads the
	// mapping with mapper.
	WithMapper func(mapper principal.GroupMapper) principal.GroupClaimApplier
	// MakeTempAdmin makes the principal a temporary administrator.
	MakeTempAdmin func(t *testing.T, principalID string)
	// GrantSiteAdministrator gives the subject the site-administrator role on
	// the installation.
	GrantSiteAdministrator func(t *testing.T, subject access.Subject)
	// BreakMemberWrites makes each later insert of a membership fail.
	BreakMemberWrites func(t *testing.T)
	// BreakAuditAppends makes each later append to the audit log fail.
	BreakAuditAppends func(t *testing.T)
}

// protectionSkip is the reason of a case whose rule has no stored data yet.
const (
	skipProtectedGroup = "skipped: the protected-group rule has no data yet: " +
		"the server computes no protection for a group"
	skipClosedGroup = "skipped: the closed-group rule is not applied yet: " +
		"the applier does not read the closed flag of a group"
	skipFullSync = "skipped: the full-sync rule has no data yet: no installation setting for full sync exists"
	skipSuspend  = "skipped: the suspension rule has no data yet: " +
		"no protected group exists, so no removal can suspend a membership"
	skipMassAlert = "skipped: the mass-suspension alert has no data yet: no counter of removals exists"
)

// RunGroupClaims runs the conformance suite of a
// principal.GroupClaimApplier. Each case names one rule. The cases of the
// rules for protected groups are skipped, each with the data it lacks. A case
// that sends hostile input tests one defense-in-depth layer alone, as if every
// other layer had failed.
func RunGroupClaims(t *testing.T, fixture func(t *testing.T) GroupClaimsFixture) {
	t.Helper()

	cases := []struct {
		name string
		skip string
		test func(t *testing.T, fx GroupClaimsFixture)
	}{
		{"ClaimsNotPresentChangeNothing", "", testClaimsNotPresent},
		{"NoMappingJoinsNoGroupAndChangesNothing", "", testNoMappingChangesNothing},
		{"NoMappingWritesOneAuditEntry", "", testNoMappingAudit},
		{"AnEmptyPresentListEndsEachMembershipOfTheSource", "", testEmptyListEndsMemberships},
		{"AMappedValueAddsAnActiveMembership", "", testMappedValueAdds},
		{"AnUnmappedValueIsListedAndJoinsNoGroup", "", testUnmappedValue},
		{"TheCallNeverMakesAGroup", "", testNeverMakesAGroup},
		{"TheMembershipRecordsTheSourceAndTheSourceID", "", testMembershipRecordsSource},
		{"AMembershipThatIsAlreadyActiveIsNotAddedAgain", "", testNotAddedAgain},
		{"AGroupMissingFromTheClaimsEndsTheMembership", "", testMissingGroupEnds},
		{"AClaimNeverEndsTheMembershipOfTheLastSiteAdministrator", "", testLastSiteAdminKept},
		{"AClaimMayEndTheMembershipOfASiteAdministratorWhenAnotherExists", "", testSiteAdminRemovedWithAnother},
		{"OfTwoRemovalsThatEndTheLastSiteAdministratorTheSecondIsRefused", "", testSecondOfLastTwoRefused},
		{"ALocalMembershipNeverChanges", "", testLocalNeverChanges},
		{"AMembershipOfAnotherSourceIDNeverChanges", "", testOtherSourceIDNeverChanges},
		{"APinnedMembershipNeverChanges", "", testPinnedNeverChanges},
		// The rules for groups from an identity provider.
		{"AProtectedGroupRefusesTheAdd", skipProtectedGroup, nil},
		{"AProtectedGroupThatTrustsTheIdentityProviderAcceptsTheAdd", skipClosedGroup, nil},
		{"AGroupClosedToTheIdentityProviderRefusesTheAdd", skipClosedGroup, nil},
		{"FullSyncAcceptsTheAddToAProtectedGroup", skipFullSync, nil},
		{"ARemovalFromAProtectedGroupSuspendsTheMembership", skipSuspend, nil},
		{"ARemovalFromAProtectedGroupWithFullSyncIsFinal", skipFullSync, nil},
		{"ASuspendedMembershipThatTheClaimsNameAgainStaysSuspended", skipSuspend, nil},
		{"RemovalsCountForTheMassSuspensionAlert", skipMassAlert, nil},
		// Audit entries.
		{"EachAddRemovalSuspensionAndRefusalWritesAnAuditEntry", "", testEachEffectAudited},
		{"TheAuditEntriesAreInTheTransactionOfTheChanges", "", testAuditEntriesCarryTheCall},
		{"UnmappedValuesWriteOneAuditEntryWithTheBoundedList", "", testUnmappedAudit},
		// Layer 1: the boundary.
		{"MoreThanTheLimitOfGroupsIsErrInvalid", "", testTooManyGroups},
		{"AGroupValueLongerThanTheLimitIsErrInvalid", "", testLongValue},
		{"AGroupValueWithControlCharactersIsErrInvalid", "", testControlCharacters},
		{"TheSourceLocalIsErrInvalid", "", testSourceLocal},
		{"AnEmptySourceIDIsErrInvalid", "", testEmptySourceID},
		// Layer 2: the value as text for SQL.
		{"AValueThatIsSQLTextJoinsNoGroup", "", testSQLText},
		// Layer 4: the principal of the claims.
		{"AnUnknownPrincipalIsErrNotFound", "", testUnknownPrincipal},
		{"ADisabledPrincipalIsErrDisabled", "", testDisabledPrincipal},
		{"AServicePrincipalIsErrNotSignInTarget", "", testServicePrincipal},
		{"ABreakGlassAccountIsErrNotSignInTarget", "", testBreakGlassAccount},
		{"ATemporaryAdministratorIsErrNotSignInTarget", "", testTempAdmin},
		{"AGroupIDOfAnotherHomeFromTheMapperIsRefused", "", testGroupOfAnotherHome},
		// Layer 5: fail closed.
		{"AFailedMapperReadChangesNothingAndReturnsTheError", "", testFailedMapper},
		{"AFailedWriteChangesNothingAndReturnsTheError", "", testFailedWrite},
		{"AFailedAuditWriteChangesNothing", "", testFailedAudit},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if tc.skip != "" {
				t.Skip(tc.skip)
			}

			tc.test(t, fixture(t))
		})
	}
}

// claimsWorld makes a person, the groups "eng" and "ops", and the mapping
// "eng-team" to eng and "ops-team" to ops for the source claim and the
// source ID oidc-1.
func claimsWorld(t *testing.T, fx GroupClaimsFixture) {
	t.Helper()

	mustCreatePrincipal(t, fx.Store, newPrincipal(claimPerson, "alice", principal.KindPerson))
	mustCreateGroup(t, fx.Store, newGroup("grp-eng", "eng"))
	mustCreateGroup(t, fx.Store, newGroup("grp-ops", "ops"))
	fx.Mapping.SetMapping(t, claimSource, claimSourceID, "eng-team", "grp-eng")
	fx.Mapping.SetMapping(t, claimSource, claimSourceID, "ops-team", "grp-ops")
}

// claims returns present claims of the person for the source ID oidc-1.
func claims(groups ...string) principal.GroupClaims {
	return principal.GroupClaims{
		PrincipalID: claimPerson, Source: claimSource, SourceID: claimSourceID, Groups: groups, Present: true,
	}
}

func apply(t *testing.T, fx GroupClaimsFixture, c principal.GroupClaims) principal.GroupClaimResult {
	t.Helper()

	res, err := fx.Applier.ApplyGroupClaims(t.Context(), entry(), c)
	if err != nil {
		t.Fatalf("ApplyGroupClaims: %v", err)
	}

	return res
}

// currentGroups returns the sorted IDs of the current groups of the person.
func currentGroups(t *testing.T, s principal.Store) []string {
	t.Helper()

	groups, _, err := s.ListGroupsOf(t.Context(), claimPerson, all())
	if err != nil {
		t.Fatalf("list groups of the person: %v", err)
	}

	ids := make([]string, 0, len(groups))
	for i := range groups {
		ids = append(ids, groups[i].ID)
	}

	slices.Sort(ids)

	return ids
}

// memberOf returns the current membership of the person in the group.
func memberOf(t *testing.T, s principal.Store, groupID string) (principal.Member, bool) {
	t.Helper()

	members, _, err := s.ListMembers(t.Context(), groupID, all())
	if err != nil {
		t.Fatalf("list members of %s: %v", groupID, err)
	}

	for i := range members {
		if members[i].PrincipalID == claimPerson {
			return members[i], true
		}
	}

	var none principal.Member

	return none, false
}

// auditEntries returns each entry of the scope @server.
func auditEntries(t *testing.T, s principal.Store) []audit.Entry {
	t.Helper()

	l, ok := s.(auditLister)
	if !ok {
		t.Fatalf("the store %T keeps no audit log to read", s)
	}

	listing, err := l.List(t.Context(), audit.ServerScope, audit.Page{Number: 1, Size: audit.MaxPageSize})
	if err != nil {
		t.Fatalf("list the audit entries: %v", err)
	}

	// The log lists the newest entry first. The cases read it oldest first.
	entries := slices.Clone(listing.Entries)
	slices.SortFunc(entries, func(a, b audit.Entry) int { return int(a.Sequence - b.Sequence) })

	return entries
}

// entriesAfter returns the entries that follow the first n entries, oldest
// first.
func entriesAfter(t *testing.T, s principal.Store, n int) []audit.Entry {
	t.Helper()

	got := auditEntries(t, s)
	if len(got) < n {
		t.Fatalf("audit entries = %d, want at least %d", len(got), n)
	}

	return got[n:]
}

func actionsOf(entries []audit.Entry) []string {
	out := make([]string, 0, len(entries))
	for i := range entries {
		out = append(out, entries[i].Action)
	}

	slices.Sort(out)

	return out
}

// requireUnchanged fails unless the person is in exactly want groups and the
// audit log has n entries.
func requireUnchanged(t *testing.T, s principal.Store, wantGroups []string, n int) {
	t.Helper()

	equalStrings(t, "groups of the person", currentGroups(t, s), wantGroups)

	if got := len(auditEntries(t, s)); got != n {
		t.Fatalf("audit entries = %d, want %d: the call must write none", got, n)
	}
}

func testClaimsNotPresent(t *testing.T, fx GroupClaimsFixture) {
	t.Helper()

	claimsWorld(t, fx)

	before := len(auditEntries(t, fx.Store))
	c := claims("eng-team")
	c.Present = false

	res := apply(t, fx, c)
	if len(res.Added)+len(res.Removed)+len(res.Unmapped) != 0 || res.NoMapping {
		t.Fatalf("result = %+v, want an empty result", res)
	}

	requireUnchanged(t, fx.Store, []string{}, before)
}

func testNoMappingChangesNothing(t *testing.T, fx GroupClaimsFixture) {
	t.Helper()

	mustCreatePrincipal(t, fx.Store, newPrincipal(claimPerson, "alice", principal.KindPerson))
	mustCreateGroup(t, fx.Store, newGroup("grp-eng", "eng"))
	mustAddMember(t, fx.Store, "grp-eng", claimPerson)

	res := apply(t, fx, claims("eng-team"))
	if !res.NoMapping || len(res.Added) != 0 || len(res.Removed) != 0 {
		t.Fatalf("result = %+v, want NoMapping and no change", res)
	}

	if !slices.Equal(res.Unmapped, []string{"eng-team"}) {
		t.Fatalf("unmapped = %v, want every value", res.Unmapped)
	}

	// The local membership stays.
	equalStrings(t, "groups of the person", currentGroups(t, fx.Store), []string{"grp-eng"})
}

func testNoMappingAudit(t *testing.T, fx GroupClaimsFixture) {
	t.Helper()

	mustCreatePrincipal(t, fx.Store, newPrincipal(claimPerson, "alice", principal.KindPerson))

	before := len(auditEntries(t, fx.Store))
	apply(t, fx, claims("eng-team"))

	got := entriesAfter(t, fx.Store, before)
	if len(got) != 1 || got[0].Action != principal.AuditActionClaimNoMapping || got[0].Outcome != audit.OutcomeDenied {
		t.Fatalf("entries = %+v, want one denied entry %q", got, principal.AuditActionClaimNoMapping)
	}
}

func testEmptyListEndsMemberships(t *testing.T, fx GroupClaimsFixture) {
	t.Helper()

	claimsWorld(t, fx)
	apply(t, fx, claims("eng-team", "ops-team"))
	equalStrings(t, "groups after the add", currentGroups(t, fx.Store), []string{"grp-eng", "grp-ops"})

	res := apply(t, fx, claims())
	if !slices.Equal(sortedCopy(res.Removed), []string{"grp-eng", "grp-ops"}) {
		t.Fatalf("removed = %v, want both groups", res.Removed)
	}

	equalStrings(t, "groups after the empty list", currentGroups(t, fx.Store), []string{})
}

func sortedCopy(in []string) []string {
	out := slices.Clone(in)
	slices.Sort(out)

	return out
}

func testMappedValueAdds(t *testing.T, fx GroupClaimsFixture) {
	t.Helper()

	claimsWorld(t, fx)

	res := apply(t, fx, claims("eng-team"))
	if !slices.Equal(res.Added, []string{"grp-eng"}) {
		t.Fatalf("added = %v, want grp-eng", res.Added)
	}

	m, ok := memberOf(t, fx.Store, "grp-eng")
	if !ok || m.State != principal.MembershipStateActive {
		t.Fatalf("membership = %+v (found %v), want an active membership", m, ok)
	}
}

func testUnmappedValue(t *testing.T, fx GroupClaimsFixture) {
	t.Helper()

	claimsWorld(t, fx)

	res := apply(t, fx, claims("eng-team", "marketing"))
	if !slices.Equal(res.Unmapped, []string{"marketing"}) {
		t.Fatalf("unmapped = %v, want the value that maps to no group", res.Unmapped)
	}

	equalStrings(t, "groups of the person", currentGroups(t, fx.Store), []string{"grp-eng"})
}

func testNeverMakesAGroup(t *testing.T, fx GroupClaimsFixture) {
	t.Helper()

	claimsWorld(t, fx)

	_, before, err := fx.Store.ListGroups(t.Context(), all())
	if err != nil {
		t.Fatalf("list groups: %v", err)
	}

	apply(t, fx, claims("eng-team", "marketing", "sales"))

	_, after, err := fx.Store.ListGroups(t.Context(), all())
	if err != nil {
		t.Fatalf("list groups: %v", err)
	}

	if before != after {
		t.Fatalf("groups = %d, want %d: the call must not make a group", after, before)
	}
}

func testMembershipRecordsSource(t *testing.T, fx GroupClaimsFixture) {
	t.Helper()

	claimsWorld(t, fx)
	apply(t, fx, claims("eng-team"))

	m, ok := memberOf(t, fx.Store, "grp-eng")
	if !ok {
		t.Fatal("no membership in grp-eng")
	}

	if m.Source != claimSource || m.SourceID != claimSourceID || m.Pinned || !m.AddedAt.Equal(entry().At) {
		t.Fatalf("membership = %+v, want source %q, source ID %q, not pinned, added at the time of the entry",
			m, claimSource, claimSourceID)
	}
}

func testNotAddedAgain(t *testing.T, fx GroupClaimsFixture) {
	t.Helper()

	claimsWorld(t, fx)
	apply(t, fx, claims("eng-team"))

	before := len(auditEntries(t, fx.Store))
	first, _ := memberOf(t, fx.Store, "grp-eng")

	res := apply(t, fx, claims("eng-team"))
	if len(res.Added) != 0 || len(res.Removed) != 0 {
		t.Fatalf("result = %+v, want no change", res)
	}

	again, _ := memberOf(t, fx.Store, "grp-eng")
	if !again.AddedAt.Equal(first.AddedAt) {
		t.Fatalf("added at = %v, want %v: the membership must stay", again.AddedAt, first.AddedAt)
	}

	requireUnchanged(t, fx.Store, []string{"grp-eng"}, before)
}

func testMissingGroupEnds(t *testing.T, fx GroupClaimsFixture) {
	t.Helper()

	claimsWorld(t, fx)
	apply(t, fx, claims("eng-team", "ops-team"))

	res := apply(t, fx, claims("eng-team"))
	if !slices.Equal(res.Removed, []string{"grp-ops"}) {
		t.Fatalf("removed = %v, want grp-ops", res.Removed)
	}

	equalStrings(t, "groups of the person", currentGroups(t, fx.Store), []string{"grp-eng"})
}

// groupSubject names the group as a subject of a grant.
func groupSubject(id string) access.Subject {
	return access.Subject{Kind: access.SubjectGroup, ID: id}
}

func testLastSiteAdminKept(t *testing.T, fx GroupClaimsFixture) {
	t.Helper()

	claimsWorld(t, fx)
	fx.GrantSiteAdministrator(t, groupSubject(engGroup))
	apply(t, fx, claims("eng-team"))

	n := len(auditEntries(t, fx.Store))
	res := apply(t, fx, claims())

	if len(res.Removed) != 0 || !slices.Equal(res.Refused, []string{engGroup}) {
		t.Fatalf("result = %+v, want the removal refused and the group kept", res)
	}

	if _, ok := memberOf(t, fx.Store, engGroup); !ok {
		t.Fatal("the membership of the last site administrator ended")
	}

	got := entriesAfter(t, fx.Store, n)
	if len(got) != 1 || got[0].Action != principal.AuditActionClaimRefuse || got[0].Outcome != audit.OutcomeDenied ||
		got[0].Object != "group:"+engGroup {
		t.Fatalf("entries = %+v, want one denied refusal for %s", got, engGroup)
	}
}

func testSiteAdminRemovedWithAnother(t *testing.T, fx GroupClaimsFixture) {
	t.Helper()

	claimsWorld(t, fx)
	mustCreatePrincipal(t, fx.Store, newPrincipal("prn-second", "bob", principal.KindPerson))
	fx.GrantSiteAdministrator(t, groupSubject(engGroup))
	fx.GrantSiteAdministrator(t, access.Subject{Kind: access.SubjectPrincipal, ID: "prn-second"})
	apply(t, fx, claims("eng-team"))

	res := apply(t, fx, claims())
	if !slices.Equal(res.Removed, []string{engGroup}) || len(res.Refused) != 0 {
		t.Fatalf("result = %+v, want the removal allowed while another site administrator exists", res)
	}
}

func testSecondOfLastTwoRefused(t *testing.T, fx GroupClaimsFixture) {
	t.Helper()

	claimsWorld(t, fx)
	fx.GrantSiteAdministrator(t, groupSubject(engGroup))
	fx.GrantSiteAdministrator(t, groupSubject(opsGroup))
	apply(t, fx, claims("eng-team", "ops-team"))

	res := apply(t, fx, claims())
	if !slices.Equal(res.Removed, []string{engGroup}) || !slices.Equal(res.Refused, []string{opsGroup}) {
		t.Fatalf("result = %+v, want the first removal allowed and the second refused", res)
	}

	equalStrings(t, "groups of the person", currentGroups(t, fx.Store), []string{opsGroup})
}

func testLocalNeverChanges(t *testing.T, fx GroupClaimsFixture) {
	t.Helper()

	claimsWorld(t, fx)
	mustAddMember(t, fx.Store, "grp-ops", claimPerson)
	mustAddMember(t, fx.Store, "grp-eng", claimPerson)

	// The claims name eng, where the person is a local member, and not ops.
	res := apply(t, fx, claims("eng-team"))
	if len(res.Added) != 0 || len(res.Removed) != 0 {
		t.Fatalf("result = %+v, want no change to a local membership", res)
	}

	for _, gid := range []string{"grp-eng", "grp-ops"} {
		m, ok := memberOf(t, fx.Store, gid)
		if !ok || m.Source != principal.MembershipSourceLocal {
			t.Fatalf("membership in %s = %+v (found %v), want the local membership", gid, m, ok)
		}
	}
}

func testOtherSourceIDNeverChanges(t *testing.T, fx GroupClaimsFixture) {
	t.Helper()

	claimsWorld(t, fx)
	addClaimMember(t, fx.Store, "grp-ops", otherSourceID, false)

	res := apply(t, fx, claims("eng-team"))
	if len(res.Removed) != 0 {
		t.Fatalf("removed = %v, want none: another source ID keeps its membership", res.Removed)
	}

	m, ok := memberOf(t, fx.Store, "grp-ops")
	if !ok || m.SourceID != otherSourceID {
		t.Fatalf("membership = %+v (found %v), want the one of source ID %q", m, ok, otherSourceID)
	}
}

func testPinnedNeverChanges(t *testing.T, fx GroupClaimsFixture) {
	t.Helper()

	claimsWorld(t, fx)
	addClaimMember(t, fx.Store, "grp-ops", claimSourceID, true)

	res := apply(t, fx, claims("eng-team"))
	if len(res.Removed) != 0 {
		t.Fatalf("removed = %v, want none: a pinned membership stays", res.Removed)
	}

	m, ok := memberOf(t, fx.Store, "grp-ops")
	if !ok || !m.Pinned {
		t.Fatalf("membership = %+v (found %v), want the pinned membership", m, ok)
	}
}

// addClaimMember makes a membership of the person from a claim.
func addClaimMember(t *testing.T, s principal.Store, groupID, sourceID string, pinned bool) {
	t.Helper()

	m := principal.Membership{
		AddedAt: at(1), SuspendedAt: time.Time{}, GroupID: groupID, PrincipalID: claimPerson,
		Source: claimSource, SourceID: sourceID, State: principal.MembershipStateActive, Pinned: pinned,
	}
	if err := s.AddMember(t.Context(), m, entry(), nil); err != nil {
		t.Fatalf("add the claim membership: %v", err)
	}
}

// fakeMapper is a principal.GroupMapper with a fixed answer.
type fakeMapper struct {
	out map[string][]string
	err error
}

func (f fakeMapper) MapGroups(
	_ context.Context, _ string, _ principal.MembershipSource, _ string, _ []string,
) (map[string][]string, error) {
	return f.out, f.err
}

func testEachEffectAudited(t *testing.T, fx GroupClaimsFixture) {
	t.Helper()

	claimsWorld(t, fx)

	n := len(auditEntries(t, fx.Store))
	apply(t, fx, claims("eng-team"))

	got := entriesAfter(t, fx.Store, n)
	if len(got) != 1 || got[0].Action != principal.AuditActionClaimAdd || got[0].Object != "group:grp-eng" ||
		got[0].Outcome != audit.OutcomeSuccess {
		t.Fatalf("entries after the add = %+v, want one add entry for grp-eng", got)
	}

	n = len(auditEntries(t, fx.Store))
	apply(t, fx, claims())

	got = entriesAfter(t, fx.Store, n)
	if len(got) != 1 || got[0].Action != principal.AuditActionClaimRemove || got[0].Object != "group:grp-eng" {
		t.Fatalf("entries after the removal = %+v, want one removal entry for grp-eng", got)
	}

	n = len(auditEntries(t, fx.Store))
	refusing := fx.WithMapper(fakeMapper{out: map[string][]string{"eng-team": {"grp-gone"}}, err: nil})

	if _, err := refusing.ApplyGroupClaims(t.Context(), entry(), claims("eng-team")); err != nil {
		t.Fatalf("ApplyGroupClaims: %v", err)
	}

	got = entriesAfter(t, fx.Store, n)
	if len(got) != 1 || got[0].Action != principal.AuditActionClaimRefuse || got[0].Outcome != audit.OutcomeDenied {
		t.Fatalf("entries after the refusal = %+v, want one denied refusal entry", got)
	}
}

func testAuditEntriesCarryTheCall(t *testing.T, fx GroupClaimsFixture) {
	t.Helper()

	claimsWorld(t, fx)

	n := len(auditEntries(t, fx.Store))
	e := entry()
	e.Subject = "alice-sign-in"
	e.RequestID = "req-1"

	if _, err := fx.Applier.ApplyGroupClaims(t.Context(), e, claims("eng-team", "ops-team")); err != nil {
		t.Fatalf("ApplyGroupClaims: %v", err)
	}

	got := entriesAfter(t, fx.Store, n)
	if !slices.Equal(actionsOf(got), []string{principal.AuditActionClaimAdd, principal.AuditActionClaimAdd}) {
		t.Fatalf("actions = %v, want one add entry for each group", actionsOf(got))
	}

	for i := range got {
		if got[i].Subject != e.Subject || !got[i].At.Equal(e.At) || got[i].RequestID != e.RequestID {
			t.Fatalf("entry = %+v, want the subject, the time and the request of the call", got[i])
		}
	}
}

func testUnmappedAudit(t *testing.T, fx GroupClaimsFixture) {
	t.Helper()

	claimsWorld(t, fx)

	values := make([]string, 1, principal.MaxUnmappedAudited+6)
	values[0] = "eng-team"

	for i := range principal.MaxUnmappedAudited + 5 {
		values = append(values, "other-"+strconv.Itoa(i))
	}

	n := len(auditEntries(t, fx.Store))
	res := apply(t, fx, claims(values...))

	if len(res.Unmapped) != principal.MaxUnmappedAudited+5 {
		t.Fatalf("unmapped = %d values, want %d", len(res.Unmapped), principal.MaxUnmappedAudited+5)
	}

	var unmapped []audit.Entry

	after := entriesAfter(t, fx.Store, n)
	for i := range after {
		if after[i].Action == principal.AuditActionClaimUnmapped {
			unmapped = append(unmapped, after[i])
		}
	}

	if len(unmapped) != 1 {
		t.Fatalf("unmapped entries = %d, want one", len(unmapped))
	}

	listed := 0

	for _, c := range unmapped[0].Changes {
		if strings.HasPrefix(c.Key, "unmapped.") {
			listed++
		}

		if c.Key == "unmappedCount" && c.New != strconv.Itoa(principal.MaxUnmappedAudited+5) {
			t.Fatalf("unmappedCount = %s, want %d", c.New, principal.MaxUnmappedAudited+5)
		}
	}

	if listed != principal.MaxUnmappedAudited {
		t.Fatalf("listed values = %d, want the bound %d", listed, principal.MaxUnmappedAudited)
	}
}

// requireInvalid applies c and fails unless the call returns ErrInvalid and
// changes nothing.
func requireInvalid(t *testing.T, fx GroupClaimsFixture, what string, c principal.GroupClaims) {
	t.Helper()

	before := len(auditEntries(t, fx.Store))

	_, err := fx.Applier.ApplyGroupClaims(t.Context(), entry(), c)
	wantErr(t, what, err, principal.ErrInvalid)
	requireUnchanged(t, fx.Store, []string{}, before)
}

func testTooManyGroups(t *testing.T, fx GroupClaimsFixture) {
	t.Helper()

	claimsWorld(t, fx)
	requireInvalid(t, fx, "1001 values", claims(hostileValues()["1001 values"]...))
}

func testLongValue(t *testing.T, fx GroupClaimsFixture) {
	t.Helper()

	claimsWorld(t, fx)
	requireInvalid(t, fx, "a value of 257 bytes", claims(hostileValues()["a value of 257 bytes"]...))

	// The limit itself is valid.
	apply(t, fx, claims(strings.Repeat("a", principal.MaxGroupClaimValueBytes)))
}

func testControlCharacters(t *testing.T, fx GroupClaimsFixture) {
	t.Helper()

	claimsWorld(t, fx)

	for _, name := range []string{"a NUL byte", "a CR LF pair", "an empty value", "text that is not UTF-8"} {
		requireInvalid(t, fx, name, claims(hostileValues()[name]...))
	}
}

func testSourceLocal(t *testing.T, fx GroupClaimsFixture) {
	t.Helper()

	claimsWorld(t, fx)

	c := claims("eng-team")
	c.Source = principal.MembershipSourceLocal

	requireInvalid(t, fx, "the source local", c)
}

func testEmptySourceID(t *testing.T, fx GroupClaimsFixture) {
	t.Helper()

	claimsWorld(t, fx)

	c := claims("eng-team")
	c.SourceID = ""

	requireInvalid(t, fx, "an empty source ID", c)
}

func testSQLText(t *testing.T, fx GroupClaimsFixture) {
	t.Helper()

	claimsWorld(t, fx)

	res := apply(t, fx, claims("' OR 1=1 --", "x' OR '1'='1"))
	if len(res.Added) != 0 || len(res.Unmapped) != 2 {
		t.Fatalf("result = %+v, want no group and two unmapped values", res)
	}

	equalStrings(t, "groups of the person", currentGroups(t, fx.Store), []string{})
}

func testUnknownPrincipal(t *testing.T, fx GroupClaimsFixture) {
	t.Helper()

	claimsWorld(t, fx)

	c := claims("eng-team")
	c.PrincipalID = "prn-nobody"

	_, err := fx.Applier.ApplyGroupClaims(t.Context(), entry(), c)
	wantErr(t, "an unknown principal", err, principal.ErrNotFound)
}

func testDisabledPrincipal(t *testing.T, fx GroupClaimsFixture) {
	t.Helper()

	claimsWorld(t, fx)

	if err := fx.Store.DisablePrincipal(t.Context(), claimPerson, entry()); err != nil {
		t.Fatalf("disable: %v", err)
	}

	before := len(auditEntries(t, fx.Store))

	_, err := fx.Applier.ApplyGroupClaims(t.Context(), entry(), claims("eng-team"))
	wantErr(t, "a disabled principal", err, principal.ErrDisabled)
	requireUnchanged(t, fx.Store, []string{}, before)
}

func testServicePrincipal(t *testing.T, fx GroupClaimsFixture) {
	t.Helper()

	claimsWorld(t, fx)
	mustCreatePrincipal(t, fx.Store, newPrincipal("prn-service", "ci-bot", principal.KindService))

	c := claims("eng-team")
	c.PrincipalID = "prn-service"

	before := len(auditEntries(t, fx.Store))

	_, err := fx.Applier.ApplyGroupClaims(t.Context(), entry(), c)
	wantErr(t, "a service principal", err, principal.ErrNotSignInTarget)
	requireUnchanged(t, fx.Store, []string{}, before)
}

func testBreakGlassAccount(t *testing.T, fx GroupClaimsFixture) {
	t.Helper()

	claimsWorld(t, fx)

	p := newPrincipal("prn-glass", "glass", principal.KindPerson)
	p.BreakGlass = true

	mustCreatePrincipal(t, fx.Store, p)

	c := claims("eng-team")
	c.PrincipalID = "prn-glass"

	before := len(auditEntries(t, fx.Store))

	_, err := fx.Applier.ApplyGroupClaims(t.Context(), entry(), c)
	wantErr(t, "a break-glass account", err, principal.ErrNotSignInTarget)
	requireUnchanged(t, fx.Store, []string{}, before)
}

func testTempAdmin(t *testing.T, fx GroupClaimsFixture) {
	t.Helper()

	claimsWorld(t, fx)
	fx.MakeTempAdmin(t, claimPerson)

	before := len(auditEntries(t, fx.Store))

	_, err := fx.Applier.ApplyGroupClaims(t.Context(), entry(), claims("eng-team"))
	wantErr(t, "a temporary administrator", err, principal.ErrNotSignInTarget)
	requireUnchanged(t, fx.Store, []string{}, before)
}

func testGroupOfAnotherHome(t *testing.T, fx GroupClaimsFixture) {
	t.Helper()

	claimsWorld(t, fx)

	directory := newGroup("grp-dir", "directory")
	directory.Source = principal.SourceSCIM

	mustCreateGroup(t, fx.Store, directory)
	mustCreateGroup(t, fx.Store, newGroup("grp-deleted", "deleted"))

	if err := fx.Store.DeleteGroup(t.Context(), "grp-deleted", entry()); err != nil {
		t.Fatalf("delete group: %v", err)
	}

	// The fake mapper returns groups that the real mapper never returns.
	hostile := fx.WithMapper(fakeMapper{
		out: map[string][]string{"eng-team": {"grp-elsewhere", "grp-deleted", "grp-dir"}}, err: nil,
	})

	n := len(auditEntries(t, fx.Store))

	res, err := hostile.ApplyGroupClaims(t.Context(), entry(), claims("eng-team"))
	if err != nil {
		t.Fatalf("ApplyGroupClaims: %v", err)
	}

	if len(res.Added) != 0 || len(res.Refused) != 3 {
		t.Fatalf("result = %+v, want no add and three refused groups", res)
	}

	equalStrings(t, "groups of the person", currentGroups(t, fx.Store), []string{})

	got := entriesAfter(t, fx.Store, n)
	if len(got) != 3 {
		t.Fatalf("entries = %+v, want one refusal entry for each group", got)
	}

	for i := range got {
		if got[i].Action != principal.AuditActionClaimRefuse || got[i].Outcome != audit.OutcomeDenied {
			t.Fatalf("entry = %+v, want a denied refusal", got[i])
		}
	}
}

func testFailedMapper(t *testing.T, fx GroupClaimsFixture) {
	t.Helper()

	claimsWorld(t, fx)

	errMapper := namespace.ErrUnavailable
	failing := fx.WithMapper(fakeMapper{out: nil, err: errMapper})
	before := len(auditEntries(t, fx.Store))

	_, err := failing.ApplyGroupClaims(t.Context(), entry(), claims("eng-team"))
	wantErr(t, "a failed mapper", err, errMapper)
	requireUnchanged(t, fx.Store, []string{}, before)
}

func testFailedWrite(t *testing.T, fx GroupClaimsFixture) {
	t.Helper()

	claimsWorld(t, fx)

	before := len(auditEntries(t, fx.Store))

	fx.BreakMemberWrites(t)

	if _, err := fx.Applier.ApplyGroupClaims(t.Context(), entry(), claims("eng-team", "ops-team")); err == nil {
		t.Fatal("ApplyGroupClaims with a failed write returned no error")
	}

	requireUnchanged(t, fx.Store, []string{}, before)
}

func testFailedAudit(t *testing.T, fx GroupClaimsFixture) {
	t.Helper()

	claimsWorld(t, fx)

	before := len(auditEntries(t, fx.Store))

	fx.BreakAuditAppends(t)

	if _, err := fx.Applier.ApplyGroupClaims(t.Context(), entry(), claims("eng-team", "ops-team")); err == nil {
		t.Fatal("ApplyGroupClaims with a failed audit write returned no error")
	}

	equalStrings(t, "groups of the person", currentGroups(t, fx.Store), []string{})

	// The log refuses appends now, so a read of the count is all that remains.
	if got := len(auditEntries(t, fx.Store)); got != before {
		t.Fatalf("audit entries = %d, want %d", got, before)
	}
}
