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
// principal records that it refers to, and the grants and the access tokens
// that the expiry ends.
type Store interface {
	tempadmin.Store
	principal.RecordStore
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
		{name: "expiry ends the host-recovery tokens", test: testExpireEndsTokens},
		{
			name: "expiry after a manual disable still ends the grants and the tokens",
			test: testExpireAfterManualDisable,
		},
		{name: "read by subject returns the new record after a recreate", test: testBySubjectAfterRecreate},
		{name: "a delete of the principal removes its record", test: testDeleteRemovesRecord},
		{name: "a new person with a reused subject has no record", test: testReusedSubjectNoRecord},
		{name: "each write appends its audit entry in @server", test: testAuditEntries},
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

// tokenLifetimeHours is the lifetime in hours of the tokens of the suite.
const tokenLifetimeHours = 48

// entry returns a valid audit entry of the kind change in the scope
// @server, with the object object.
func entry(object string) audit.Entry {
	return audit.Entry{
		FenceVersion: 0,
		At:           at(minuteBase),
		Namespace:    audit.ServerScope,
		Subject:      createdBy,
		IDPSubject:   "",
		TokenID:      "",
		Action:       tempadmin.Action,
		Object:       object,
		Version:      "",
		ValuePath:    "",
		ValueCount:   nil,
		Changes:      nil,
		SourceIP:     "",
		UserAgent:    "",
		Channel:      audit.ChannelAPI,
		Kind:         audit.KindChange,
		Outcome:      audit.OutcomeSuccess,
		RequestID:    "",
		ObjectID:     "",
		PrevHash:     "",
		Hash:         "",
		Sequence:     0,
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
		CreatedAt:   at(created),
		ExpiresAt:   at(created).Add(tempadmin.Lifetime),
		PrincipalID: id,
		Subject:     subject,
		CreatedBy:   createdBy,
		Reason:      reason,
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
		CreatedAt:   at(minuteLater),
		ExpiresAt:   at(minuteLater).Add(tempadmin.Lifetime),
		PrincipalID: alicePrincipalID,
		Subject:     "alice",
		CreatedBy:   createdBy,
		Reason:      reason,
	}

	err := s.CreateTempAdmin(t.Context(), dup, entry(tempadmin.Ref(alicePrincipalID)))
	wantErr(t, "duplicate record", err, tempadmin.ErrAlreadyExists)
}

func testMissingPrincipal(t *testing.T, s Store) {
	t.Helper()

	r := tempadmin.TempAdmin{
		CreatedAt:   at(0),
		ExpiresAt:   at(0).Add(tempadmin.Lifetime),
		PrincipalID: "prn-ghost",
		Subject:     "ghost",
		CreatedBy:   createdBy,
		Reason:      reason,
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
	mustPrincipal(t, s, "prn-bob", "bob")
	mustRecord(t, s, "prn-bob", "bob", minuteLatest)
	mustRecord(t, s, alicePrincipalID, "alice", 0)

	all, err := s.ListTempAdmins(t.Context())
	if err != nil {
		t.Fatalf("list temporary administrators: %v", err)
	}

	if len(all) != 2 || all[0].PrincipalID != alicePrincipalID || all[1].PrincipalID != "prn-bob" {
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

	checkHostRevoked(t, s)
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

// checkHostRevoked fails unless the host-recovery token is revoked and the
// token of another name still works.
func checkHostRevoked(t *testing.T, s Store) {
	t.Helper()

	host, err := s.GetToken(t.Context(), "tok-host")
	if err != nil {
		t.Fatalf("get host token after expiry: %v", err)
	}

	if host.RevokedAt.IsZero() {
		t.Fatal("host token after expiry is not revoked")
	}

	other, err := s.GetToken(t.Context(), "tok-laptop")
	if err != nil {
		t.Fatalf("get other token after expiry: %v", err)
	}

	if !other.RevokedAt.IsZero() {
		t.Fatal("token of another name is revoked, want only the host-recovery tokens ended")
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
	checkHostRevoked(t, s)
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
