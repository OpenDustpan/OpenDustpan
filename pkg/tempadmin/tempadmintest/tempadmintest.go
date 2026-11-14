// Package tempadmintest is the conformance suite for tempadmin.Store.
// Every built-in store runs it:
//
//	func TestTempAdminConformance(t *testing.T) {
//		t.Parallel()
//		tempadmintest.Run(t, func(t *testing.T) tempadmintest.Store { return newMyStore(t) })
//	}
package tempadmintest

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/tempadmin"
)

// Store is the store under test: the temporary administrator port with the
// principal records that it refers to, and the grants, the group
// memberships and the access tokens that the expiry ends.
type Store interface {
	tempadmin.Store
	principal.RecordStore
	principal.GroupStore
	principal.MemberStore
	access.GrantStore
	accesstoken.Store
}

// Factory returns a fresh, empty store for one subtest.
type Factory func(t *testing.T) Store

// Run executes the conformance suite against stores produced by newStore.
func Run(t *testing.T, newStore Factory) {
	t.Helper()

	cases := []struct {
		test func(t *testing.T, s Store)
		name string
	}{
		{name: "create and read round trip", test: testRoundTrip},
		{name: "read by subject", test: testBySubject},
		{name: "duplicate record is ErrAlreadyExists", test: testDuplicate},
		{name: "record of a missing principal is principal.ErrNotFound", test: testMissingPrincipal},
		{name: "missing record is ErrNotFound", test: testMissing},
		{name: "list orders by expiry", test: testList},
		{name: "expiry disables the principal and keeps the record", test: testExpire},
		{name: "expiry of a missing record is ErrNotFound", test: testExpireMissing},
		{name: "a second expiry is ErrExpired and writes nothing", test: testExpireTwice},
		{name: "expiry ends the site-administrator grant", test: testExpireEndsGrant},
		{name: "expiry ends every grant and group membership", test: testExpireEndsAllAccess},
		{name: "expiry ends every live token, whatever its name", test: testExpireEndsTokens},
		{
			name: "expiry after a manual disable still ends the grants and the tokens",
			test: testExpireAfterManualDisable,
		},
		{name: "read by subject returns the new record after a recreate", test: testBySubjectAfterRecreate},
		{name: "a delete of the principal removes its record", test: testDeleteRemovesRecord},
		{name: "a new person with a reused subject has no record", test: testReusedSubjectNoRecord},
		{name: "each write appends its audit entry in @server", test: testAuditEntries},
		{name: "awaiting review lists only ended records with no mark", test: testAwaitingReview},
		{name: "a review mark keeps the first time and writes no entry", test: testReviewMarkOnce},
		{name: "a review mark of a missing or an active record fails", test: testReviewMarkRefused},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.test(t, newStore(t))
		})
	}
}

// at returns a fixed time minute minutes after a base, in UTC to the
// millisecond, so records stay comparable.
func at(minute int) time.Time {
	return time.Date(2026, time.October, 15, 12, minute, 0, 0, time.UTC)
}

// Minutes of the fixed times of the suite, after a base hour.
const (
	minuteBase   = 0
	minuteLater  = 5
	minuteLatest = 30
)

// wantNewEntries is the count of audit entries that the create and the
// expiry of one record append.
const wantNewEntries = 2

// createdBy is the creator of the records of the suite.
const createdBy = "host"

// reason is the reason of the records of the suite.
const reason = "every administrator lost access"

// alicePrincipalID is the principal ID of alice in the suite.
const alicePrincipalID = "prn-alice"

// bobPrincipalID is the principal ID of bob in the suite.
const bobPrincipalID = "prn-bob"

// groupPageLimit is the page size of a group listing of the suite.
const groupPageLimit = 10

// tokenLifetimeHours is the lifetime in hours of the tokens of the suite.
const tokenLifetimeHours = 48

// entry returns a valid audit entry of the kind change in the scope
// @server, with the object object.
func entry(object string) audit.Entry {
	return audit.Entry{
		FenceVersion:  0,
		At:            at(minuteBase),
		Namespace:     audit.ServerScope,
		Subject:       createdBy,
		IDPSubject:    "",
		TokenID:       "",
		Action:        tempadmin.Action,
		Object:        object,
		Version:       "",
		ValuePath:     "",
		ValueCount:    nil,
		Changes:       nil,
		SourceIP:      "",
		UserAgent:     "",
		Channel:       audit.ChannelAPI,
		Kind:          audit.KindChange,
		Outcome:       audit.OutcomeSuccess,
		RequestID:     "",
		ObjectID:      "",
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		Reason:        "",
		PrincipalKind: "",
		JobID:         "",
	}
}

// mustPrincipal creates the person id with the subject subject.
func mustPrincipal(t *testing.T, s Store, id, subject string) {
	t.Helper()

	p := principal.Principal{
		BreakGlass:  false,
		CreatedAt:   at(0),
		UpdatedAt:   at(0),
		DisabledAt:  time.Time{},
		ID:          id,
		Subject:     subject,
		DisplayName: "Name of " + subject,
		Kind:        principal.KindPerson,
		Source:      principal.SourceLocal,
		Namespace:   "",
		GroupCount:  0,
	}

	e := entry(principal.Ref(id))

	if err := s.CreatePrincipal(t.Context(), p, e); err != nil {
		t.Fatalf("create principal %q: %v", id, err)
	}
}

// mustRecord creates the temporary administrator record of id for subject
// at minute created.
func mustRecord(t *testing.T, s Store, id, subject string, created int) tempadmin.TempAdmin {
	t.Helper()

	r := tempadmin.TempAdmin{
		CreatedAt:      at(created),
		ExpiresAt:      at(created).Add(tempadmin.Lifetime),
		EndedAt:        time.Time{},
		ReviewOpenedAt: time.Time{},
		PrincipalID:    id,
		Subject:        subject,
		CreatedBy:      createdBy,
		Reason:         reason,
	}

	if err := s.CreateTempAdmin(t.Context(), r, entry(tempadmin.Ref(id))); err != nil {
		t.Fatalf("create temporary administrator for %q: %v", subject, err)
	}

	return r
}

// wantErr fails the test unless err wraps want.
func wantErr(t *testing.T, what string, err, want error) {
	t.Helper()

	if !errors.Is(err, want) {
		t.Fatalf("%s: err = %v, want %v", what, err, want)
	}
}

// equalRecord fails the test unless got holds the fields of want.
func equalRecord(t *testing.T, got, want tempadmin.TempAdmin) {
	t.Helper()

	same := got.CreatedAt.Equal(want.CreatedAt) && got.ExpiresAt.Equal(want.ExpiresAt) &&
		got.PrincipalID == want.PrincipalID && got.Subject == want.Subject && got.CreatedBy == want.CreatedBy &&
		got.Reason == want.Reason

	if !same {
		t.Fatalf("record = %+v, want %+v", got, want)
	}
}

func testRoundTrip(t *testing.T, s Store) {
	t.Helper()

	mustPrincipal(t, s, alicePrincipalID, "alice")
	want := mustRecord(t, s, alicePrincipalID, "alice", 0)

	got, err := s.GetTempAdmin(t.Context(), alicePrincipalID)
	if err != nil {
		t.Fatalf("get temporary administrator: %v", err)
	}

	equalRecord(t, got, want)

	if !got.EndedAt.IsZero() {
		t.Fatalf("new record ended at %s, want no end", got.EndedAt)
	}
}

func testBySubject(t *testing.T, s Store) {
	t.Helper()

	mustPrincipal(t, s, alicePrincipalID, "alice")
	want := mustRecord(t, s, alicePrincipalID, "alice", 0)

	got, err := s.GetTempAdminBySubject(t.Context(), "alice")
	if err != nil {
		t.Fatalf("get temporary administrator by subject: %v", err)
	}

	equalRecord(t, got, want)
}

func testDuplicate(t *testing.T, s Store) {
	t.Helper()

	mustPrincipal(t, s, alicePrincipalID, "alice")
	mustRecord(t, s, alicePrincipalID, "alice", 0)

	dup := tempadmin.TempAdmin{
		CreatedAt:      at(minuteLater),
		ExpiresAt:      at(minuteLater).Add(tempadmin.Lifetime),
		EndedAt:        time.Time{},
		ReviewOpenedAt: time.Time{},
		PrincipalID:    alicePrincipalID,
		Subject:        "alice",
		CreatedBy:      createdBy,
		Reason:         reason,
	}

	err := s.CreateTempAdmin(t.Context(), dup, entry(tempadmin.Ref(alicePrincipalID)))
	wantErr(t, "duplicate record", err, tempadmin.ErrAlreadyExists)
}

func testMissingPrincipal(t *testing.T, s Store) {
	t.Helper()

	r := tempadmin.TempAdmin{
		CreatedAt:      at(0),
		ExpiresAt:      at(0).Add(tempadmin.Lifetime),
		EndedAt:        time.Time{},
		ReviewOpenedAt: time.Time{},
		PrincipalID:    "prn-ghost",
		Subject:        "ghost",
		CreatedBy:      createdBy,
		Reason:         reason,
	}

	err := s.CreateTempAdmin(t.Context(), r, entry(tempadmin.Ref("prn-ghost")))
	wantErr(t, "record of a missing principal", err, principal.ErrNotFound)
}

func testMissing(t *testing.T, s Store) {
	t.Helper()

	if _, err := s.GetTempAdmin(t.Context(), "prn-ghost"); !errors.Is(err, tempadmin.ErrNotFound) {
		t.Fatalf("get of a missing record = %v, want ErrNotFound", err)
	}

	if _, err := s.GetTempAdminBySubject(t.Context(), "ghost"); !errors.Is(err, tempadmin.ErrNotFound) {
		t.Fatalf("get by subject of a missing record = %v, want ErrNotFound", err)
	}
}

func testList(t *testing.T, s Store) {
	t.Helper()

	mustPrincipal(t, s, alicePrincipalID, "alice")
	mustPrincipal(t, s, bobPrincipalID, "bob")
	mustRecord(t, s, bobPrincipalID, "bob", minuteLatest)
	mustRecord(t, s, alicePrincipalID, "alice", 0)

	all, err := s.ListTempAdmins(t.Context())
	if err != nil {
		t.Fatalf("list temporary administrators: %v", err)
	}

	if len(all) != 2 || all[0].PrincipalID != alicePrincipalID || all[1].PrincipalID != bobPrincipalID {
		t.Fatalf("list = %v, want alice then bob by expiry", all)
	}
}

func testExpire(t *testing.T, s Store) {
	t.Helper()

	mustPrincipal(t, s, alicePrincipalID, "alice")
	want := mustRecord(t, s, alicePrincipalID, "alice", 0)

	if err := s.ExpireTempAdmin(t.Context(), alicePrincipalID, entry(tempadmin.Ref(alicePrincipalID))); err != nil {
		t.Fatalf("expire temporary administrator: %v", err)
	}

	got, err := s.GetTempAdmin(t.Context(), alicePrincipalID)
	if err != nil {
		t.Fatalf("get after expiry = %v, want the tombstone", err)
	}

	equalRecord(t, got, want)

	if !got.EndedAt.Equal(at(minuteBase)) {
		t.Fatalf("record ended at %s, want the time of the expiry entry %s", got.EndedAt, at(minuteBase))
	}

	if live, liveErr := s.GetTempAdmin(t.Context(), alicePrincipalID); liveErr != nil || live.EndedAt.IsZero() {
		t.Fatalf("record after expiry = %+v, %v; want an ended record", live, liveErr)
	}

	p, err := s.GetPrincipal(t.Context(), alicePrincipalID)
	if err != nil {
		t.Fatalf("get principal after expiry: %v", err)
	}

	if p.DisabledAt.IsZero() {
		t.Fatal("principal after expiry is not disabled")
	}
}

func testExpireMissing(t *testing.T, s Store) {
	t.Helper()

	err := s.ExpireTempAdmin(t.Context(), "prn-ghost", entry(tempadmin.Ref("prn-ghost")))
	wantErr(t, "expiry of a missing record", err, tempadmin.ErrNotFound)
}

func testExpireTwice(t *testing.T, s Store) {
	t.Helper()

	mustPrincipal(t, s, alicePrincipalID, "alice")
	mustRecord(t, s, alicePrincipalID, "alice", 0)

	if err := s.ExpireTempAdmin(t.Context(), alicePrincipalID, entry(tempadmin.Ref(alicePrincipalID))); err != nil {
		t.Fatalf("expire temporary administrator: %v", err)
	}

	err := s.ExpireTempAdmin(t.Context(), alicePrincipalID, entry(tempadmin.Ref(alicePrincipalID)))
	wantErr(t, "second expiry", err, tempadmin.ErrExpired)

	if _, err := s.GetTempAdmin(t.Context(), alicePrincipalID); err != nil {
		t.Fatalf("get after the second expiry = %v, want the tombstone", err)
	}
}

// mustGrant gives the principal id the site-administrator role on the
// installation, as the host command does.
func mustGrant(t *testing.T, s Store, id, principalID string) {
	t.Helper()

	g := access.Grant{
		CreatedAt: at(0),
		Condition: nil,
		Object:    identity.ServerObject(),
		ID:        id,
		Role:      access.RoleSiteAdministrator,
		CreatedBy: tempadmin.HostSubject,
		Subject:   access.Subject{Kind: access.SubjectPrincipal, ID: principalID},
	}

	e := entry(access.GrantRef(g))

	if err := s.CreateGrant(t.Context(), g, namespace.Fence{Namespace: "", FailoverVersion: 0}, e, nil); err != nil {
		t.Fatalf("grant site-administrator to %q: %v", principalID, err)
	}
}

// mustToken issues the token id of the principal alice with the name name.
func mustToken(t *testing.T, s Store, id, name string) {
	t.Helper()

	tok := accesstoken.Token{
		CreatedAt:   at(0),
		ExpiresAt:   at(0).Add(tokenLifetimeHours * time.Hour),
		LastUsedAt:  time.Time{},
		RevokedAt:   time.Time{},
		DisabledAt:  time.Time{},
		ID:          id,
		PrincipalID: alicePrincipalID,
		Name:        name,
		Kind:        accesstoken.KindPersonal,
		ReplacedBy:  "",
		CreatedBy:   tempadmin.HostSubject,
	}

	d := sha256.Sum256([]byte("secret of " + id))

	if err := s.CreateToken(t.Context(), tok, d, entry(accesstoken.Ref(id))); err != nil {
		t.Fatalf("create token %q: %v", id, err)
	}
}

func testExpireEndsGrant(t *testing.T, s Store) {
	t.Helper()

	mustPrincipal(t, s, alicePrincipalID, "alice")
	mustRecord(t, s, alicePrincipalID, "alice", 0)
	mustGrant(t, s, "grt-alice", alicePrincipalID)

	if err := s.ExpireTempAdmin(t.Context(), alicePrincipalID, entry(tempadmin.Ref(alicePrincipalID))); err != nil {
		t.Fatalf("expire temporary administrator: %v", err)
	}

	grants, err := s.GrantsOn(
		t.Context(),
		[]access.Subject{{Kind: access.SubjectPrincipal, ID: alicePrincipalID}},
		[]identity.Object{identity.ServerObject()},
	)
	if err != nil {
		t.Fatalf("grants after expiry: %v", err)
	}

	if len(grants) != 0 {
		t.Fatalf("grants after expiry = %+v, want none live", grants)
	}
}

func testExpireEndsTokens(t *testing.T, s Store) {
	t.Helper()

	mustPrincipal(t, s, alicePrincipalID, "alice")
	mustRecord(t, s, alicePrincipalID, "alice", 0)
	mustToken(t, s, "tok-host", tempadmin.HostTokenName)
	mustToken(t, s, "tok-laptop", "laptop")

	if err := s.ExpireTempAdmin(t.Context(), alicePrincipalID, entry(tempadmin.Ref(alicePrincipalID))); err != nil {
		t.Fatalf("expire temporary administrator: %v", err)
	}

	checkTokensRevoked(t, s)
}

// mustGroupMember makes the local group id and adds the principal
// principalID to it.
func mustGroupMember(t *testing.T, s Store, id, principalID string) {
	t.Helper()

	g := principal.Group{
		CreatedAt: at(0), UpdatedAt: at(0), ID: id, Name: "Group " + id, Description: "",
		Source: principal.SourceLocal, MemberCount: 0, ClosedToIdentityProvider: false,
	}

	if err := s.CreateGroup(t.Context(), g, entry(principal.GroupRef(id))); err != nil {
		t.Fatalf("create group %q: %v", id, err)
	}

	m := principal.Membership{
		AddedAt: at(0), SuspendedAt: time.Time{}, GroupID: id, PrincipalID: principalID,
		Source: "", SourceID: "", State: "", Pinned: false,
	}

	if err := s.AddMember(t.Context(), m, entry(principal.GroupRef(id)), nil); err != nil {
		t.Fatalf("add %q to group %q: %v", principalID, id, err)
	}
}

// mustRoleGrant gives the principal principalID a grant of role on the
// installation.
func mustRoleGrant(t *testing.T, s Store, id, principalID, role string) {
	t.Helper()

	g := access.Grant{
		CreatedAt: at(0), Condition: nil, Object: identity.ServerObject(), ID: id, Role: role,
		CreatedBy: tempadmin.HostSubject, Subject: access.Subject{Kind: access.SubjectPrincipal, ID: principalID},
	}

	fence := namespace.Fence{Namespace: "", FailoverVersion: 0}

	if err := s.CreateGrant(t.Context(), g, fence, entry(access.GrantRef(g)), nil); err != nil {
		t.Fatalf("grant %s to %q: %v", role, principalID, err)
	}
}

// testExpireEndsAllAccess proves that the expiry ends every grant that the
// principal holds, not only the site-administrator grant, and every group
// membership of the principal, so no access stays after the window.
func testExpireEndsAllAccess(t *testing.T, s Store) {
	t.Helper()

	mustPrincipal(t, s, alicePrincipalID, "alice")
	mustRecord(t, s, alicePrincipalID, "alice", 0)
	mustGrant(t, s, "grt-alice", alicePrincipalID)
	mustRoleGrant(t, s, "grt-alice-audit", alicePrincipalID, access.RoleAuditor)
	mustGroupMember(t, s, "grp-ops", alicePrincipalID)

	if err := s.ExpireTempAdmin(t.Context(), alicePrincipalID, entry(tempadmin.Ref(alicePrincipalID))); err != nil {
		t.Fatalf("expire temporary administrator: %v", err)
	}

	checkGrantsGone(t, s, alicePrincipalID)

	groups, total, err := s.ListGroupsOf(t.Context(), alicePrincipalID, principal.Query{
		After: "", Low: "", High: "", Limit: groupPageLimit,
	})
	if err != nil {
		t.Fatalf("groups after expiry: %v", err)
	}

	if total != 0 || len(groups) != 0 {
		t.Fatalf("groups after expiry = %+v (%d), want none", groups, total)
	}
}

// disableBySweep disables the principal id through the inactivity sweep,
// before its temporary administrator record exists, so the sweep does not
// exempt it.
func disableBySweep(t *testing.T, s Store, id string) {
	t.Helper()

	sweepEntry := func(at time.Time, object string) audit.Entry {
		e := entry(object)
		e.At = at

		return e
	}

	_, err := s.SweepInactive(
		t.Context(),
		accesstoken.Sweep{Inactivity: time.Hour, Tokens: false, Accounts: true},
		sweepEntry,
	)
	if err != nil {
		t.Fatalf("sweep to disable %q: %v", id, err)
	}

	p, err := s.GetPrincipal(t.Context(), id)
	if err != nil {
		t.Fatalf("get principal after the sweep: %v", err)
	}

	if p.DisabledAt.IsZero() {
		t.Fatalf("principal %q after the sweep is not disabled", id)
	}
}

// checkGrantsGone fails unless no live grant of the principal id remains.
func checkGrantsGone(t *testing.T, s Store, id string) {
	t.Helper()

	grants, err := s.GrantsOn(
		t.Context(),
		[]access.Subject{{Kind: access.SubjectPrincipal, ID: id}},
		[]identity.Object{identity.ServerObject()},
	)
	if err != nil {
		t.Fatalf("grants after expiry: %v", err)
	}

	if len(grants) != 0 {
		t.Fatalf("grants after expiry = %+v, want none live", grants)
	}
}

// checkTokensRevoked fails unless the host-recovery token and the token
// of another name are both revoked.
func checkTokensRevoked(t *testing.T, s Store) {
	t.Helper()

	for _, id := range []string{"tok-host", "tok-laptop"} {
		tok, err := s.GetToken(t.Context(), id)
		if err != nil {
			t.Fatalf("get token %q after expiry: %v", id, err)
		}

		if tok.RevokedAt.IsZero() {
			t.Fatalf("token %q after expiry is not revoked, want every live token ended", id)
		}
	}
}

func testExpireAfterManualDisable(t *testing.T, s Store) {
	t.Helper()

	mustPrincipal(t, s, alicePrincipalID, "alice")
	disableBySweep(t, s, alicePrincipalID)
	mustRecord(t, s, alicePrincipalID, "alice", 0)
	mustGrant(t, s, "grt-alice", alicePrincipalID)
	mustToken(t, s, "tok-host", tempadmin.HostTokenName)
	mustToken(t, s, "tok-laptop", "laptop")

	if err := s.ExpireTempAdmin(t.Context(), alicePrincipalID, entry(tempadmin.Ref(alicePrincipalID))); err != nil {
		t.Fatalf("expire after a manual disable: %v, want no error", err)
	}

	checkGrantsGone(t, s, alicePrincipalID)
	checkTokensRevoked(t, s)
}

func testBySubjectAfterRecreate(t *testing.T, s Store) {
	t.Helper()

	mustPrincipal(t, s, alicePrincipalID, "alice")
	mustRecord(t, s, alicePrincipalID, "alice", minuteBase)

	if err := s.ExpireTempAdmin(t.Context(), alicePrincipalID, entry(tempadmin.Ref(alicePrincipalID))); err != nil {
		t.Fatalf("expire temporary administrator: %v", err)
	}

	if err := s.DeletePrincipal(t.Context(), alicePrincipalID, entry(principal.Ref(alicePrincipalID))); err != nil {
		t.Fatalf("delete principal: %v", err)
	}

	const newID = "prn-alice-2"

	mustPrincipal(t, s, newID, "alice")
	mustRecord(t, s, newID, "alice", minuteLater)

	got, err := s.GetTempAdminBySubject(t.Context(), "alice")
	if err != nil {
		t.Fatalf("get temporary administrator by subject after a recreate: %v", err)
	}

	if got.PrincipalID != newID {
		t.Fatalf("record after a recreate = %+v, want the new principal %q", got, newID)
	}

	if !got.ActiveAt(at(minuteLater)) {
		t.Fatalf("record after a recreate is not active at %s, want a sign-in to work", at(minuteLater))
	}
}

func testDeleteRemovesRecord(t *testing.T, s Store) {
	t.Helper()

	mustPrincipal(t, s, alicePrincipalID, "alice")
	mustRecord(t, s, alicePrincipalID, "alice", minuteBase)

	if err := s.DeletePrincipal(t.Context(), alicePrincipalID, entry(principal.Ref(alicePrincipalID))); err != nil {
		t.Fatalf("delete principal: %v", err)
	}

	if _, err := s.GetTempAdmin(t.Context(), alicePrincipalID); !errors.Is(err, tempadmin.ErrNotFound) {
		t.Fatalf("record after the delete: err = %v, want ErrNotFound", err)
	}

	list, err := s.ListTempAdmins(t.Context())
	if err != nil || len(list) != 0 {
		t.Fatalf("records after the delete = %+v, %v; want none", list, err)
	}
}

// testReusedSubjectNoRecord proves that a new person with the subject of a
// deleted temporary administrator reads no record, so it is not locked out
// by the tombstone of the old one.
func testReusedSubjectNoRecord(t *testing.T, s Store) {
	t.Helper()

	mustPrincipal(t, s, alicePrincipalID, "alice")
	mustRecord(t, s, alicePrincipalID, "alice", minuteBase)

	if err := s.ExpireTempAdmin(t.Context(), alicePrincipalID, entry(tempadmin.Ref(alicePrincipalID))); err != nil {
		t.Fatalf("expire temporary administrator: %v", err)
	}

	if err := s.DeletePrincipal(t.Context(), alicePrincipalID, entry(principal.Ref(alicePrincipalID))); err != nil {
		t.Fatalf("delete principal: %v", err)
	}

	mustPrincipal(t, s, "prn-alice-2", "alice")

	if got, err := s.GetTempAdminBySubject(t.Context(), "alice"); !errors.Is(err, tempadmin.ErrNotFound) {
		t.Fatalf("record of the new alice = %+v, %v; want ErrNotFound", got, err)
	}
}

// auditLister reads the audit log of a store that keeps one.
type auditLister interface {
	List(ctx context.Context, ns namespace.Name, page audit.Page) (audit.Listing, error)
}

func testAuditEntries(t *testing.T, s Store) {
	t.Helper()

	lister, ok := s.(auditLister)
	if !ok {
		t.Skip("the store keeps no audit log to read")
	}

	mustPrincipal(t, s, alicePrincipalID, "alice")

	before, err := listServerObjects(t.Context(), lister)
	if err != nil {
		t.Fatalf("list audit entries: %v", err)
	}

	mustRecord(t, s, alicePrincipalID, "alice", 0)

	if err := s.ExpireTempAdmin(t.Context(), alicePrincipalID, entry(tempadmin.Ref(alicePrincipalID))); err != nil {
		t.Fatalf("expire temporary administrator: %v", err)
	}

	after, afterErr := listServerObjects(t.Context(), lister)
	if afterErr != nil {
		t.Fatalf("list audit entries: %v", afterErr)
	}

	if len(after)-len(before) != wantNewEntries {
		t.Fatalf("audit entries grew by %d, want %d for the create and the expiry",
			len(after)-len(before), wantNewEntries)
	}

	for _, object := range after[:2] {
		if object != tempadmin.Ref(alicePrincipalID) {
			t.Fatalf("audit object = %q, want %q", object, tempadmin.Ref(alicePrincipalID))
		}
	}
}

// listServerObjects returns the objects of the entries of @server, newest
// first, so a case counts the entries that its writes append.
func listServerObjects(ctx context.Context, lister auditLister) ([]string, error) {
	listing, err := lister.List(ctx, audit.ServerScope, audit.Page{Number: 1, Size: audit.MaxPageSize})
	if err != nil {
		return nil, err
	}

	out := make([]string, 0, len(listing.Entries))

	for i := range listing.Entries {
		out = append(out, listing.Entries[i].Object)
	}

	return out, nil
}

// expireRecord ends the record of id.
func expireRecord(t *testing.T, s Store, id string) {
	t.Helper()

	if err := s.ExpireTempAdmin(t.Context(), id, entry(tempadmin.Ref(id))); err != nil {
		t.Fatalf("expire temporary administrator %q: %v", id, err)
	}
}

// awaiting returns the principal IDs of the records that await a review.
func awaiting(t *testing.T, s Store) []string {
	t.Helper()

	all, err := s.ListTempAdminsAwaitingReview(t.Context())
	if err != nil {
		t.Fatalf("list records awaiting review: %v", err)
	}

	ids := make([]string, 0, len(all))
	for i := range all {
		ids = append(ids, all[i].PrincipalID)
	}

	return ids
}

func testAwaitingReview(t *testing.T, s Store) {
	t.Helper()

	if got := awaiting(t, s); len(got) != 0 {
		t.Fatalf("awaiting review of an empty store = %v, want none", got)
	}

	mustPrincipal(t, s, alicePrincipalID, "alice")
	mustPrincipal(t, s, bobPrincipalID, "bob")
	mustPrincipal(t, s, "prn-carol", "carol")
	mustRecord(t, s, alicePrincipalID, "alice", 0)
	mustRecord(t, s, bobPrincipalID, "bob", minuteLatest)
	mustRecord(t, s, "prn-carol", "carol", minuteLater)

	if got := awaiting(t, s); len(got) != 0 {
		t.Fatalf("awaiting review before any expiry = %v, want none: a record that works has no review", got)
	}

	expireRecord(t, s, bobPrincipalID)
	expireRecord(t, s, alicePrincipalID)

	got := awaiting(t, s)
	if len(got) != 2 || got[0] != alicePrincipalID || got[1] != bobPrincipalID {
		t.Fatalf("awaiting review = %v, want alice then bob by expiry, and not carol, who works", got)
	}

	if err := s.MarkTempAdminReviewOpened(t.Context(), alicePrincipalID, at(minuteLater)); err != nil {
		t.Fatalf("mark review opened: %v", err)
	}

	got = awaiting(t, s)
	if len(got) != 1 || got[0] != bobPrincipalID {
		t.Fatalf("awaiting review after the mark of alice = %v, want bob only", got)
	}
}

func testReviewMarkOnce(t *testing.T, s Store) {
	t.Helper()

	mustPrincipal(t, s, alicePrincipalID, "alice")
	mustRecord(t, s, alicePrincipalID, "alice", 0)
	expireRecord(t, s, alicePrincipalID)

	before := auditCount(t, s)

	if err := s.MarkTempAdminReviewOpened(t.Context(), alicePrincipalID, at(minuteLater)); err != nil {
		t.Fatalf("mark review opened: %v", err)
	}

	if err := s.MarkTempAdminReviewOpened(t.Context(), alicePrincipalID, at(minuteLatest)); err != nil {
		t.Fatalf("second mark of the review: %v", err)
	}

	got, err := s.GetTempAdmin(t.Context(), alicePrincipalID)
	if err != nil {
		t.Fatalf("get temporary administrator: %v", err)
	}

	if !got.ReviewOpenedAt.Equal(at(minuteLater)) {
		t.Fatalf("review opened at %s, want the first mark %s", got.ReviewOpenedAt, at(minuteLater))
	}

	if after := auditCount(t, s); after != before {
		t.Fatalf("audit entries after the marks = %d, want %d: a mark writes no entry", after, before)
	}

	if all, listErr := s.ListTempAdmins(t.Context()); listErr != nil || len(all) != 1 ||
		!all[0].ReviewOpenedAt.Equal(at(minuteLater)) {
		t.Fatalf("list = %+v, %v; want the mark in the listed record", all, listErr)
	}
}

func testReviewMarkRefused(t *testing.T, s Store) {
	t.Helper()

	err := s.MarkTempAdminReviewOpened(t.Context(), "prn-ghost", at(minuteLater))
	wantErr(t, "mark of a missing record", err, tempadmin.ErrNotFound)

	mustPrincipal(t, s, alicePrincipalID, "alice")
	mustRecord(t, s, alicePrincipalID, "alice", 0)

	err = s.MarkTempAdminReviewOpened(t.Context(), alicePrincipalID, at(minuteLater))
	wantErr(t, "mark of a record that works", err, tempadmin.ErrInvalid)

	got, getErr := s.GetTempAdmin(t.Context(), alicePrincipalID)
	if getErr != nil || !got.ReviewOpenedAt.IsZero() {
		t.Fatalf("record after the refused mark = %+v, %v; want no mark", got, getErr)
	}
}

// auditCount returns the count of entries in @server, or skips the test
// when the store keeps no audit log to read.
func auditCount(t *testing.T, s Store) int {
	t.Helper()

	lister, ok := s.(auditLister)
	if !ok {
		t.Skip("the store keeps no audit log to read")
	}

	objects, err := listServerObjects(t.Context(), lister)
	if err != nil {
		t.Fatalf("list audit entries: %v", err)
	}

	return len(objects)
}
