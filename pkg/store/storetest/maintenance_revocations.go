package storetest

import (
	"crypto/sha256"
	"errors"
	"strconv"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/tempadmin"
)

// RevocationStore is every port the revocation suite uses: the stores of
// sessions, access tokens, principals and temporary administrators that it
// revokes through, the audit store that it reads the entries from, and the
// portable export and stage that the restore cases swap through.
type RevocationStore interface {
	PortableStore
	backup.Applier
	audit.Store
	session.Store
	accesstoken.Store
	principal.RecordStore
	tempadmin.Store
}

// RevocationFactory returns a fresh, empty store for one subtest.
type RevocationFactory func(t *testing.T) RevocationStore

// RunRevocations executes the suite of the writes that only reduce access.
// In maintenance mode they run (a sign-out, a session end, a token revocation
// and the disable of a principal), each with its audit entry; the writes that
// grant or widen access stay refused; and a revocation made during the stay
// survives the restore of older access data.
func RunRevocations(t *testing.T, newStore RevocationFactory) {
	t.Helper()

	cases := []struct {
		test func(t *testing.T, s RevocationStore)
		name string
	}{
		{name: "a write that grants access is refused in maintenance mode", test: testGrantsRefused},
		{name: "a revocation refuses hostile input in maintenance mode", test: testRevocationsHostileInput},
		{name: "a revocation made in maintenance mode survives a restore", test: testRevocationSurvivesRestore},
		{name: "a restore applies only the revocations of its own stay", test: testRevocationStayIsBounded},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.test(t, newStore(t))
		})
	}

	for _, tc := range revocationCases() {
		t.Run("in maintenance mode: "+tc.name, func(t *testing.T) {
			t.Parallel()
			testRevocationRuns(t, newStore(t), tc)
		})
	}
}

// Identities of the fixture of the revocation suite.
const (
	revPerson        = "prn-person"
	revService       = "prn-service"
	revTempAdmin     = "prn-temp"
	revSessionOne    = "ses-1"
	revSessionTwo    = "ses-2"
	revPersonalToken = "tok-personal"
	revServiceToken  = "tok-service"
	revTempToken     = "tok-temp"
	revKeepToken     = "tok-keep"
)

// revAt is a fixed, millisecond-precision instant of the suite: stores keep
// milliseconds.
func revAt(minute int) time.Time {
	return time.Date(2026, time.October, 15, 9, minute, 0, 0, time.UTC)
}

// revEntry returns a valid audit entry of the kind change in the scope
// @server for object, as a service builds it.
func revEntry(object string, when time.Time) audit.Entry {
	return audit.Entry{
		At:            when,
		Namespace:     audit.ServerScope,
		Subject:       "alice",
		IDPSubject:    "",
		TokenID:       "",
		Action:        "server:manage-tokens",
		Object:        object,
		Version:       "",
		ValuePath:     "",
		ValueCount:    nil,
		Changes:       nil,
		SourceIP:      "192.0.2.1",
		UserAgent:     "storetest",
		Channel:       audit.ChannelAPI,
		Kind:          audit.KindChange,
		Outcome:       audit.OutcomeSuccess,
		RequestID:     "",
		ObjectID:      "",
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		FenceVersion:  0,
		Reason:        "",
		PrincipalKind: "",
		JobID:         "",
	}
}

// revDigest returns a distinct digest for n.
func revDigest(n int) [sha256.Size]byte {
	return sha256.Sum256([]byte("revocation " + strconv.Itoa(n)))
}

// revMustPrincipal creates the principal id of kind.
func revMustPrincipal(t *testing.T, s RevocationStore, id string, kind principal.Kind) {
	t.Helper()

	p := principal.Principal{
		CreatedAt:   revAt(0),
		UpdatedAt:   revAt(0),
		DisabledAt:  time.Time{},
		ID:          id,
		Subject:     "subject-" + id,
		DisplayName: "Name of " + id,
		Kind:        kind,
		Source:      principal.SourceLocal,
		Namespace:   "",
		GroupCount:  0,
		BreakGlass:  false,
	}

	if err := s.CreatePrincipal(t.Context(), p, revEntry(principal.Ref(id), revAt(0))); err != nil {
		t.Fatalf("create principal %q: %v", id, err)
	}
}

// revMustSession creates the session id of principalID.
func revMustSession(t *testing.T, s RevocationStore, id, principalID string, n int) {
	t.Helper()

	created := revAt(1)
	ss := session.Session{
		CreatedAt:   created,
		LastSeenAt:  created,
		AuthTime:    created.Add(-time.Minute),
		EndedAt:     time.Time{},
		ID:          id,
		PrincipalID: principalID,
		Provider:    "corp-idp",
		EndReason:   "",
		SourceIP:    "192.0.2.10",
		UserAgent:   "storetest",
	}

	e := revEntry(session.FullRef(principalID, id), created)

	if err := s.CreateSession(t.Context(), ss, revDigest(n), e); err != nil {
		t.Fatalf("create session %q: %v", id, err)
	}
}

// revToken returns the token id of principalID with kind.
func revToken(id, principalID string, kind accesstoken.Kind) accesstoken.Token {
	return accesstoken.Token{
		CreatedAt:   revAt(1),
		ExpiresAt:   revAt(1).Add(30 * 24 * time.Hour),
		LastUsedAt:  time.Time{},
		RevokedAt:   time.Time{},
		DisabledAt:  time.Time{},
		ID:          id,
		PrincipalID: principalID,
		Name:        "token " + id,
		Kind:        kind,
		ReplacedBy:  "",
		CreatedBy:   "admin",
	}
}

// revMustToken creates tok with the digest of n.
func revMustToken(t *testing.T, s RevocationStore, tok accesstoken.Token, n int) {
	t.Helper()

	if err := s.CreateToken(t.Context(), tok, revDigest(n), revEntry(accesstoken.Ref(tok.ID), revAt(1))); err != nil {
		t.Fatalf("create token %q: %v", tok.ID, err)
	}
}

// seedAccess creates the access data of the suite: a person with two
// sessions and a personal token, a service principal with a service token,
// and a person who is a temporary administrator and holds a token.
func seedAccess(t *testing.T, s RevocationStore) {
	t.Helper()

	revMustPrincipal(t, s, revPerson, principal.KindPerson)
	revMustPrincipal(t, s, revService, principal.KindService)
	revMustPrincipal(t, s, revTempAdmin, principal.KindPerson)

	revMustSession(t, s, revSessionOne, revPerson, 1)
	revMustSession(t, s, revSessionTwo, revPerson, 2)
	revMustToken(t, s, revToken(revPersonalToken, revPerson, accesstoken.KindPersonal), 3)
	revMustToken(t, s, revToken(revKeepToken, revPerson, accesstoken.KindPersonal), 4)
	revMustToken(t, s, revToken(revServiceToken, revService, accesstoken.KindService), 5)
	revMustToken(t, s, revToken(revTempToken, revTempAdmin, accesstoken.KindPersonal), 6)

	record := tempadmin.TempAdmin{
		CreatedAt:   revAt(2),
		ExpiresAt:   revAt(2).Add(tempadmin.Lifetime),
		EndedAt:     time.Time{},
		PrincipalID: revTempAdmin,
		Subject:     "subject-" + revTempAdmin,
		CreatedBy:   "admin",
		Reason:      "every administrator lost access",
	}

	if err := s.CreateTempAdmin(t.Context(), record, revEntry(tempadmin.Ref(revTempAdmin), revAt(2))); err != nil {
		t.Fatalf("create temporary administrator: %v", err)
	}
}

// revEnter moves the installation to maintenance mode.
func revEnter(t *testing.T, s RevocationStore) {
	t.Helper()

	if err := s.SetStateIf(t.Context(), maintenance.StateNormal, maintenance.StateMaintenance); err != nil {
		t.Fatalf("enter maintenance mode: %v", err)
	}
}

// revLeave moves the installation back to normal operation.
func revLeave(t *testing.T, s RevocationStore) {
	t.Helper()

	if err := s.SetStateIf(t.Context(), maintenance.StateMaintenance, maintenance.StateNormal); err != nil {
		t.Fatalf("leave maintenance mode: %v", err)
	}
}

// revAuditTotal returns the count of the entries in the scope @server.
func revAuditTotal(t *testing.T, s RevocationStore) int64 {
	t.Helper()

	listing, err := s.List(t.Context(), audit.ServerScope, audit.Page{Number: 1, Size: 1})
	if err != nil {
		t.Fatalf("list the server audit entries: %v", err)
	}

	return listing.Total
}

// revSessionEnded fails the test unless the session id has ended with reason.
func revSessionEnded(t *testing.T, s RevocationStore, id string, reason session.EndReason) {
	t.Helper()

	got, err := s.GetSession(t.Context(), id)
	if err != nil {
		t.Fatalf("get session %q: %v", id, err)
	}

	if got.EndedAt.IsZero() || got.EndReason != reason {
		t.Fatalf("session %q = ended %v for %q, want ended for %q", id, got.EndedAt, got.EndReason, reason)
	}
}

// revSessionActive fails the test when the session id has ended.
func revSessionActive(t *testing.T, s RevocationStore, id string) {
	t.Helper()

	got, err := s.GetSession(t.Context(), id)
	if err != nil {
		t.Fatalf("get session %q: %v", id, err)
	}

	if !got.EndedAt.IsZero() {
		t.Fatalf("session %q ended at %v, want it active", id, got.EndedAt)
	}
}

// revTokenRevoked fails the test unless the token id is revoked.
func revTokenRevoked(t *testing.T, s RevocationStore, id string) {
	t.Helper()

	got, err := s.GetToken(t.Context(), id)
	if err != nil {
		t.Fatalf("get token %q: %v", id, err)
	}

	if got.RevokedAt.IsZero() {
		t.Fatalf("token %q is not revoked, want it revoked", id)
	}
}

// revTokenActive fails the test when the token id is revoked.
func revTokenActive(t *testing.T, s RevocationStore, id string) {
	t.Helper()

	got, err := s.GetToken(t.Context(), id)
	if err != nil {
		t.Fatalf("get token %q: %v", id, err)
	}

	if !got.RevokedAt.IsZero() {
		t.Fatalf("token %q was revoked at %v, want it active", id, got.RevokedAt)
	}
}

// revPrincipalDisabled fails the test unless the principal id is disabled.
func revPrincipalDisabled(t *testing.T, s RevocationStore, id string) {
	t.Helper()

	got, err := s.GetPrincipal(t.Context(), id)
	if err != nil {
		t.Fatalf("get principal %q: %v", id, err)
	}

	if got.DisabledAt.IsZero() {
		t.Fatalf("principal %q is enabled, want it disabled", id)
	}
}

// revPrincipalEnabled fails the test when the principal id is disabled.
func revPrincipalEnabled(t *testing.T, s RevocationStore, id string) {
	t.Helper()

	got, err := s.GetPrincipal(t.Context(), id)
	if err != nil {
		t.Fatalf("get principal %q: %v", id, err)
	}

	if !got.DisabledAt.IsZero() {
		t.Fatalf("principal %q was disabled at %v, want it enabled", id, got.DisabledAt)
	}
}

// revocationCase is one write that only reduces access, with a check of its
// effect.
type revocationCase struct {
	run   func(t *testing.T, s RevocationStore) error
	check func(t *testing.T, s RevocationStore)
	name  string
}

// revocationCases lists every write that only reduces access.
func revocationCases() []revocationCase {
	return []revocationCase{
		{
			name: "sign-out ends the session",
			run: func(t *testing.T, s RevocationStore) error {
				t.Helper()

				e := revEntry(session.FullRef(revPerson, revSessionOne), revAt(10))

				return s.EndSession(t.Context(), revSessionOne, session.ReasonSignOut, e)
			},
			check: func(t *testing.T, s RevocationStore) {
				t.Helper()
				revSessionEnded(t, s, revSessionOne, session.ReasonSignOut)
				revSessionActive(t, s, revSessionTwo)
			},
		},
		{
			name: "an administrator ends the session",
			run: func(t *testing.T, s RevocationStore) error {
				t.Helper()

				e := revEntry(session.FullRef(revPerson, revSessionOne), revAt(10))

				return s.EndSession(t.Context(), revSessionOne, session.ReasonEnded, e)
			},
			check: func(t *testing.T, s RevocationStore) {
				t.Helper()
				revSessionEnded(t, s, revSessionOne, session.ReasonEnded)
			},
		},
		{
			name: "revoking an access token",
			run: func(t *testing.T, s RevocationStore) error {
				t.Helper()

				return s.RevokeToken(
					t.Context(),
					revPersonalToken,
					revEntry(accesstoken.Ref(revPersonalToken), revAt(10)),
				)
			},
			check: func(t *testing.T, s RevocationStore) {
				t.Helper()
				revTokenRevoked(t, s, revPersonalToken)
				revTokenActive(t, s, revKeepToken)
			},
		},
		{
			name: "revoking a service token",
			run: func(t *testing.T, s RevocationStore) error {
				t.Helper()

				return s.RevokeToken(
					t.Context(),
					revServiceToken,
					revEntry(accesstoken.Ref(revServiceToken), revAt(10)),
				)
			},
			check: func(t *testing.T, s RevocationStore) {
				t.Helper()
				revTokenRevoked(t, s, revServiceToken)
			},
		},
		{
			name: "disabling a principal",
			run: func(t *testing.T, s RevocationStore) error {
				t.Helper()

				return s.ExpireTempAdmin(t.Context(), revTempAdmin, revEntry(tempadmin.Ref(revTempAdmin), revAt(10)))
			},
			check: func(t *testing.T, s RevocationStore) {
				t.Helper()
				revPrincipalDisabled(t, s, revTempAdmin)
				revTokenRevoked(t, s, revTempToken)
				revPrincipalEnabled(t, s, revPerson)
			},
		},
	}
}

// testRevocationRuns proves that one write that only reduces access runs in
// maintenance mode, changes the row, and appends its audit entry in the same
// transaction: one entry.
func testRevocationRuns(t *testing.T, s RevocationStore, tc revocationCase) {
	t.Helper()

	seedAccess(t, s)
	revEnter(t, s)

	before := revAuditTotal(t, s)

	if err := tc.run(t, s); err != nil {
		t.Fatalf("%s in maintenance mode: %v", tc.name, err)
	}

	tc.check(t, s)

	if got := revAuditTotal(t, s); got != before+1 {
		t.Fatalf("%s in maintenance mode: audit entries = %d, want %d", tc.name, got, before+1)
	}
}

// grantAttempt is one write that grants or widens access.
type grantAttempt struct {
	run  func(t *testing.T, s RevocationStore) error
	name string
}

// testGrantsRefused proves that the writes which grant or widen access stay
// checked: in maintenance mode each answers maintenance.ErrInMaintenance and
// changes nothing.
func testGrantsRefused(t *testing.T, s RevocationStore) {
	t.Helper()

	seedAccess(t, s)

	// A disabled principal to enable: a temporary administrator that expired.
	expire := revEntry(tempadmin.Ref(revTempAdmin), revAt(5))

	if err := s.ExpireTempAdmin(t.Context(), revTempAdmin, expire); err != nil {
		t.Fatalf("expire the temporary administrator: %v", err)
	}

	revEnter(t, s)

	attempts := []grantAttempt{
		{
			name: "create a session",
			run: func(t *testing.T, s RevocationStore) error {
				t.Helper()

				ss := session.Session{
					CreatedAt: revAt(20), LastSeenAt: revAt(20), AuthTime: revAt(19), EndedAt: time.Time{},
					ID: "ses-new", PrincipalID: revPerson, Provider: "corp-idp", EndReason: "",
					SourceIP: "192.0.2.10", UserAgent: "storetest",
				}

				return s.CreateSession(
					t.Context(),
					ss,
					revDigest(100),
					revEntry(session.FullRef(revPerson, "ses-new"), revAt(20)),
				)
			},
		},
		{
			name: "create an access token",
			run: func(t *testing.T, s RevocationStore) error {
				t.Helper()

				tok := accesstoken.Token{
					CreatedAt: revAt(20), ExpiresAt: revAt(20).Add(time.Hour), LastUsedAt: time.Time{},
					RevokedAt: time.Time{}, DisabledAt: time.Time{}, ID: "tok-new", PrincipalID: revPerson,
					Name: "new", Kind: accesstoken.KindPersonal, ReplacedBy: "", CreatedBy: "admin",
				}

				return s.CreateToken(t.Context(), tok, revDigest(101), revEntry(accesstoken.Ref("tok-new"), revAt(20)))
			},
		},
		{
			name: "enable a disabled principal",
			run: func(t *testing.T, s RevocationStore) error {
				t.Helper()

				return s.EnablePrincipal(t.Context(), revTempAdmin, revEntry(principal.Ref(revTempAdmin), revAt(20)))
			},
		},
	}

	before := revAuditTotal(t, s)

	for _, tc := range attempts {
		if err := tc.run(t, s); !errors.Is(err, maintenance.ErrInMaintenance) {
			t.Fatalf("%s in maintenance mode: err = %v, want ErrInMaintenance", tc.name, err)
		}
	}

	if _, err := s.GetSession(t.Context(), "ses-new"); !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("get the refused session: err = %v, want ErrNotFound", err)
	}

	if _, err := s.GetToken(t.Context(), "tok-new"); !errors.Is(err, accesstoken.ErrNotFound) {
		t.Fatalf("get the refused token: err = %v, want ErrNotFound", err)
	}

	revPrincipalDisabled(t, s, revTempAdmin)

	if got := revAuditTotal(t, s); got != before {
		t.Fatalf("audit entries after the refused writes = %d, want %d", got, before)
	}
}

// testRevocationsHostileInput proves that the control path keeps its own
// checks as if the layers above it had failed: an invalid end reason, an
// invalid audit entry and an unknown or ended target each fail, change
// nothing and append no audit entry, also in maintenance mode.
func testRevocationsHostileInput(t *testing.T, s RevocationStore) {
	t.Helper()

	seedAccess(t, s)
	revEnter(t, s)

	ctx := t.Context()
	before := revAuditTotal(t, s)

	invalidEntry := revEntry(accesstoken.Ref(revPersonalToken), revAt(10))
	invalidEntry.Subject = ""

	otherScope := revEntry(accesstoken.Ref(revPersonalToken), revAt(10))
	otherScope.Namespace = "acme"

	cases := []struct {
		run  func() error
		want error
		name string
	}{
		{
			name: "an end reason outside the known set",
			run: func() error {
				return s.EndSession(ctx, revSessionOne, session.EndReason("granted"),
					revEntry(session.FullRef(revPerson, revSessionOne), revAt(10)))
			},
			want: session.ErrInvalid,
		},
		{
			name: "an audit entry without a subject",
			run:  func() error { return s.RevokeToken(ctx, revPersonalToken, invalidEntry) },
			want: audit.ErrInvalidEntry,
		},
		{
			name: "an audit entry of another scope",
			run:  func() error { return s.RevokeToken(ctx, revPersonalToken, otherScope) },
			want: audit.ErrInvalidEntry,
		},
		{
			name: "a session that does not exist",
			run: func() error {
				return s.EndSession(
					ctx,
					"ses-none",
					session.ReasonSignOut,
					revEntry(session.FullRef(revPerson, "ses-none"), revAt(10)),
				)
			},
			want: session.ErrNotFound,
		},
		{
			name: "a token that does not exist",
			run:  func() error { return s.RevokeToken(ctx, "tok-none", revEntry(accesstoken.Ref("tok-none"), revAt(10))) },
			want: accesstoken.ErrNotFound,
		},
		{
			name: "a temporary administrator that does not exist",
			run: func() error {
				return s.ExpireTempAdmin(ctx, "prn-none", revEntry(tempadmin.Ref("prn-none"), revAt(10)))
			},
			want: tempadmin.ErrNotFound,
		},
	}

	for _, tc := range cases {
		if err := tc.run(); !errors.Is(err, tc.want) {
			t.Fatalf("%s in maintenance mode: err = %v, want %v", tc.name, err, tc.want)
		}
	}

	revSessionActive(t, s, revSessionOne)
	revTokenActive(t, s, revPersonalToken)

	if got := revAuditTotal(t, s); got != before {
		t.Fatalf("audit entries after the refused writes = %d, want %d", got, before)
	}

	// A second end of one session is refused and appends no entry.
	end := revEntry(session.FullRef(revPerson, revSessionOne), revAt(11))
	if err := s.EndSession(ctx, revSessionOne, session.ReasonSignOut, end); err != nil {
		t.Fatalf("end the session: %v", err)
	}

	again := revAuditTotal(t, s)
	if err := s.EndSession(ctx, revSessionOne, session.ReasonSignOut, end); !errors.Is(err, session.ErrEnded) {
		t.Fatalf("end the session twice: err = %v, want ErrEnded", err)
	}

	if got := revAuditTotal(t, s); got != again {
		t.Fatalf("audit entries after the second end = %d, want %d", got, again)
	}
}

// revStageAndApply stages the export of s on s and applies it: a restore of
// the state that the export holds.
func revStageAndApply(t *testing.T, s RevocationStore, export revExport) {
	t.Helper()

	if _, err := s.StagePortable(t.Context(), export.sink.read, export.sum, export.files); err != nil {
		t.Fatalf("stage the export: %v", err)
	}

	if err := s.ApplyStaging(t.Context()); err != nil {
		t.Fatalf("apply the staged export: %v", err)
	}
}

// revExport holds one export of a store.
type revExport struct {
	sink  *portableSink
	sum   backup.Summary
	files []backup.PortableFile
}

// revTakeExport exports s.
func revTakeExport(t *testing.T, s RevocationStore) revExport {
	t.Helper()

	sink, sum, files := exportPortable(t, s)

	return revExport{sink: sink, sum: sum, files: files}
}

// testRevocationSurvivesRestore proves the restore interaction. A backup
// holds the access data from before the incident. An operator then enters
// maintenance mode and revokes a session, a token and a principal. The
// restore swaps in the older access data, and the swap applies the revocations
// of the stay again: the restored rows end, are revoked or are disabled,
// and the rows that nobody revoked stay as the backup holds them.
func testRevocationSurvivesRestore(t *testing.T, s RevocationStore) {
	t.Helper()

	seedAccess(t, s)

	export := revTakeExport(t, s)

	revEnter(t, s)

	endSession := revEntry(session.FullRef(revPerson, revSessionOne), revAt(30))
	if err := s.EndSession(t.Context(), revSessionOne, session.ReasonEnded, endSession); err != nil {
		t.Fatalf("end the session in maintenance mode: %v", err)
	}

	revoke := revEntry(accesstoken.Ref(revServiceToken), revAt(31))
	if err := s.RevokeToken(t.Context(), revServiceToken, revoke); err != nil {
		t.Fatalf("revoke the token in maintenance mode: %v", err)
	}

	expire := revEntry(tempadmin.Ref(revTempAdmin), revAt(32))
	if err := s.ExpireTempAdmin(t.Context(), revTempAdmin, expire); err != nil {
		t.Fatalf("disable the principal in maintenance mode: %v", err)
	}

	revStageAndApply(t, s, export)

	revSessionEnded(t, s, revSessionOne, session.ReasonEnded)
	revSessionActive(t, s, revSessionTwo)
	revTokenRevoked(t, s, revServiceToken)
	revTokenActive(t, s, revPersonalToken)
	revTokenActive(t, s, revKeepToken)
	revPrincipalDisabled(t, s, revTempAdmin)
	revTokenRevoked(t, s, revTempToken)
	revPrincipalEnabled(t, s, revPerson)

	if got, err := s.GetState(t.Context()); err != nil || got != maintenance.StateMaintenance {
		t.Fatalf("installation state after the restore = %q, %v, want maintenance without error", got, err)
	}
}

// testRevocationStayIsBounded proves what the swap applies again: the
// revocations of the stay that the restore runs in. A revocation made in
// normal operation, and a revocation of an earlier stay, belong to the
// access data that the restore replaces, so the restored rows come back as
// the backup holds them.
func testRevocationStayIsBounded(t *testing.T, s RevocationStore) {
	t.Helper()

	seedAccess(t, s)

	export := revTakeExport(t, s)

	// In normal operation: no journal row.
	normal := revEntry(accesstoken.Ref(revPersonalToken), revAt(40))
	if err := s.RevokeToken(t.Context(), revPersonalToken, normal); err != nil {
		t.Fatalf("revoke the token in normal operation: %v", err)
	}

	// In an earlier stay: the next enter starts a new stay.
	revEnter(t, s)

	earlier := revEntry(session.FullRef(revPerson, revSessionOne), revAt(41))
	if err := s.EndSession(t.Context(), revSessionOne, session.ReasonEnded, earlier); err != nil {
		t.Fatalf("end the session in the first stay: %v", err)
	}

	revLeave(t, s)
	revEnter(t, s)

	revStageAndApply(t, s, export)

	revTokenActive(t, s, revPersonalToken)
	revSessionActive(t, s, revSessionOne)
}
