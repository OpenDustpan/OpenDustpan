// Package firstadmintest is the conformance suite for firstadmin.Store. Every
// built-in store runs it:
//
//	func TestFirstAdministratorConformance(t *testing.T) {
//		t.Parallel()
//		firstadmintest.RunFirstAdministrator(t, func(t *testing.T) firstadmintest.Harness { return newHarness(t) })
//	}
package firstadmintest

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/firstadmin"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/tempadmin"
)

// Store is the store under test: the port of the first administrator, with
// the principal records, the grants, the temporary administrator records and
// the audit log that the cases read and write.
type Store interface {
	firstadmin.Store
	principal.RecordStore
	access.GrantStore
	tempadmin.Store
	List(ctx context.Context, ns namespace.Name, page audit.Page) (audit.Listing, error)
}

// CodeRow is a row of the code table, as a hostile case writes it with direct
// SQL.
type CodeRow struct {
	PrincipalID string
	Digest      string
	CreatedBy   string
	Reason      string
	CreatedAtMs int64
	ExpiresAtMs int64
	ID          int
}

// MarkerRow is a row of the marker table, as a hostile case writes it with
// direct SQL.
type MarkerRow struct {
	PrincipalID string
	GrantID     string
	SessionID   string
	CreatedBy   string
	MadeAtMs    int64
	ID          int
}

// Harness is a fresh, empty store and the direct access that the hostile cases
// need. The adapters give each function in dialect SQL.
type Harness struct {
	Store Store
	// BreakAuditAppends makes each later append of an audit entry fail.
	BreakAuditAppends func(ctx context.Context) error
	// SetSubject changes the subject of a principal with no validation.
	SetSubject func(ctx context.Context, principalID, subject string) error
	// InsertCode inserts a row in the code table with no validation of the
	// service. The schema alone can refuse it.
	InsertCode func(ctx context.Context, r CodeRow) error
	// InsertMarker inserts a row in the marker table with no validation of
	// the service. The schema alone can refuse it.
	InsertMarker func(ctx context.Context, r MarkerRow) error
}

// Factory returns a fresh harness for one subtest.
type Factory func(t *testing.T) Harness

// The minutes of the fixed times of the suite, after a base hour.
const (
	minuteBase   = 0
	minuteOffer  = 1
	minuteRedeem = 2

	secondRow = 2
)

const (
	aliceSubject = "alice"
	createdBy    = "host"
	reason       = "the operator lost the admin session"
	alice        = "prn-alice"
	bob          = "prn-bob"
	aliceGrantID = "grt-alice"
	preGrantID   = "grt-pre"
	sessionID    = "ses-alice"
	window       = 5 * time.Minute
	offerAction  = "server:offer-first-administrator"
	redeemAction = "caller:redeem-first-administrator"
	otherAction  = "server:manage-grants"
)

// digestA returns the digest of one code of the suite.
func digestA() string {
	return firstadmin.Code("A" + strings.Repeat("a", firstadmin.CodeLength-1)).Digest()
}

// digestB returns the digest of another code of the suite.
func digestB() string {
	return firstadmin.Code("B" + strings.Repeat("b", firstadmin.CodeLength-1)).Digest()
}

// RunFirstAdministrator runs the conformance suite against stores that newHarness
// produces.
func RunFirstAdministrator(t *testing.T, newHarness Factory) {
	t.Helper()

	cases := []struct {
		test func(t *testing.T, h Harness)
		name string
	}{
		{name: "TestRedeemOneTransaction", test: testRedeemOneTransaction},
		{name: "TestRedeemRollsBackOnAuditFailure", test: testRedeemRollsBackOnAuditFailure},
		{name: "TestOfferRefusedAfterMarker", test: testOfferRefusedAfterMarker},
		{name: "TestOfferReplacesCode", test: testOfferReplacesCode},
		{name: "TestRedeemWrongPrincipal", test: testRedeemWrongPrincipal},
		{name: "TestRedeemExpired", test: testRedeemExpired},
		{name: "TestRedeemSpent", test: testRedeemSpent},
		{name: "TestRedeemGrantExists", test: testRedeemGrantExists},
		{name: "TestOneRowOnly", test: testOneRowOnly},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.test(t, newHarness(t))
		})
	}

	t.Run("TestStoreHostileRows", func(t *testing.T) {
		t.Parallel()

		testStoreHostileRows(t, newHarness)
	})
}

// at returns a fixed time minute minutes after a base, in UTC to the
// millisecond.
func at(minute int) time.Time {
	return time.Date(2026, time.October, 15, 12, minute, 0, 0, time.UTC)
}

// entry returns a valid audit entry in the scope @server.
func entry(action, object string) audit.Entry {
	return audit.Entry{
		FenceVersion:  0,
		At:            at(minuteBase),
		Namespace:     audit.ServerScope,
		Subject:       createdBy,
		IDPSubject:    "",
		TokenID:       "",
		Action:        action,
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
		Origin:        nil,
	}
}

// person returns a live person with the ID id and the subject subject.
func person(id, subject string) principal.Principal {
	return principal.Principal{
		BreakGlass:  false,
		CreatedAt:   at(minuteBase),
		UpdatedAt:   at(minuteBase),
		DisabledAt:  time.Time{},
		ID:          id,
		Subject:     subject,
		DisplayName: "Name of " + subject,
		Kind:        principal.KindPerson,
		Source:      principal.SourceLocal,
		Namespace:   "",
		GroupCount:  0,
	}
}

func mustPrincipal(t *testing.T, h Harness, p principal.Principal) {
	t.Helper()

	if err := h.Store.CreatePrincipal(t.Context(), p, entry(otherAction, principal.Ref(p.ID))); err != nil {
		t.Fatalf("create principal %q: %v", p.ID, err)
	}
}

func offerInput(id, digest string) firstadmin.OfferInput {
	return firstadmin.OfferInput{
		Now:         at(minuteOffer),
		PrincipalID: id,
		Digest:      digest,
		CreatedBy:   createdBy,
		Reason:      reason,
	}
}

func mustOffer(t *testing.T, h Harness, id, digest string) firstadmin.OfferResult {
	t.Helper()

	res, err := h.Store.Offer(t.Context(), offerInput(id, digest), entry(offerAction, principal.Ref(id)))
	if err != nil {
		t.Fatalf("offer to %q: %v", id, err)
	}

	return res
}

func redeemInput(id, digest, grantID string) firstadmin.RedeemInput {
	return firstadmin.RedeemInput{
		Now:         at(minuteRedeem),
		AuthTime:    at(minuteOffer),
		PrincipalID: id,
		SessionID:   sessionID,
		Digest:      digest,
		GrantID:     grantID,
		Window:      window,
	}
}

func redeem(h Harness, in firstadmin.RedeemInput) (firstadmin.Result, error) {
	return h.Store.Redeem(context.Background(), in, entry(redeemAction, "@server"))
}

func wantErr(t *testing.T, what string, err, want error) {
	t.Helper()

	if !errors.Is(err, want) {
		t.Fatalf("%s: err = %v, want %v", what, err, want)
	}
}

// serverEntries returns how many entries the scope @server holds.
func serverEntries(t *testing.T, h Harness) int {
	t.Helper()

	listing, err := h.Store.List(t.Context(), audit.ServerScope, audit.Page{Number: 1, Size: audit.MaxPageSize})
	if err != nil {
		t.Fatalf("list audit entries: %v", err)
	}

	return len(listing.Entries)
}

func mustState(t *testing.T, h Harness, now time.Time) firstadmin.State {
	t.Helper()

	st, err := h.Store.State(t.Context(), now)
	if err != nil {
		t.Fatalf("read state: %v", err)
	}

	return st
}

// wantUnchanged fails unless no marker is stored, the offer to id is still
// open, no grant with grantID exists and the audit log holds entries entries.
func wantUnchanged(t *testing.T, h Harness, id, grantID string, entries int) {
	t.Helper()

	st := mustState(t, h, at(minuteRedeem))

	if st.Marker != nil {
		t.Fatalf("marker = %+v, want none", *st.Marker)
	}

	if st.Offer == nil || st.Offer.PrincipalID != id {
		t.Fatalf("offer = %+v, want the open offer to %q: the code was spent", st.Offer, id)
	}

	if _, err := h.Store.GetGrant(t.Context(), grantID); !errors.Is(err, access.ErrNotFound) {
		t.Fatalf("grant %q: err = %v, want access.ErrNotFound", grantID, err)
	}

	if got := serverEntries(t, h); got != entries {
		t.Fatalf("audit entries = %d, want %d: a refused redeem wrote one", got, entries)
	}
}

func testRedeemOneTransaction(t *testing.T, h Harness) {
	t.Helper()

	mustPrincipal(t, h, person(alice, aliceSubject))

	offer := mustOffer(t, h, alice, digestA())
	if offer.Replaced {
		t.Fatal("first offer: Replaced = true, want false")
	}

	if want := at(minuteOffer).Add(firstadmin.Lifetime); !offer.ExpiresAt.Equal(want) {
		t.Fatalf("offer expires at %s, want %s", offer.ExpiresAt, want)
	}

	before := serverEntries(t, h)

	res, err := redeem(h, redeemInput(alice, digestA(), aliceGrantID))
	if err != nil {
		t.Fatalf("redeem: %v", err)
	}

	if res.PrincipalID != alice || res.GrantID != aliceGrantID || !res.MadeAt.Equal(at(minuteRedeem)) {
		t.Fatalf("result = %+v", res)
	}

	wantSiteAdministratorGrant(t, h)
	wantMarkerAndSpentCode(t, h)

	if got := serverEntries(t, h) - before; got != 1 {
		t.Fatalf("the redeem appended %d audit entries, want 1", got)
	}

	// The route is closed for good.
	_, err = redeem(h, redeemInput(alice, digestA(), "grt-again"))
	wantErr(t, "second redeem", err, firstadmin.ErrAlreadyMade)
}

// wantSiteAdministratorGrant fails unless alice holds the grant that the redeem
// made: the role, on the installation, created by the creator of the code.
func wantSiteAdministratorGrant(t *testing.T, h Harness) {
	t.Helper()

	g, err := h.Store.GetGrant(t.Context(), aliceGrantID)
	if err != nil {
		t.Fatalf("read the grant: %v", err)
	}

	subject := access.Subject{Kind: access.SubjectPrincipal, ID: alice}
	if g.Subject != subject || g.Role != firstadmin.Role || g.Object.String() != identity.ServerObject().String() ||
		g.CreatedBy != createdBy {
		t.Fatalf("grant = %+v, want %s on %s created by %q", g, firstadmin.Role, identity.ServerObject(), createdBy)
	}
}

// wantMarkerAndSpentCode fails unless the state holds the marker of alice and
// no open code.
func wantMarkerAndSpentCode(t *testing.T, h Harness) {
	t.Helper()

	st := mustState(t, h, at(minuteRedeem))

	if st.Offer != nil {
		t.Fatalf("offer = %+v after the redeem, want none: the code is spent", *st.Offer)
	}

	want := firstadmin.Marker{
		MadeAt: at(minuteRedeem), PrincipalID: alice, GrantID: aliceGrantID, SessionID: sessionID, CreatedBy: createdBy,
	}
	if st.Marker == nil || *st.Marker != want {
		t.Fatalf("marker = %+v, want %+v", st.Marker, want)
	}
}

func testRedeemRollsBackOnAuditFailure(t *testing.T, h Harness) {
	t.Helper()

	mustPrincipal(t, h, person(alice, aliceSubject))
	mustOffer(t, h, alice, digestA())

	entries := serverEntries(t, h)

	if err := h.BreakAuditAppends(t.Context()); err != nil {
		t.Fatalf("break audit appends: %v", err)
	}

	_, err := redeem(h, redeemInput(alice, digestA(), aliceGrantID))
	if err == nil {
		t.Fatal("redeem with a failing audit append succeeded")
	}

	for _, sentinel := range []error{firstadmin.ErrAlreadyMade, firstadmin.ErrCodeInvalid, firstadmin.ErrGrantExists} {
		if errors.Is(err, sentinel) {
			t.Fatalf("redeem err = %v, want the audit failure and not %v", err, sentinel)
		}
	}

	// The grant, the spend and the marker all rolled back.
	wantUnchanged(t, h, alice, aliceGrantID, entries)
}

func testOfferRefusedAfterMarker(t *testing.T, h Harness) {
	t.Helper()

	mustPrincipal(t, h, person(alice, aliceSubject))
	mustPrincipal(t, h, person(bob, "bob"))
	mustOffer(t, h, alice, digestA())

	if _, err := redeem(h, redeemInput(alice, digestA(), aliceGrantID)); err != nil {
		t.Fatalf("redeem: %v", err)
	}

	entries := serverEntries(t, h)

	_, err := h.Store.Offer(t.Context(), offerInput(bob, digestB()), entry(offerAction, principal.Ref(bob)))
	wantErr(t, "offer after the marker", err, firstadmin.ErrAlreadyMade)

	if st := mustState(t, h, at(minuteRedeem)); st.Offer != nil {
		t.Fatalf("offer = %+v after a refused offer, want none", *st.Offer)
	}

	if got := serverEntries(t, h); got != entries {
		t.Fatalf("audit entries = %d, want %d: a refused offer wrote one", got, entries)
	}
}

func testOfferReplacesCode(t *testing.T, h Harness) {
	t.Helper()

	mustPrincipal(t, h, person(alice, aliceSubject))
	mustPrincipal(t, h, person(bob, "bob"))

	if res := mustOffer(t, h, alice, digestA()); res.Replaced {
		t.Fatal("first offer: Replaced = true, want false")
	}

	before := serverEntries(t, h)

	if res := mustOffer(t, h, bob, digestB()); !res.Replaced {
		t.Fatal("second offer: Replaced = false, want true")
	}

	if got := serverEntries(t, h) - before; got != 1 {
		t.Fatalf("the second offer appended %d audit entries, want 1", got)
	}

	st := mustState(t, h, at(minuteRedeem))
	if st.Offer == nil || st.Offer.PrincipalID != bob || st.Offer.CreatedBy != createdBy || st.Offer.Reason != reason ||
		!st.Offer.CreatedAt.Equal(
			at(minuteOffer),
		) || !st.Offer.ExpiresAt.Equal(at(minuteOffer).Add(firstadmin.Lifetime)) {
		t.Fatalf("offer = %+v, want the open offer to %q", st.Offer, bob)
	}

	// The first code died with the replace.
	_, err := redeem(h, redeemInput(alice, digestA(), aliceGrantID))
	wantErr(t, "redeem of the replaced code", err, firstadmin.ErrCodeInvalid)

	if _, err := redeem(h, redeemInput(bob, digestB(), "grt-bob")); err != nil {
		t.Fatalf("redeem of the new code: %v", err)
	}
}

func testRedeemWrongPrincipal(t *testing.T, h Harness) {
	t.Helper()

	mustPrincipal(t, h, person(alice, aliceSubject))
	mustPrincipal(t, h, person(bob, "bob"))
	mustOffer(t, h, alice, digestA())

	entries := serverEntries(t, h)

	_, err := redeem(h, redeemInput(bob, digestA(), "grt-bob"))
	wantErr(t, "redeem by another person", err, firstadmin.ErrCodeInvalid)
	wantUnchanged(t, h, alice, "grt-bob", entries)

	_, err = redeem(h, redeemInput(alice, digestB(), aliceGrantID))
	wantErr(t, "redeem with a wrong code", err, firstadmin.ErrCodeInvalid)
	wantUnchanged(t, h, alice, aliceGrantID, entries)
}

func testRedeemExpired(t *testing.T, h Harness) {
	t.Helper()

	mustPrincipal(t, h, person(alice, aliceSubject))
	mustOffer(t, h, alice, digestA())

	expiry := at(minuteOffer).Add(firstadmin.Lifetime)

	if st := mustState(t, h, expiry); st.Offer != nil {
		t.Fatalf("offer = %+v at its expiry, want none", *st.Offer)
	}

	if st := mustState(t, h, expiry.Add(-time.Millisecond)); st.Offer == nil {
		t.Fatal("no offer 1 ms before its expiry, want the open offer")
	}

	entries := serverEntries(t, h)

	late := redeemInput(alice, digestA(), aliceGrantID)
	late.Now = expiry
	late.AuthTime = expiry.Add(-time.Minute)

	_, err := redeem(h, late)
	wantErr(t, "redeem at the expiry", err, firstadmin.ErrCodeInvalid)
	wantUnchanged(t, h, alice, aliceGrantID, entries)

	// One millisecond earlier the code still works.
	early := late
	early.Now = expiry.Add(-time.Millisecond)

	if _, err := redeem(h, early); err != nil {
		t.Fatalf("redeem 1 ms before the expiry: %v", err)
	}
}

func testRedeemSpent(t *testing.T, h Harness) {
	t.Helper()

	mustPrincipal(t, h, person(alice, aliceSubject))

	// No code at all.
	_, err := redeem(h, redeemInput(alice, digestA(), aliceGrantID))
	wantErr(t, "redeem with no offer", err, firstadmin.ErrCodeInvalid)

	mustOffer(t, h, alice, digestA())

	if _, redeemErr := redeem(h, redeemInput(alice, digestA(), aliceGrantID)); redeemErr != nil {
		t.Fatalf("redeem: %v", redeemErr)
	}

	entries := serverEntries(t, h)

	_, err = redeem(h, redeemInput(alice, digestA(), "grt-second"))
	wantErr(t, "redeem of a spent code", err, firstadmin.ErrAlreadyMade)

	if got := serverEntries(t, h); got != entries {
		t.Fatalf("audit entries = %d, want %d: a refused redeem wrote one", got, entries)
	}
}

func testRedeemGrantExists(t *testing.T, h Harness) {
	t.Helper()

	mustPrincipal(t, h, person(alice, aliceSubject))

	pre := access.Grant{
		CreatedAt: at(minuteBase),
		Condition: nil,
		Object:    identity.ServerObject(),
		ID:        preGrantID,
		Role:      firstadmin.Role,
		CreatedBy: createdBy,
		Subject:   access.Subject{Kind: access.SubjectPrincipal, ID: alice},
	}

	err := h.Store.CreateGrant(
		t.Context(),
		pre,
		namespace.Fence{Namespace: "", FailoverVersion: 0},
		entry(otherAction, "@server"),
		nil,
	)
	if err != nil {
		t.Fatalf("create the grant before the offer: %v", err)
	}

	mustOffer(t, h, alice, digestA())

	entries := serverEntries(t, h)

	_, err = redeem(h, redeemInput(alice, digestA(), aliceGrantID))
	wantErr(t, "redeem by the holder of the role", err, firstadmin.ErrGrantExists)

	// Nothing is spent: the code stays open, and there is no marker and no
	// second grant.
	wantUnchanged(t, h, alice, aliceGrantID, entries)
}

func testOneRowOnly(t *testing.T, h Harness) {
	t.Helper()

	ctx := t.Context()

	mustPrincipal(t, h, person(alice, aliceSubject))
	mustOffer(t, h, alice, digestA())

	code := CodeRow{
		ID: secondRow, PrincipalID: alice, Digest: digestB(), CreatedBy: createdBy, Reason: reason,
		CreatedAtMs: at(minuteOffer).UnixMilli(), ExpiresAtMs: at(minuteOffer).Add(firstadmin.Lifetime).UnixMilli(),
	}

	if err := h.InsertCode(ctx, code); err == nil {
		t.Fatal("a second code row with the id 2 was stored, want the schema to refuse it")
	}

	code.ID = 1
	if err := h.InsertCode(ctx, code); err == nil {
		t.Fatal("a second code row with the id 1 was stored, want the schema to refuse it")
	}

	marker := MarkerRow{
		ID: secondRow, PrincipalID: alice, GrantID: aliceGrantID, SessionID: sessionID, CreatedBy: createdBy,
		MadeAtMs: at(minuteRedeem).UnixMilli(),
	}

	if err := h.InsertMarker(ctx, marker); err == nil {
		t.Fatal("a marker row with the id 2 was stored, want the schema to refuse it")
	}

	if st := mustState(t, h, at(minuteRedeem)); st.Marker != nil || st.Offer == nil || st.Offer.PrincipalID != alice {
		t.Fatalf("state = %+v after the refused rows, want the one open offer and no marker", st)
	}

	if _, err := redeem(h, redeemInput(alice, digestA(), aliceGrantID)); err != nil {
		t.Fatalf("redeem: %v", err)
	}

	marker.ID = 1
	if err := h.InsertMarker(ctx, marker); err == nil {
		t.Fatal("a second marker row with the id 1 was stored, want the schema to refuse it")
	}
}

// codeRowOf returns a valid raw code row for id.
func codeRowOf(id string) CodeRow {
	return CodeRow{
		ID: 1, PrincipalID: id, Digest: digestA(), CreatedBy: createdBy, Reason: reason,
		CreatedAtMs: at(minuteOffer).UnixMilli(), ExpiresAtMs: at(minuteOffer).Add(firstadmin.Lifetime).UnixMilli(),
	}
}

// testStoreHostileRows is the table of the hostile cases of the store layer.
// Each case runs in its own store and sends the hostile input to the store
// directly, as if the service and the handler had failed.
func testStoreHostileRows(t *testing.T, newHarness Factory) {
	t.Helper()

	t.Run("principals", func(t *testing.T) { t.Parallel(); testHostilePrincipals(t, newHarness) })
	t.Run("sign-in time", func(t *testing.T) { t.Parallel(); testHostileSignIn(t, newHarness) })
	t.Run("input digest", func(t *testing.T) { t.Parallel(); testHostileInputDigest(t, newHarness) })
	t.Run("redeem form", func(t *testing.T) { t.Parallel(); testHostileRedeemForm(t, newHarness) })
	t.Run("offer form", func(t *testing.T) { t.Parallel(); testHostileOfferForm(t, newHarness) })
	t.Run("stored rows", func(t *testing.T) { t.Parallel(); testHostileStoredRows(t, newHarness) })
}

// hostilePrincipalCase makes a principal that CheckPrincipal refuses. A case
// that makes its own person uses the ID bob. The other cases change alice.
type hostilePrincipalCase struct {
	prepare func(t *testing.T, h Harness)
	name    string
}

// withSubject returns a prepare function that sets the subject of alice with
// no validation, as a database of another version leaves it.
func withSubject(subject string) func(t *testing.T, h Harness) {
	return func(t *testing.T, h Harness) {
		t.Helper()

		if err := h.SetSubject(t.Context(), alice, subject); err != nil {
			t.Fatalf("set subject: %v", err)
		}
	}
}

// withOtherPerson returns a prepare function that makes the person bob with
// the changes of change.
func withOtherPerson(subject string, change func(p *principal.Principal)) func(t *testing.T, h Harness) {
	return func(t *testing.T, h Harness) {
		t.Helper()

		p := person(bob, subject)
		change(&p)
		mustPrincipal(t, h, p)
	}
}

func hostilePrincipalCases() []hostilePrincipalCase {
	return []hostilePrincipalCase{
		{name: "disabled", prepare: func(t *testing.T, h Harness) {
			t.Helper()

			err := h.Store.DisablePrincipal(t.Context(), alice, entry(otherAction, principal.Ref(alice)))
			if err != nil {
				t.Fatalf("disable: %v", err)
			}
		}},
		{name: "deleted", prepare: func(t *testing.T, h Harness) {
			t.Helper()

			err := h.Store.DeletePrincipal(t.Context(), alice, entry(otherAction, principal.Ref(alice)))
			if err != nil {
				t.Fatalf("delete: %v", err)
			}
		}},
		{name: "break-glass", prepare: withOtherPerson("bg", func(p *principal.Principal) { p.BreakGlass = true })},
		{
			name:    "service",
			prepare: withOtherPerson("svc", func(p *principal.Principal) { p.Kind = principal.KindService }),
		},
		{name: "temporary administrator", prepare: func(t *testing.T, h Harness) {
			t.Helper()

			r := tempadmin.TempAdmin{
				CreatedAt:      at(minuteBase),
				ExpiresAt:      at(minuteBase).Add(tempadmin.Lifetime),
				EndedAt:        time.Time{},
				ReviewOpenedAt: time.Time{},
				PrincipalID:    alice,
				Subject:        aliceSubject,
				CreatedBy:      createdBy,
				Reason:         reason,
			}
			err := h.Store.CreateTempAdmin(t.Context(), r, entry(tempadmin.Action, tempadmin.Ref(alice)))
			if err != nil {
				t.Fatalf("create temporary administrator: %v", err)
			}
		}},
		{name: "dev subject", prepare: withSubject("dev:alice")},
		{name: "upper case dev subject", prepare: withSubject("DEV:alice")},
		{name: "bootstrap subject", prepare: withSubject("admin")},
	}
}

// testHostilePrincipals runs a redeem and an offer for a principal that
// CheckPrincipal refuses. The code row is a raw row, as a database of another
// version or a change after the offer leaves it, so the transaction alone
// must stop the redeem.
func testHostilePrincipals(t *testing.T, newHarness Factory) {
	t.Helper()

	for _, tc := range hostilePrincipalCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t)
			mustPrincipal(t, h, person(alice, aliceSubject))
			tc.prepare(t, h)

			// The case is about bob when it made its own person.
			id := alice
			if _, err := h.Store.GetPrincipal(t.Context(), bob); err == nil {
				id = bob
			}

			if err := h.InsertCode(t.Context(), codeRowOf(id)); err != nil {
				t.Fatalf("insert the code row: %v", err)
			}

			entries := serverEntries(t, h)

			_, err := redeem(h, redeemInput(id, digestA(), aliceGrantID))
			wantErr(t, "redeem", err, firstadmin.ErrPrincipalInvalid)
			wantUnchanged(t, h, id, aliceGrantID, entries)

			// The offer applies the same rule to the live row.
			_, err = h.Store.Offer(t.Context(), offerInput(id, digestB()), entry(offerAction, principal.Ref(id)))
			wantErr(t, "offer", err, firstadmin.ErrPrincipalInvalid)

			if got := serverEntries(t, h); got != entries {
				t.Fatalf("audit entries = %d, want %d: a refused offer wrote one", got, entries)
			}
		})
	}
}

func testHostileSignIn(t *testing.T, newHarness Factory) {
	t.Helper()

	cases := []struct {
		mutate func(in *firstadmin.RedeemInput)
		name   string
	}{
		{
			name:   "older than the window",
			mutate: func(in *firstadmin.RedeemInput) { in.AuthTime = in.Now.Add(-window - time.Millisecond) },
		},
		{name: "after now", mutate: func(in *firstadmin.RedeemInput) { in.AuthTime = in.Now.Add(time.Millisecond) }},
		{name: "window of 0", mutate: func(in *firstadmin.RedeemInput) { in.Window = 0 }},
		{name: "negative window", mutate: func(in *firstadmin.RedeemInput) { in.Window = -time.Minute }},
		{name: "no sign-in time", mutate: func(in *firstadmin.RedeemInput) { in.AuthTime = time.Time{} }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t)
			mustPrincipal(t, h, person(alice, aliceSubject))
			mustOffer(t, h, alice, digestA())

			entries := serverEntries(t, h)
			in := redeemInput(alice, digestA(), aliceGrantID)
			tc.mutate(&in)

			_, err := redeem(h, in)
			wantErr(t, "redeem", err, firstadmin.ErrStaleSignIn)
			wantUnchanged(t, h, alice, aliceGrantID, entries)
		})
	}

	t.Run("exactly the window", func(t *testing.T) {
		t.Parallel()

		h := newHarness(t)
		mustPrincipal(t, h, person(alice, aliceSubject))
		mustOffer(t, h, alice, digestA())

		in := redeemInput(alice, digestA(), aliceGrantID)
		in.AuthTime = in.Now.Add(-window)

		if _, err := redeem(h, in); err != nil {
			t.Fatalf("redeem with a sign-in as old as the window: %v", err)
		}
	})
}

func testHostileInputDigest(t *testing.T, newHarness Factory) {
	t.Helper()

	cases := []struct {
		name   string
		digest string
	}{
		{name: "empty", digest: ""},
		{name: "63 characters", digest: digestA()[:firstadmin.DigestLength-1]},
		{name: "65 characters", digest: digestA() + "a"},
		{name: "upper case", digest: strings.ToUpper(digestA())},
		{name: "not hex", digest: strings.Repeat("g", firstadmin.DigestLength)},
		{name: "other digest", digest: digestB()},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t)
			mustPrincipal(t, h, person(alice, aliceSubject))
			mustOffer(t, h, alice, digestA())

			entries := serverEntries(t, h)

			_, err := redeem(h, redeemInput(alice, tc.digest, aliceGrantID))
			wantErr(t, "redeem", err, firstadmin.ErrCodeInvalid)
			wantUnchanged(t, h, alice, aliceGrantID, entries)
		})
	}
}

func testHostileRedeemForm(t *testing.T, newHarness Factory) {
	t.Helper()

	redeemCases := []struct {
		mutate func(in *firstadmin.RedeemInput)
		want   error
		name   string
	}{
		{
			name:   "blank session",
			want:   firstadmin.ErrInvalid,
			mutate: func(in *firstadmin.RedeemInput) { in.SessionID = "" },
		},
		{
			name:   "hidden character in the session",
			want:   firstadmin.ErrInvalid,
			mutate: func(in *firstadmin.RedeemInput) { in.SessionID = "ses-\u202ex" },
		},
		{
			name:   "grant ID without the prefix",
			want:   firstadmin.ErrInvalid,
			mutate: func(in *firstadmin.RedeemInput) { in.GrantID = aliceSubject },
		},
		{
			name:   "grant ID with a bad character",
			want:   firstadmin.ErrInvalid,
			mutate: func(in *firstadmin.RedeemInput) { in.GrantID = "grt-a b" },
		},
		{
			name:   "principal ID without the prefix",
			want:   firstadmin.ErrPrincipalInvalid,
			mutate: func(in *firstadmin.RedeemInput) { in.PrincipalID = "alice" },
		},
		{
			name:   "blank principal ID",
			want:   firstadmin.ErrPrincipalInvalid,
			mutate: func(in *firstadmin.RedeemInput) { in.PrincipalID = "" },
		},
	}

	for _, tc := range redeemCases {
		t.Run("redeem "+tc.name, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t)
			mustPrincipal(t, h, person(alice, aliceSubject))
			mustOffer(t, h, alice, digestA())

			entries := serverEntries(t, h)
			in := redeemInput(alice, digestA(), aliceGrantID)
			tc.mutate(&in)

			_, err := redeem(h, in)
			wantErr(t, "redeem", err, tc.want)
			wantUnchanged(t, h, alice, aliceGrantID, entries)
		})
	}
}

// testHostileOfferForm sends an offer with a bad field to the store.
func testHostileOfferForm(t *testing.T, newHarness Factory) {
	t.Helper()

	offerCases := []struct {
		mutate func(in *firstadmin.OfferInput)
		want   error
		name   string
	}{
		{
			name:   "digest of another length",
			want:   firstadmin.ErrInvalid,
			mutate: func(in *firstadmin.OfferInput) { in.Digest = in.Digest[:10] },
		},
		{
			name:   "upper case digest",
			want:   firstadmin.ErrInvalid,
			mutate: func(in *firstadmin.OfferInput) { in.Digest = strings.ToUpper(in.Digest) },
		},
		{
			name:   "blank creator",
			want:   firstadmin.ErrInvalid,
			mutate: func(in *firstadmin.OfferInput) { in.CreatedBy = "" },
		},
		{
			name:   "blank reason",
			want:   firstadmin.ErrReasonInvalid,
			mutate: func(in *firstadmin.OfferInput) { in.Reason = "  " },
		},
		{
			name:   "reason with a control character",
			want:   firstadmin.ErrReasonInvalid,
			mutate: func(in *firstadmin.OfferInput) { in.Reason = "a\nb" },
		},
		{
			name:   "principal ID without the prefix",
			want:   firstadmin.ErrPrincipalInvalid,
			mutate: func(in *firstadmin.OfferInput) { in.PrincipalID = "alice" },
		},
		{
			name:   "no time",
			want:   firstadmin.ErrInvalid,
			mutate: func(in *firstadmin.OfferInput) { in.Now = time.Time{} },
		},
	}

	for _, tc := range offerCases {
		t.Run("offer "+tc.name, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t)
			mustPrincipal(t, h, person(alice, aliceSubject))

			entries := serverEntries(t, h)
			in := offerInput(alice, digestA())
			tc.mutate(&in)

			_, err := h.Store.Offer(t.Context(), in, entry(offerAction, principal.Ref(alice)))
			wantErr(t, "offer", err, tc.want)

			if st := mustState(t, h, at(minuteRedeem)); st.Offer != nil {
				t.Fatalf("offer = %+v after a refused offer, want none", *st.Offer)
			}

			if got := serverEntries(t, h); got != entries {
				t.Fatalf("audit entries = %d, want %d: a refused offer wrote one", got, entries)
			}
		})
	}
}

// testHostileStoredRows writes rows that the schema alone must refuse: a
// digest of another length or form, a lifetime of another length and a second
// row.
func testHostileStoredRows(t *testing.T, newHarness Factory) {
	t.Helper()

	for _, tc := range storedCodeRowCases() {
		t.Run("code "+tc.name, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t)
			row := codeRowOf(alice)
			tc.mutate(&row)

			if err := h.InsertCode(t.Context(), row); err == nil {
				t.Fatalf("code row %+v was stored, want the schema to refuse it", row)
			}

			if st := mustState(t, h, at(minuteRedeem)); st.Offer != nil {
				t.Fatalf("offer = %+v after a refused row, want none", *st.Offer)
			}
		})
	}

	for _, id := range []int{0, 2} {
		t.Run("marker row id "+strconv.Itoa(id), func(t *testing.T) {
			t.Parallel()

			h := newHarness(t)
			row := MarkerRow{
				ID: id, PrincipalID: alice, GrantID: aliceGrantID, SessionID: sessionID, CreatedBy: createdBy,
				MadeAtMs: at(minuteRedeem).UnixMilli(),
			}

			if err := h.InsertMarker(t.Context(), row); err == nil {
				t.Fatalf("marker row %+v was stored, want the schema to refuse it", row)
			}

			if st := mustState(t, h, at(minuteRedeem)); st.Marker != nil {
				t.Fatalf("marker = %+v after a refused row, want none", *st.Marker)
			}
		})
	}

	t.Run("a raw code row after the marker", func(t *testing.T) {
		t.Parallel()

		testRawCodeAfterMarker(t, newHarness(t))
	})
}

// storedCodeRowCase is a code row that the schema alone must refuse.
type storedCodeRowCase struct {
	mutate func(r *CodeRow)
	name   string
}

func storedCodeRowCases() []storedCodeRowCase {
	const day = int64(24 * time.Hour / time.Millisecond)

	return []storedCodeRowCase{
		{name: "digest of 63 characters", mutate: func(r *CodeRow) { r.Digest = r.Digest[:firstadmin.DigestLength-1] }},
		{name: "digest of 65 characters", mutate: func(r *CodeRow) { r.Digest += "a" }},
		{name: "empty digest", mutate: func(r *CodeRow) { r.Digest = "" }},
		{name: "upper case digest", mutate: func(r *CodeRow) { r.Digest = strings.ToUpper(r.Digest) }},
		{
			name:   "digest that is not hex",
			mutate: func(r *CodeRow) { r.Digest = strings.Repeat("z", firstadmin.DigestLength) },
		},
		{name: "lifetime 1 ms short", mutate: func(r *CodeRow) { r.ExpiresAtMs-- }},
		{name: "lifetime 1 ms long", mutate: func(r *CodeRow) { r.ExpiresAtMs++ }},
		{name: "lifetime of 0", mutate: func(r *CodeRow) { r.ExpiresAtMs = r.CreatedAtMs }},
		{name: "lifetime of one day", mutate: func(r *CodeRow) { r.ExpiresAtMs = r.CreatedAtMs + day }},
		{name: "negative lifetime", mutate: func(r *CodeRow) { r.ExpiresAtMs = r.CreatedAtMs - firstadmin.LifetimeMs }},
		{name: "second row", mutate: func(r *CodeRow) { r.ID = 2 }},
		{name: "row 0", mutate: func(r *CodeRow) { r.ID = 0 }},
		{name: "blank principal", mutate: func(r *CodeRow) { r.PrincipalID = "" }},
	}
}

// testRawCodeAfterMarker writes a code row after the marker and proves the
// marker still closes the redeem.
func testRawCodeAfterMarker(t *testing.T, h Harness) {
	t.Helper()

	mustPrincipal(t, h, person(alice, aliceSubject))
	mustOffer(t, h, alice, digestA())

	if _, err := redeem(h, redeemInput(alice, digestA(), aliceGrantID)); err != nil {
		t.Fatalf("redeem: %v", err)
	}

	if err := h.InsertCode(t.Context(), codeRowOf(alice)); err != nil {
		t.Fatalf("insert the code row: %v", err)
	}

	_, err := redeem(h, redeemInput(alice, digestA(), "grt-again"))
	wantErr(t, "redeem with a code row after the marker", err, firstadmin.ErrAlreadyMade)
}
