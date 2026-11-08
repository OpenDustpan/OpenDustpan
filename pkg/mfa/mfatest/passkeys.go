package mfatest

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/localuser"
	"opendustpan.org/opendustpan/pkg/mfa"
	"opendustpan.org/opendustpan/pkg/principal"
)

// passkeyCase is one case of the PasskeyStore suite.
type passkeyCase struct {
	test func(t *testing.T, s PasskeyStore)
	name string
}

func passkeyCases() []passkeyCase {
	return []passkeyCase{
		{name: "an added passkey reads back by person and by credential ID with its audit entry", test: testAddReads},
		{
			name: "a passkey with an empty field or a name that the principal rules refuse fails with ErrInvalid",
			test: testAddInvalid,
		},
		{
			name: "a passkey of a principal that is not a person or does not exist fails with ErrInvalid",
			test: testAddNotPerson,
		},
		{name: "a credential ID that another person holds fails with ErrAlreadyExists", test: testAddTaken},
		{name: "an add above MaxPasskeys fails with ErrLimit and stores nothing", test: testAddLimit},
		{name: "two adds at the same time at the limit: one wins", test: testAddLimitRace},
		{name: "a passkey add or remove for the demo person fails with localuser.ErrDemoFixed", test: testDemoFixed},
		{
			name: "a lookup of an empty, a too long or an unknown credential ID fails with ErrNotFound",
			test: testLookupUnknown,
		},
		{name: "a use with a higher counter records the counter and the time", test: testUseHigher},
		{name: "a use with a counter of 0 on a passkey with a counter of 0 is no regression", test: testUseZero},
		{
			name: "a use with a lower or equal counter suspends the passkey, appends one entry and fails with " +
				"ErrCounterRegressed",
			test: testUseRegressed,
		},
		{name: "a use of a suspended passkey fails with ErrSuspended", test: testUseSuspended},
		{name: "a use of an unknown passkey fails with ErrNotFound", test: testUseUnknown},
		{
			name: "two uses with one counter at the same time: one passes and the other suspends the passkey",
			test: testUseRace,
		},
		{name: "a remove deletes the passkey and appends one entry", test: testRemove},
		{
			name: "a remove of the last passkey of a user with no password fails with ErrLastCredential",
			test: testRemoveLastNoPassword,
		},
		{name: "a remove of the last passkey of a user with a password deletes it", test: testRemoveLastWithPassword},
		{
			name: "a remove of the last passkey of a break-glass person fails with ErrLastCredential",
			test: testRemoveBreakGlassPasskey,
		},
		{
			name: "a remove of the last live passkey of a user with no password fails with ErrLastCredential when " +
				"the other passkey is suspended",
			test: testRemoveLastLive,
		},
		{
			name: "a remove of a suspended passkey of a user with no password deletes it while a live passkey stays",
			test: testRemoveSuspended,
		},
		{
			name: "a remove of a break-glass person whose passkeys are all suspended fails with ErrLastCredential",
			test: testRemoveAllSuspended,
		},
		{name: "a remove of a passkey of another person fails with ErrNotFound", test: testRemoveOther},
		{
			name: "a remove of the last passkey and of the password at the same time: one wins and the person " +
				"keeps a credential",
			test: testRemoveRace,
		},
		{name: "a challenge works once", test: testChallengeOnce},
		{
			name: "a sign-in challenge over the cap of open ones stores no row, and the cap counts sign-ins only",
			test: testSignInChallengeCap,
		},
		{
			name: "a source over its cap of open sign-in challenges stores no row, and another source still works",
			test: testSignInSourceCap,
		},
		{name: "a challenge with a hostile source fails with ErrInvalid", test: testChallengeSourceChecks},
		{
			name: "a site group over its cap of open sign-in challenges stores no row, and another group still works",
			test: testSourceGroupCap,
		},
		{
			name: "starts of one source at the same time: exactly the cap succeeds and the others get " +
				"ErrSourceChallengeLimit",
			test: testSourceCapRace,
		},
		{
			name: "a challenge after its expiry fails with ErrNotFound, also on a second take",
			test: testChallengeExpired,
		},
		{name: "two takes of one challenge at the same time: one wins", test: testChallengeRace},
		{name: "a challenge that fails a check fails with ErrInvalid and stores nothing", test: testChallengeInvalid},
		{
			name: "a challenge with a digest that another challenge holds fails with ErrAlreadyExists",
			test: testChallengeTaken,
		},
		{
			name: "a change with an entry of another scope fails with audit.ErrInvalidEntry and changes nothing",
			test: testOtherScope,
		},
		{name: "a change whose audit append fails changes nothing", test: testAppendFails},
	}
}

// at is a fixed, millisecond-precision instant: stores keep milliseconds.
func at(second int) time.Time {
	return time.Date(2026, time.November, 1, 9, 0, 0, 0, time.UTC).Add(time.Duration(second) * time.Second)
}

// entry returns a valid audit entry in the scope @server about object, at
// when, as a service builds it.
func entry(object string, when time.Time) audit.Entry {
	return audit.Entry{
		At:            when,
		Namespace:     audit.ServerScope,
		Subject:       "admin",
		IDPSubject:    "",
		TokenID:       "",
		Action:        "caller:manage-sign-in",
		Object:        object,
		Version:       "",
		ValuePath:     "",
		ValueCount:    nil,
		Changes:       nil,
		SourceIP:      "192.0.2.1",
		UserAgent:     "mfatest",
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

// refused returns an entry that the audit log refuses: it names no subject.
func refused(object string, when time.Time) audit.Entry {
	e := entry(object, when)
	e.Subject = ""

	return e
}

// otherScope returns an entry of a namespace, not of @server.
func otherScope(object string, when time.Time) audit.Entry {
	e := entry(object, when)
	e.Namespace = "team"

	return e
}

// hash returns a valid stored password hash. The suite never verifies it.
func hash(n int) localuser.PasswordHash {
	enc := base64.RawStdEncoding
	salt := localuser.DigestOf("salt " + strconv.Itoa(n))
	key := localuser.DigestOf("key " + strconv.Itoa(n))

	return localuser.PasswordHash(
		"$argon2id$v=19$m=19456,t=2,p=1$" + enc.EncodeToString(salt[:localuser.DefaultSaltBytes]) +
			"$" + enc.EncodeToString(key[:localuser.DefaultKeyBytes]),
	)
}

// principalOf returns the principal id of kind with the subject subject.
func principalOf(id, subject string, kind principal.Kind) principal.Principal {
	return principal.Principal{
		CreatedAt:   at(0),
		UpdatedAt:   at(0),
		DisabledAt:  time.Time{},
		ID:          id,
		Subject:     subject,
		DisplayName: "Name of " + id,
		Kind:        kind,
		Source:      principal.SourceLocal,
		Namespace:   "",
		GroupCount:  0,
		BreakGlass:  false,
	}
}

// mustPrincipal creates the principal p.
func mustPrincipal(t *testing.T, s PasskeyStore, p principal.Principal) {
	t.Helper()

	if err := s.CreatePrincipal(t.Context(), p, entry(principal.Ref(p.ID), at(0))); err != nil {
		t.Fatalf("create principal %q: %v", p.ID, err)
	}
}

// mustPerson creates the person prn-<n>.
func mustPerson(t *testing.T, s PasskeyStore, n int) string {
	t.Helper()

	id := "prn-" + strconv.Itoa(n)
	mustPrincipal(t, s, principalOf(id, "person"+strconv.Itoa(n), principal.KindPerson))

	return id
}

// mustLocalUser creates the person prn-<n> with the local user lus-<n> and
// no password.
func mustLocalUser(t *testing.T, s PasskeyStore, n int) string {
	t.Helper()

	id := strconv.Itoa(n)
	pid := "prn-" + id
	mustPrincipal(t, s, principalOf(pid, "local:lus-"+id, principal.KindPerson))

	u := localuser.User{
		CreatedAt:      at(1),
		PasswordSetAt:  time.Time{},
		ID:             "lus-" + id,
		PrincipalID:    pid,
		UserName:       "user" + id,
		CreatedBy:      "admin",
		ChangeRequired: false,
		Demo:           false,
	}

	if err := s.CreateUser(t.Context(), u, entry("local-user:"+u.ID, at(1))); err != nil {
		t.Fatalf("create local user %s: %v", u.ID, err)
	}

	return pid
}

// mustPasswordUser creates the person prn-<n> with the local user lus-<n>
// and a password.
func mustPasswordUser(t *testing.T, s PasskeyStore, n int) string {
	t.Helper()

	pid := mustLocalUser(t, s, n)
	userID := "lus-" + strconv.Itoa(n)
	change := localuser.PasswordChange{ChangeRequired: false, FromGeneration: 0}

	if _, err := s.SetPassword(t.Context(), userID, hash(n), change, entry("local-user:"+userID, at(2))); err != nil {
		t.Fatalf("set password of %s: %v", userID, err)
	}

	return pid
}

// credential returns the credential ID of the passkey n.
func credential(n int) []byte {
	return []byte("credential-" + strconv.Itoa(n))
}

// passkeyOf returns a valid passkey pky-<n> of the person personID, with
// the counter 5.
func passkeyOf(n int, personID string) mfa.Passkey {
	return mfa.Passkey{
		CreatedAt:      at(10 + n),
		LastUsedAt:     time.Time{},
		SuspendedAt:    time.Time{},
		ID:             "pky-" + strconv.Itoa(n),
		PersonID:       personID,
		Name:           "key-" + strconv.Itoa(n),
		CredentialID:   credential(n),
		PublicKey:      []byte("cose-key-" + strconv.Itoa(n)),
		Transports:     []string{mfa.TransportUSB, mfa.TransportNFC},
		AAGUID:         [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
		SignCount:      5,
		BackupEligible: n%2 == 0,
	}
}

// passkeyObject returns the audit object of the passkey n.
func passkeyObject(n int) string {
	return "passkey:pky-" + strconv.Itoa(n)
}

// mustAdd adds the passkey n of the person personID.
func mustAdd(t *testing.T, s PasskeyStore, n int, personID string) mfa.Passkey {
	t.Helper()

	p := passkeyOf(n, personID)
	if err := s.AddPasskey(t.Context(), p, entry(passkeyObject(n), at(10+n))); err != nil {
		t.Fatalf("add passkey %d: %v", n, err)
	}

	return p
}

// entries returns the entries of @server whose object has the prefix.
func entries(t *testing.T, s PasskeyStore, prefix string) []audit.Entry {
	t.Helper()

	listing, err := s.List(t.Context(), audit.ServerScope, audit.Page{Number: 1, Size: audit.MaxPageSize})
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}

	var out []audit.Entry

	for i := range listing.Entries {
		if strings.HasPrefix(listing.Entries[i].Object, prefix) {
			out = append(out, listing.Entries[i])
		}
	}

	return out
}

// wantEntries fails the test when the count of entries about object is not n.
func wantEntries(t *testing.T, s PasskeyStore, object string, n int) {
	t.Helper()

	if got := len(entries(t, s, object)); got != n {
		t.Fatalf("entries about %s = %d, want %d", object, got, n)
	}
}

// wantErr fails the test when err does not match want.
func wantErr(t *testing.T, what string, err, want error) {
	t.Helper()

	if !errors.Is(err, want) {
		t.Fatalf("%s: err = %v, want %v", what, err, want)
	}
}

// count returns the count of passkeys of the person personID.
func count(t *testing.T, s PasskeyStore, personID string) int {
	t.Helper()

	ps, err := s.Passkeys(t.Context(), personID)
	if err != nil {
		t.Fatalf("passkeys of %s: %v", personID, err)
	}

	return len(ps)
}

// samePasskey fails the test when got differs from want.
func samePasskey(t *testing.T, what string, got, want mfa.Passkey) {
	t.Helper()

	if !sameKey(got, want) || !sameState(got, want) {
		t.Fatalf("%s = %+v, want %+v", what, got, want)
	}
}

// sameKey reports whether a and b hold the same owner, name and key.
func sameKey(a, b mfa.Passkey) bool {
	return a.ID == b.ID && a.PersonID == b.PersonID && a.Name == b.Name &&
		bytes.Equal(a.CredentialID, b.CredentialID) && bytes.Equal(a.PublicKey, b.PublicKey) &&
		strings.Join(a.Transports, ",") == strings.Join(b.Transports, ",") && a.AAGUID == b.AAGUID
}

// sameState reports whether a and b hold the same counter, flags and times.
func sameState(a, b mfa.Passkey) bool {
	return a.SignCount == b.SignCount && a.BackupEligible == b.BackupEligible &&
		a.CreatedAt.Equal(b.CreatedAt) && a.LastUsedAt.Equal(b.LastUsedAt) && a.SuspendedAt.Equal(b.SuspendedAt)
}

// byCredential returns the passkey of the credential ID of n.
func byCredential(t *testing.T, s PasskeyStore, n int) mfa.Passkey {
	t.Helper()

	p, err := s.PasskeyByCredentialID(t.Context(), credential(n))
	if err != nil {
		t.Fatalf("passkey by credential ID %d: %v", n, err)
	}

	return p
}

func testAddReads(t *testing.T, s PasskeyStore) {
	t.Helper()

	pid := mustPerson(t, s, 1)
	first := mustAdd(t, s, 1, pid)
	mustAdd(t, s, 2, pid)

	if err := s.AddPasskey(t.Context(), passkeyOf(3, pid), entry(passkeyObject(3), at(13))); err != nil {
		t.Fatalf("add passkey 3: %v", err)
	}

	ps, err := s.Passkeys(t.Context(), pid)
	if err != nil {
		t.Fatalf("passkeys: %v", err)
	}

	if len(ps) != 3 {
		t.Fatalf("passkeys = %d, want 3", len(ps))
	}

	samePasskey(t, "first passkey", ps[0], first)
	samePasskey(t, "by credential ID", byCredential(t, s, 2), passkeyOf(2, pid))

	if other := count(t, s, "prn-none"); other != 0 {
		t.Fatalf("passkeys of a person with none = %d, want 0", other)
	}

	wantEntries(t, s, passkeyObject(1), 1)
}

func testAddInvalid(t *testing.T, s PasskeyStore) {
	t.Helper()

	pid := mustPerson(t, s, 1)

	cases := []struct {
		change func(p *mfa.Passkey)
		name   string
	}{
		{name: "no prefix", change: func(p *mfa.Passkey) { p.ID = "key-1" }},
		{name: "no person", change: func(p *mfa.Passkey) { p.PersonID = "" }},
		{name: "empty name", change: func(p *mfa.Passkey) { p.Name = "" }},
		{name: "name with a space", change: func(p *mfa.Passkey) { p.Name = "my key" }},
		{name: "name with a quote", change: func(p *mfa.Passkey) { p.Name = "key'; DROP TABLE passkeys; --" }},
		{name: "name with a line break", change: func(p *mfa.Passkey) { p.Name = "key\nforged" }},
		{name: "name starts with a dot", change: func(p *mfa.Passkey) { p.Name = ".key" }},
		{name: "long name", change: func(p *mfa.Passkey) { p.Name = strings.Repeat("a", mfa.MaxPasskeyNameLength+1) }},
		{name: "no credential ID", change: func(p *mfa.Passkey) { p.CredentialID = nil }},
		{name: "long credential ID", change: func(p *mfa.Passkey) {
			p.CredentialID = bytes.Repeat([]byte{1}, mfa.MaxCredentialIDBytes+1)
		}},
		{name: "no public key", change: func(p *mfa.Passkey) { p.PublicKey = nil }},
		{name: "long public key", change: func(p *mfa.Passkey) {
			p.PublicKey = bytes.Repeat([]byte{1}, mfa.MaxPublicKeyBytes+1)
		}},
		{name: "unknown transport", change: func(p *mfa.Passkey) { p.Transports = []string{"carrier-pigeon"} }},
		{name: "transport with a comma", change: func(p *mfa.Passkey) { p.Transports = []string{"usb,nfc"} }},
		{name: "transport twice", change: func(p *mfa.Passkey) { p.Transports = []string{"usb", "usb"} }},
		{name: "no time", change: func(p *mfa.Passkey) { p.CreatedAt = time.Time{} }},
		{name: "a use already", change: func(p *mfa.Passkey) { p.LastUsedAt = at(20) }},
		{name: "suspended already", change: func(p *mfa.Passkey) { p.SuspendedAt = at(20) }},
	}

	for i, c := range cases {
		p := passkeyOf(i+1, pid)
		c.change(&p)

		err := s.AddPasskey(t.Context(), p, entry(passkeyObject(i+1), at(30)))
		wantErr(t, c.name, err, mfa.ErrInvalid)
	}

	if n := count(t, s, pid); n != 0 {
		t.Fatalf("passkeys after refused adds = %d, want 0", n)
	}

	wantEntries(t, s, "passkey:", 0)
}

func testAddNotPerson(t *testing.T, s PasskeyStore) {
	t.Helper()

	mustPrincipal(t, s, principalOf("prn-svc", "service-1", principal.KindService))

	err := s.AddPasskey(t.Context(), passkeyOf(1, "prn-svc"), entry(passkeyObject(1), at(11)))
	wantErr(t, "add for a service", err, mfa.ErrInvalid)

	err = s.AddPasskey(t.Context(), passkeyOf(2, "prn-missing"), entry(passkeyObject(2), at(12)))
	wantErr(t, "add for no principal", err, mfa.ErrInvalid)

	wantEntries(t, s, "passkey:", 0)
}

func testAddTaken(t *testing.T, s PasskeyStore) {
	t.Helper()

	first := mustPerson(t, s, 1)
	second := mustPerson(t, s, 2)
	mustAdd(t, s, 1, first)

	p := passkeyOf(2, second)
	p.CredentialID = credential(1)

	err := s.AddPasskey(t.Context(), p, entry(passkeyObject(2), at(12)))
	wantErr(t, "add a credential ID of another person", err, mfa.ErrAlreadyExists)

	same := passkeyOf(1, second)
	same.CredentialID = credential(9)

	err = s.AddPasskey(t.Context(), same, entry(passkeyObject(1), at(12)))
	wantErr(t, "add a passkey ID that exists", err, mfa.ErrAlreadyExists)

	if n := count(t, s, second); n != 0 {
		t.Fatalf("passkeys of the second person = %d, want 0", n)
	}

	if got := byCredential(t, s, 1); got.PersonID != first {
		t.Fatalf("credential owner = %s, want %s", got.PersonID, first)
	}
}

func testAddLimit(t *testing.T, s PasskeyStore) {
	t.Helper()

	pid := mustPerson(t, s, 1)

	for n := 1; n <= mfa.MaxPasskeys; n++ {
		mustAdd(t, s, n, pid)
	}

	n := mfa.MaxPasskeys + 1
	err := s.AddPasskey(t.Context(), passkeyOf(n, pid), entry(passkeyObject(n), at(n)))
	wantErr(t, "add above the limit", err, mfa.ErrLimit)

	if got := count(t, s, pid); got != mfa.MaxPasskeys {
		t.Fatalf("passkeys = %d, want %d", got, mfa.MaxPasskeys)
	}

	if _, err := s.PasskeyByCredentialID(t.Context(), credential(n)); !errors.Is(err, mfa.ErrNotFound) {
		t.Fatalf("passkey above the limit: err = %v, want ErrNotFound", err)
	}
}

func testAddLimitRace(t *testing.T, s PasskeyStore) {
	t.Helper()

	pid := mustPerson(t, s, 1)

	for n := 1; n < mfa.MaxPasskeys; n++ {
		mustAdd(t, s, n, pid)
	}

	const racers = 4

	errs := make([]error, racers)

	var wg sync.WaitGroup

	for i := range racers {
		wg.Go(func() {
			n := mfa.MaxPasskeys + i
			errs[i] = s.AddPasskey(
				context.WithoutCancel(t.Context()),
				passkeyOf(n, pid),
				entry(passkeyObject(n), at(n)),
			)
		})
	}

	wg.Wait()

	passed := 0

	for _, err := range errs {
		switch {
		case err == nil:
			passed++
		case errors.Is(err, mfa.ErrLimit):
		default:
			t.Fatalf("add at the limit: err = %v, want nil or ErrLimit", err)
		}
	}

	if passed != 1 {
		t.Fatalf("adds that passed = %d, want 1", passed)
	}

	if got := count(t, s, pid); got != mfa.MaxPasskeys {
		t.Fatalf("passkeys = %d, want %d", got, mfa.MaxPasskeys)
	}
}

func testDemoFixed(t *testing.T, s PasskeyStore) {
	t.Helper()

	if _, err := s.ClaimKind(t.Context(), installation.KindDev); err != nil {
		t.Fatalf("claim kind: %v", err)
	}

	mustPrincipal(t, s, principalOf("prn-demo", localuser.DemoSubject, principal.KindPerson))

	if err := s.SeedDemoPassword(t.Context(), hash(1), entry("local-user:demo", at(2))); err != nil {
		t.Fatalf("seed the demo password: %v", err)
	}

	err := s.AddPasskey(t.Context(), passkeyOf(1, "prn-demo"), entry(passkeyObject(1), at(11)))
	wantErr(t, "add a passkey for the demo person", err, localuser.ErrDemoFixed)

	err = s.RemovePasskey(t.Context(), "prn-demo", "pky-1", entry(passkeyObject(1), at(12)))
	if !errors.Is(err, localuser.ErrDemoFixed) && !errors.Is(err, mfa.ErrNotFound) {
		t.Fatalf("remove a passkey of the demo person: err = %v, want ErrDemoFixed or ErrNotFound", err)
	}

	wantEntries(t, s, "passkey:", 0)
}

func testLookupUnknown(t *testing.T, s PasskeyStore) {
	t.Helper()

	pid := mustPerson(t, s, 1)
	mustAdd(t, s, 1, pid)

	for _, id := range [][]byte{
		nil,
		{},
		bytes.Repeat([]byte{1}, mfa.MaxCredentialIDBytes+1), []byte("credential-2"),
		[]byte("credential-1' OR '1'='1"), []byte("credential-%"),
	} {
		if _, err := s.PasskeyByCredentialID(t.Context(), id); !errors.Is(err, mfa.ErrNotFound) {
			t.Fatalf("lookup %q: err = %v, want ErrNotFound", id, err)
		}
	}
}

// use returns the use of the passkey n with the counter c at second sec.
func use(n int, c uint32, sec int) mfa.PasskeyUse {
	return mfa.PasskeyUse{At: at(sec), PasskeyID: "pky-" + strconv.Itoa(n), SignCount: c}
}

// regressedEntry returns the entry of a suspension of the passkey n.
func regressedEntry(n, sec int) audit.Entry {
	e := entry(passkeyObject(n), at(sec))
	e.Kind = audit.KindSecurity
	e.Action = "passkey.suspended"

	return e
}

func testUseHigher(t *testing.T, s PasskeyStore) {
	t.Helper()

	pid := mustPerson(t, s, 1)
	p := mustAdd(t, s, 5, pid)

	if err := s.RecordUse(t.Context(), use(5, 6, 100), regressedEntry(5, 100)); err != nil {
		t.Fatalf("use: %v", err)
	}

	p.SignCount = 6
	p.LastUsedAt = at(100)
	samePasskey(t, "passkey after a use", byCredential(t, s, 5), p)
	wantEntries(t, s, passkeyObject(5), 1)
}

func testUseZero(t *testing.T, s PasskeyStore) {
	t.Helper()

	pid := mustPerson(t, s, 1)
	p := passkeyOf(1, pid)
	p.SignCount = 0

	if err := s.AddPasskey(t.Context(), p, entry(passkeyObject(1), at(11))); err != nil {
		t.Fatalf("add: %v", err)
	}

	for sec := 100; sec < 103; sec++ {
		if err := s.RecordUse(t.Context(), use(1, 0, sec), regressedEntry(1, sec)); err != nil {
			t.Fatalf("use with a counter of 0: %v", err)
		}
	}

	if got := byCredential(t, s, 1); !got.SuspendedAt.IsZero() || !got.LastUsedAt.Equal(at(102)) {
		t.Fatalf("passkey after uses with 0 = %+v, want not suspended and used at %s", got, at(102))
	}
}

func testUseRegressed(t *testing.T, s PasskeyStore) {
	t.Helper()

	cases := []struct {
		name    string
		counter uint32
	}{
		{name: "equal", counter: 5},
		{name: "lower", counter: 4},
		{name: "zero after a counter", counter: 0},
	}

	pid := mustPerson(t, s, 1)

	for i, c := range cases {
		n := 5 + i*10
		p := passkeyOf(n, pid)
		p.SignCount = 5

		if err := s.AddPasskey(t.Context(), p, entry(passkeyObject(n), at(11))); err != nil {
			t.Fatalf("%s: add: %v", c.name, err)
		}

		err := s.RecordUse(t.Context(), use(n, c.counter, 100), regressedEntry(n, 100))
		wantErr(t, c.name, err, mfa.ErrCounterRegressed)

		got := byCredential(t, s, n)
		if !got.SuspendedAt.Equal(at(100)) || got.SignCount != 5 || !got.LastUsedAt.IsZero() {
			t.Fatalf("%s: passkey after a regression = %+v, want suspended at %s with counter 5", c.name, got, at(100))
		}

		suspended := entries(t, s, passkeyObject(n))
		if len(suspended) != 2 || suspended[0].Kind != audit.KindSecurity {
			t.Fatalf("%s: entries = %+v, want the add and one security entry", c.name, suspended)
		}
	}
}

func testUseSuspended(t *testing.T, s PasskeyStore) {
	t.Helper()

	pid := mustPerson(t, s, 1)
	mustAdd(t, s, 5, pid)

	err := s.RecordUse(t.Context(), use(5, 1, 100), regressedEntry(5, 100))
	wantErr(t, "regression", err, mfa.ErrCounterRegressed)

	err = s.RecordUse(t.Context(), use(5, 50, 101), regressedEntry(5, 101))
	wantErr(t, "use after the suspension", err, mfa.ErrSuspended)

	if got := byCredential(t, s, 5); got.SignCount != 5 || !got.SuspendedAt.Equal(at(100)) {
		t.Fatalf("passkey = %+v, want counter 5 and suspended at %s", got, at(100))
	}

	wantEntries(t, s, passkeyObject(5), 2)
}

func testUseUnknown(t *testing.T, s PasskeyStore) {
	t.Helper()

	err := s.RecordUse(t.Context(), use(9, 1, 100), regressedEntry(9, 100))
	wantErr(t, "use of no passkey", err, mfa.ErrNotFound)

	err = s.RecordUse(t.Context(), mfa.PasskeyUse{At: at(1), PasskeyID: "", SignCount: 1}, regressedEntry(9, 100))
	wantErr(t, "use with no ID", err, mfa.ErrInvalid)

	wantEntries(t, s, "passkey:", 0)
}

func testUseRace(t *testing.T, s PasskeyStore) {
	t.Helper()

	pid := mustPerson(t, s, 1)
	mustAdd(t, s, 5, pid)

	const racers = 4

	errs := make([]error, racers)

	var wg sync.WaitGroup

	for i := range racers {
		wg.Go(func() {
			errs[i] = s.RecordUse(context.WithoutCancel(t.Context()), use(5, 6, 100+i), regressedEntry(5, 100+i))
		})
	}

	wg.Wait()

	passed := 0

	for _, err := range errs {
		switch {
		case err == nil:
			passed++
		case errors.Is(err, mfa.ErrCounterRegressed), errors.Is(err, mfa.ErrSuspended):
		default:
			t.Fatalf("use: err = %v, want nil, ErrCounterRegressed or ErrSuspended", err)
		}
	}

	if passed != 1 {
		t.Fatalf("uses that passed = %d, want 1", passed)
	}

	if got := byCredential(t, s, 5); got.SuspendedAt.IsZero() || got.SignCount != 6 {
		t.Fatalf("passkey = %+v, want suspended with counter 6", got)
	}
}

func testRemove(t *testing.T, s PasskeyStore) {
	t.Helper()

	pid := mustPerson(t, s, 1)
	mustAdd(t, s, 1, pid)
	mustAdd(t, s, 2, pid)

	if err := s.RemovePasskey(t.Context(), pid, "pky-1", entry(passkeyObject(1), at(50))); err != nil {
		t.Fatalf("remove: %v", err)
	}

	if _, err := s.PasskeyByCredentialID(t.Context(), credential(1)); !errors.Is(err, mfa.ErrNotFound) {
		t.Fatalf("removed passkey: err = %v, want ErrNotFound", err)
	}

	if n := count(t, s, pid); n != 1 {
		t.Fatalf("passkeys = %d, want 1", n)
	}

	wantEntries(t, s, passkeyObject(1), 2)

	err := s.RemovePasskey(t.Context(), pid, "pky-1", entry(passkeyObject(1), at(51)))
	wantErr(t, "second remove", err, mfa.ErrNotFound)
	wantEntries(t, s, passkeyObject(1), 2)
}

func testRemoveLastNoPassword(t *testing.T, s PasskeyStore) {
	t.Helper()

	pid := mustLocalUser(t, s, 1)
	mustAdd(t, s, 1, pid)
	mustAdd(t, s, 2, pid)

	if err := s.RemovePasskey(t.Context(), pid, "pky-1", entry(passkeyObject(1), at(50))); err != nil {
		t.Fatalf("remove the first of two: %v", err)
	}

	err := s.RemovePasskey(t.Context(), pid, "pky-2", entry(passkeyObject(2), at(51)))
	wantErr(t, "remove the last", err, mfa.ErrLastCredential)

	if n := count(t, s, pid); n != 1 {
		t.Fatalf("passkeys = %d, want 1", n)
	}

	wantEntries(t, s, passkeyObject(2), 1)
}

func testRemoveLastWithPassword(t *testing.T, s PasskeyStore) {
	t.Helper()

	pid := mustPasswordUser(t, s, 1)
	mustAdd(t, s, 1, pid)

	if err := s.RemovePasskey(t.Context(), pid, "pky-1", entry(passkeyObject(1), at(50))); err != nil {
		t.Fatalf("remove the last passkey of a user with a password: %v", err)
	}

	if n := count(t, s, pid); n != 0 {
		t.Fatalf("passkeys = %d, want 0", n)
	}

	other := mustPerson(t, s, 2)
	mustAdd(t, s, 2, other)

	if err := s.RemovePasskey(t.Context(), other, "pky-2", entry(passkeyObject(2), at(51))); err != nil {
		t.Fatalf("remove the last passkey of a person with no local user: %v", err)
	}
}

func testRemoveBreakGlassPasskey(t *testing.T, s PasskeyStore) {
	t.Helper()

	bg := principalOf("prn-bg", "break-glass-1", principal.KindPerson)
	bg.BreakGlass = true
	mustPrincipal(t, s, bg)
	mustAdd(t, s, 1, "prn-bg")

	err := s.RemovePasskey(t.Context(), "prn-bg", "pky-1", entry(passkeyObject(1), at(50)))
	wantErr(t, "remove the last passkey of a break-glass person", err, mfa.ErrLastCredential)

	if n := count(t, s, "prn-bg"); n != 1 {
		t.Fatalf("passkeys = %d, want 1", n)
	}
}

// mustSuspend suspends the passkey n with a counter that goes back.
func mustSuspend(t *testing.T, s PasskeyStore, n int) {
	t.Helper()

	err := s.RecordUse(t.Context(), use(n, 1, 90), regressedEntry(n, 90))
	wantErr(t, "suspend passkey "+strconv.Itoa(n), err, mfa.ErrCounterRegressed)
}

func testRemoveLastLive(t *testing.T, s PasskeyStore) {
	t.Helper()

	pid := mustLocalUser(t, s, 1)
	mustAdd(t, s, 1, pid)
	mustAdd(t, s, 2, pid)
	mustSuspend(t, s, 1)

	err := s.RemovePasskey(t.Context(), pid, "pky-2", entry(passkeyObject(2), at(51)))
	wantErr(t, "remove the live passkey next to a suspended one", err, mfa.ErrLastCredential)

	if n := count(t, s, pid); n != 2 {
		t.Fatalf("passkeys = %d, want 2", n)
	}

	wantEntries(t, s, passkeyObject(2), 1)
}

func testRemoveSuspended(t *testing.T, s PasskeyStore) {
	t.Helper()

	pid := mustLocalUser(t, s, 1)
	mustAdd(t, s, 1, pid)
	mustAdd(t, s, 2, pid)
	mustSuspend(t, s, 1)

	if err := s.RemovePasskey(t.Context(), pid, "pky-1", entry(passkeyObject(1), at(51))); err != nil {
		t.Fatalf("remove the suspended passkey: %v", err)
	}

	if got := byCredential(t, s, 2); !got.SuspendedAt.IsZero() {
		t.Fatalf("passkey that stays = %+v, want the live one", got)
	}
}

func testRemoveAllSuspended(t *testing.T, s PasskeyStore) {
	t.Helper()

	bg := principalOf("prn-bg", "break-glass-1", principal.KindPerson)
	bg.BreakGlass = true
	mustPrincipal(t, s, bg)
	mustAdd(t, s, 1, "prn-bg")
	mustAdd(t, s, 2, "prn-bg")
	mustSuspend(t, s, 1)
	mustSuspend(t, s, 2)

	for _, id := range []string{"pky-1", "pky-2"} {
		err := s.RemovePasskey(t.Context(), "prn-bg", id, entry("passkey:"+id, at(51)))
		wantErr(t, "remove "+id+" when all are suspended", err, mfa.ErrLastCredential)
	}

	if n := count(t, s, "prn-bg"); n != 2 {
		t.Fatalf("passkeys = %d, want 2", n)
	}
}

func testRemoveOther(t *testing.T, s PasskeyStore) {
	t.Helper()

	first := mustPerson(t, s, 1)
	second := mustPerson(t, s, 2)
	mustAdd(t, s, 1, first)
	mustAdd(t, s, 2, second)

	err := s.RemovePasskey(t.Context(), second, "pky-1", entry(passkeyObject(1), at(50)))
	wantErr(t, "remove a passkey of another person", err, mfa.ErrNotFound)

	err = s.RemovePasskey(t.Context(), "prn-missing", "pky-1", entry(passkeyObject(1), at(50)))
	wantErr(t, "remove for no principal", err, mfa.ErrNotFound)

	if n := count(t, s, first); n != 1 {
		t.Fatalf("passkeys of the owner = %d, want 1", n)
	}

	wantEntries(t, s, passkeyObject(1), 1)
}

func testRemoveRace(t *testing.T, s PasskeyStore) {
	t.Helper()

	pid := mustPasswordUser(t, s, 1)
	mustAdd(t, s, 1, pid)

	var (
		wg                 sync.WaitGroup
		passkeyErr, pwdErr error
	)

	ctx := context.WithoutCancel(t.Context())

	wg.Go(func() { passkeyErr = s.RemovePasskey(ctx, pid, "pky-1", entry(passkeyObject(1), at(50))) })
	wg.Go(func() { pwdErr = s.RemovePassword(ctx, "lus-1", entry("local-user:lus-1", at(50))) })
	wg.Wait()

	if passkeyErr == nil && pwdErr == nil {
		t.Fatal("both removals passed, want one to fail")
	}

	keeps := count(t, s, pid) > 0

	c, err := s.UserByName(t.Context(), "user1")
	if err != nil {
		t.Fatalf("user: %v", err)
	}

	if !keeps && c.Hash == "" {
		t.Fatalf("the person has no credential after the removals (passkey: %v, password: %v)", passkeyErr, pwdErr)
	}
}

// digest returns the digest of the challenge n.
func digest(n int) mfa.Digest {
	return mfa.DigestOf([]byte("challenge " + strconv.Itoa(n)))
}

// defaultSource is the source of a sign-in challenge that challengeOf makes.
// The fill of the installation uses other sources, so this one has room.
const defaultSource = "192.0.2.1"

// challengeFrom returns a sign-in challenge from the source that starts at
// second sec.
// Its site group is its source, so that each source is its own group.
func challengeFrom(source string, sec int) mfa.Challenge {
	return challengeIn(source, source, sec)
}

// challengeIn returns a sign-in challenge from the source in the site group
// that starts at second sec.
func challengeIn(source, group string, sec int) mfa.Challenge {
	c := challengeOf(mfa.CeremonySignIn, sec)
	c.Source, c.SourceGroup = source, group

	return c
}

// challengeOf returns a challenge of the kind k that starts at second sec.
func challengeOf(k mfa.Ceremony, sec int) mfa.Challenge {
	c := mfa.Challenge{
		CreatedAt: at(sec),
		ExpiresAt: mfa.ChallengeExpiry(at(sec)),
		Kind:      k,
		PersonID:  "",
		PendingID: "",
		SessionID: "",
		Source:    "",

		SourceGroup: "",
	}

	if k == mfa.CeremonySignIn {
		c.Source, c.SourceGroup = defaultSource, defaultSource
	}

	if k == mfa.CeremonyRegister || k == mfa.CeremonyFresh {
		c.PersonID, c.SessionID = "prn-1", "ses-1"
	}

	if k == mfa.CeremonySecondFactor {
		c.PersonID, c.PendingID = "prn-1", "psi-1"
	}

	return c
}

// testSignInChallengeCap fills the installation with the most open sign-in
// challenges. The next sign-in challenge is refused with ErrChallengeLimit and
// stores no row. A challenge of another kind still works. When one open
// challenge ends, a new sign-in challenge works again, and a challenge that
// expired does not count.
func testSignInChallengeCap(t *testing.T, s PasskeyStore) {
	t.Helper()

	ctx := t.Context()

	// Many sources, each one under its own count, fill the installation.
	for i := range mfa.MaxOpenSignInChallenges {
		source := "10.0." + strconv.Itoa(i/mfa.MaxOpenSignInChallengesPerSource)

		if err := s.CreateChallenge(ctx, challengeFrom(source, 0), digest(i)); err != nil {
			t.Fatalf("sign-in challenge %d: %v", i, err)
		}
	}

	over := digest(mfa.MaxOpenSignInChallenges)

	err := s.CreateChallenge(ctx, challengeOf(mfa.CeremonySignIn, 1), over)
	if !errors.Is(err, mfa.ErrChallengeLimit) || !errors.Is(err, mfa.ErrUnavailable) {
		t.Fatalf("sign-in challenge over the cap = %v, want ErrChallengeLimit", err)
	}

	if _, err := s.TakeChallenge(ctx, over, at(2)); !errors.Is(err, mfa.ErrNotFound) {
		t.Fatalf("take of the refused challenge = %v, want ErrNotFound: the refusal stored a row", err)
	}

	if err := s.CreateChallenge(ctx, challengeOf(mfa.CeremonyFresh, 1), digest(-1)); err != nil {
		t.Fatalf("fresh challenge at the cap: %v", err)
	}

	if _, err := s.TakeChallenge(ctx, digest(0), at(2)); err != nil {
		t.Fatalf("take of an open challenge: %v", err)
	}

	if err := s.CreateChallenge(ctx, challengeOf(mfa.CeremonySignIn, 1), over); err != nil {
		t.Fatalf("sign-in challenge after one ended: %v", err)
	}

	// Every challenge expired after their lifetime, so a new one works.
	late := int(mfa.ChallengeExpiry(at(0)).Sub(at(0)).Seconds()) + 5
	lateDigest := digest(mfa.MaxOpenSignInChallenges + 1)

	if err := s.CreateChallenge(ctx, challengeOf(mfa.CeremonySignIn, late), lateDigest); err != nil {
		t.Fatalf("sign-in challenge after the others expired: %v", err)
	}
}

// testSignInSourceCap fills one source with the most open sign-in challenges
// that one source can hold. The next challenge of that source is refused with
// ErrSourceChallengeLimit, which is not ErrChallengeLimit, and stores no row.
// Another source, and a challenge of another kind, still work.
func testSignInSourceCap(t *testing.T, s PasskeyStore) {
	t.Helper()

	ctx := t.Context()
	flood := "198.51.100.7"

	for i := range mfa.MaxOpenSignInChallengesPerSource {
		if err := s.CreateChallenge(ctx, challengeFrom(flood, 0), digest(i)); err != nil {
			t.Fatalf("sign-in challenge %d: %v", i, err)
		}
	}

	over := digest(mfa.MaxOpenSignInChallengesPerSource)

	err := s.CreateChallenge(ctx, challengeFrom(flood, 1), over)
	if !errors.Is(err, mfa.ErrSourceChallengeLimit) || errors.Is(err, mfa.ErrChallengeLimit) ||
		errors.Is(err, mfa.ErrUnavailable) {
		t.Fatalf("challenge over the count of a source = %v, want ErrSourceChallengeLimit", err)
	}

	if _, err := s.TakeChallenge(ctx, over, at(2)); !errors.Is(err, mfa.ErrNotFound) {
		t.Fatalf("take of the refused challenge = %v, want ErrNotFound: the refusal stored a row", err)
	}

	for i, other := range []string{"198.51.100.8", "2001:db8:0:1::/64"} {
		if err := s.CreateChallenge(ctx, challengeFrom(other, 1), digest(100+i)); err != nil {
			t.Fatalf("challenge of another source %s: %v", other, err)
		}
	}

	if err := s.CreateChallenge(ctx, challengeOf(mfa.CeremonyFresh, 1), digest(-1)); err != nil {
		t.Fatalf("fresh challenge while a source is at its count: %v", err)
	}

	testSourceRecovers(t, s, flood, over)
}

// testSourceGroupCap fills one site group (the /48 of an IPv6 address) with
// the most open sign-in challenges that one group can hold, from several
// sources (the /64 ranges) that each stay under their own count. The next
// challenge of the group is refused with ErrSourceChallengeLimit, from a new
// source too, and stores no row. Another group still works, and the group
// works again when one challenge ends.
func testSourceGroupCap(t *testing.T, s PasskeyStore) {
	t.Helper()

	ctx := t.Context()
	group := "2001:db8::/48"
	perGroup := mfa.MaxOpenSignInChallengesPerSourceGroup
	n := 0

	for n < perGroup {
		source := "2001:db8:0:" + strconv.Itoa(n/mfa.MaxOpenSignInChallengesPerSource) + "::/64"

		if err := s.CreateChallenge(ctx, challengeIn(source, group, 0), digest(n)); err != nil {
			t.Fatalf("challenge %d of the group: %v", n, err)
		}

		n++
	}

	over := digest(n)

	err := s.CreateChallenge(ctx, challengeIn("2001:db8:0:ffff::/64", group, 1), over)
	if !errors.Is(err, mfa.ErrSourceChallengeLimit) || errors.Is(err, mfa.ErrUnavailable) {
		t.Fatalf("challenge over the count of a group = %v, want ErrSourceChallengeLimit", err)
	}

	if _, err := s.TakeChallenge(ctx, over, at(2)); !errors.Is(err, mfa.ErrNotFound) {
		t.Fatalf("take of the refused challenge = %v, want ErrNotFound: the refusal stored a row", err)
	}

	if err := s.CreateChallenge(ctx, challengeIn("2001:db9:0:1::/64", "2001:db9::/48", 1), digest(n+1)); err != nil {
		t.Fatalf("challenge of another group: %v", err)
	}

	if _, err := s.TakeChallenge(ctx, digest(0), at(2)); err != nil {
		t.Fatalf("take of an open challenge: %v", err)
	}

	if err := s.CreateChallenge(ctx, challengeIn("2001:db8:0:ffff::/64", group, 2), over); err != nil {
		t.Fatalf("challenge after one of the group ended: %v", err)
	}
}

// testSourceCapRace sends twice the count of one source at the same time. The
// count and the insert of a start are one step, so exactly the count succeeds
// and each other start gets ErrSourceChallengeLimit. The same step covers the
// total of the installation.
func testSourceCapRace(t *testing.T, s PasskeyStore) {
	t.Helper()

	const sends = 2 * mfa.MaxOpenSignInChallengesPerSource

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		ok      int
		refused int
		other   []error
	)

	for i := range sends {
		wg.Go(func() {
			err := s.CreateChallenge(t.Context(), challengeFrom("198.51.100.7", 0), digest(i))

			mu.Lock()
			defer mu.Unlock()

			switch {
			case err == nil:
				ok++
			case errors.Is(err, mfa.ErrSourceChallengeLimit):
				refused++
			default:
				other = append(other, err)
			}
		})
	}

	wg.Wait()

	if ok != mfa.MaxOpenSignInChallengesPerSource || refused != sends-ok || len(other) != 0 {
		t.Fatalf("%d starts at the same time: %d ok, %d refused, other errors %v; want exactly %d ok",
			sends, ok, refused, other, mfa.MaxOpenSignInChallengesPerSource)
	}
}

// testSourceRecovers checks that a source at its count works again after one
// of its challenges ends, and after all of them expire.
func testSourceRecovers(t *testing.T, s PasskeyStore, flood string, over mfa.Digest) {
	t.Helper()

	ctx := t.Context()

	if _, err := s.TakeChallenge(ctx, digest(0), at(2)); err != nil {
		t.Fatalf("take of an open challenge: %v", err)
	}

	if err := s.CreateChallenge(ctx, challengeFrom(flood, 2), over); err != nil {
		t.Fatalf("challenge after one of the source ended: %v", err)
	}

	late := int(mfa.ChallengeExpiry(at(0)).Sub(at(0)).Seconds()) + 5
	if err := s.CreateChallenge(ctx, challengeFrom(flood, late), digest(200)); err != nil {
		t.Fatalf("challenge after the others of the source expired: %v", err)
	}
}

// testChallengeSourceChecks sends a hostile source to the store alone. A
// sign-in without a source, a source on another kind, a source with a control
// character or a source over the length is refused with ErrInvalid and stores
// no row.
func testChallengeSourceChecks(t *testing.T, s PasskeyStore) {
	t.Helper()

	onFresh := challengeOf(mfa.CeremonyFresh, 0)
	onFresh.Source, onFresh.SourceGroup = "198.51.100.7", "198.51.100.7"

	onFreshGroup := challengeOf(mfa.CeremonyFresh, 0)
	onFreshGroup.SourceGroup = "198.51.100.7"

	cases := [...]struct {
		name string
		c    mfa.Challenge
	}{
		{"sign-in without a source", challengeFrom("", 0)},
		{"source with a line break", challengeFrom("198.51.100.7\r\nX: 1", 0)},
		{"source with a non-ASCII character", challengeFrom("198.51.100.\u00e9", 0)},
		{"source over the length", challengeFrom(strings.Repeat("a", mfa.MaxSourceBytes+1), 0)},
		{"source on a fresh challenge", onFresh},
		{"source group on a fresh challenge", onFreshGroup},
		{"sign-in without a source group", challengeIn("198.51.100.7", "", 0)},
		{"source group with a line break", challengeIn("198.51.100.7", "198.51.100.7\r\nX: 1", 0)},
		{"source group over the length", challengeIn("198.51.100.7", strings.Repeat("a", mfa.MaxSourceBytes+1), 0)},
	}

	for i := range cases {
		err := s.CreateChallenge(t.Context(), cases[i].c, digest(i))
		wantErr(t, cases[i].name, err, mfa.ErrInvalid)

		if _, err := s.TakeChallenge(t.Context(), digest(i), at(1)); !errors.Is(err, mfa.ErrNotFound) {
			t.Fatalf("%s: take = %v, want ErrNotFound: the refusal stored a row", cases[i].name, err)
		}
	}
}

func testChallengeOnce(t *testing.T, s PasskeyStore) {
	t.Helper()

	kinds := []mfa.Ceremony{mfa.CeremonyRegister, mfa.CeremonySignIn, mfa.CeremonySecondFactor, mfa.CeremonyFresh}

	for i, k := range kinds {
		c := challengeOf(k, 0)
		if err := s.CreateChallenge(t.Context(), c, digest(i)); err != nil {
			t.Fatalf("%s: create: %v", k, err)
		}

		got, err := s.TakeChallenge(t.Context(), digest(i), at(1))
		if err != nil {
			t.Fatalf("%s: take: %v", k, err)
		}

		if got != c {
			t.Fatalf("%s: challenge = %+v, want %+v", k, got, c)
		}

		_, err = s.TakeChallenge(t.Context(), digest(i), at(2))
		wantErr(t, fmt.Sprintf("%s: second take", k), err, mfa.ErrNotFound)
	}
}

func testChallengeExpired(t *testing.T, s PasskeyStore) {
	t.Helper()

	c := challengeOf(mfa.CeremonySignIn, 0)
	if err := s.CreateChallenge(t.Context(), c, digest(1)); err != nil {
		t.Fatalf("create: %v", err)
	}

	_, err := s.TakeChallenge(t.Context(), digest(1), c.ExpiresAt)
	wantErr(t, "take at the expiry", err, mfa.ErrNotFound)

	_, err = s.TakeChallenge(t.Context(), digest(1), c.CreatedAt)
	wantErr(t, "take after an expired take", err, mfa.ErrNotFound)

	_, err = s.TakeChallenge(t.Context(), digest(9), at(1))
	wantErr(t, "take of no challenge", err, mfa.ErrNotFound)
}

func testChallengeRace(t *testing.T, s PasskeyStore) {
	t.Helper()

	if err := s.CreateChallenge(t.Context(), challengeOf(mfa.CeremonyFresh, 0), digest(1)); err != nil {
		t.Fatalf("create: %v", err)
	}

	const racers = 6

	errs := make([]error, racers)

	var wg sync.WaitGroup

	for i := range racers {
		wg.Go(func() {
			_, errs[i] = s.TakeChallenge(context.WithoutCancel(t.Context()), digest(1), at(1))
		})
	}

	wg.Wait()

	passed := 0

	for _, err := range errs {
		switch {
		case err == nil:
			passed++
		case errors.Is(err, mfa.ErrNotFound):
		default:
			t.Fatalf("take: err = %v, want nil or ErrNotFound", err)
		}
	}

	if passed != 1 {
		t.Fatalf("takes that passed = %d, want 1", passed)
	}
}

func testChallengeInvalid(t *testing.T, s PasskeyStore) {
	t.Helper()

	cases := []struct {
		change func(c *mfa.Challenge)
		name   string
	}{
		{name: "unknown kind", change: func(c *mfa.Challenge) { c.Kind = "login" }},
		{name: "no creation time", change: func(c *mfa.Challenge) { c.CreatedAt = time.Time{} }},
		{name: "expiry at creation", change: func(c *mfa.Challenge) { c.ExpiresAt = c.CreatedAt }},
		{name: "lifetime above the limit", change: func(c *mfa.Challenge) {
			c.ExpiresAt = c.CreatedAt.Add(mfa.ChallengeLifetime + time.Millisecond)
		}},
		{name: "register with no person", change: func(c *mfa.Challenge) { c.PersonID = "" }},
		{name: "register with no session", change: func(c *mfa.Challenge) { c.SessionID = "" }},
		{name: "register with a pending sign-in", change: func(c *mfa.Challenge) { c.PendingID = "psi-1" }},
		{name: "person ID with a quote", change: func(c *mfa.Challenge) { c.PersonID = "prn-1'--" }},
		{name: "long session ID", change: func(c *mfa.Challenge) { c.SessionID = strings.Repeat("s", 129) }},
	}

	for i, c := range cases {
		ch := challengeOf(mfa.CeremonyRegister, 0)
		c.change(&ch)

		err := s.CreateChallenge(t.Context(), ch, digest(i))
		wantErr(t, c.name, err, mfa.ErrInvalid)

		_, err = s.TakeChallenge(t.Context(), digest(i), at(1))
		wantErr(t, c.name+": take", err, mfa.ErrNotFound)
	}

	signIn := challengeOf(mfa.CeremonySignIn, 0)
	signIn.PersonID = "prn-1"

	err := s.CreateChallenge(t.Context(), signIn, digest(99))
	wantErr(t, "sign-in with a person", err, mfa.ErrInvalid)
}

func testChallengeTaken(t *testing.T, s PasskeyStore) {
	t.Helper()

	if err := s.CreateChallenge(t.Context(), challengeOf(mfa.CeremonySignIn, 0), digest(1)); err != nil {
		t.Fatalf("create: %v", err)
	}

	err := s.CreateChallenge(t.Context(), challengeOf(mfa.CeremonyFresh, 1), digest(1))
	wantErr(t, "create with a digest that is taken", err, mfa.ErrAlreadyExists)

	got, err := s.TakeChallenge(t.Context(), digest(1), at(2))
	if err != nil || got.Kind != mfa.CeremonySignIn {
		t.Fatalf("take = %+v, %v; want the first challenge", got, err)
	}
}

// changes lists each write of the port with the entry e.
func changes(s PasskeyStore, pid string) []struct {
	call func(ctx context.Context, e audit.Entry) error
	name string
} {
	return []struct {
		call func(ctx context.Context, e audit.Entry) error
		name string
	}{
		{name: "add", call: func(ctx context.Context, e audit.Entry) error {
			return s.AddPasskey(ctx, passkeyOf(3, pid), e)
		}},
		{name: "remove", call: func(ctx context.Context, e audit.Entry) error {
			return s.RemovePasskey(ctx, pid, "pky-1", e)
		}},
		{name: "regressed use", call: func(ctx context.Context, e audit.Entry) error {
			return s.RecordUse(ctx, use(2, 0, 100), e)
		}},
	}
}

// wantUnchanged fails the test unless the passkeys of pid are pky-1 and
// pky-2 as mustAdd made them.
func wantUnchanged(t *testing.T, s PasskeyStore, pid string) {
	t.Helper()

	ps, err := s.Passkeys(t.Context(), pid)
	if err != nil {
		t.Fatalf("passkeys: %v", err)
	}

	if len(ps) != 2 {
		t.Fatalf("passkeys = %d, want 2", len(ps))
	}

	samePasskey(t, "first passkey", ps[0], passkeyOf(1, pid))
	samePasskey(t, "second passkey", ps[1], passkeyOf(2, pid))
}

func testOtherScope(t *testing.T, s PasskeyStore) {
	t.Helper()

	pid := mustPerson(t, s, 1)
	mustAdd(t, s, 1, pid)
	mustAdd(t, s, 2, pid)

	for _, c := range changes(s, pid) {
		err := c.call(t.Context(), otherScope(passkeyObject(1), at(60)))
		wantErr(t, c.name, err, audit.ErrInvalidEntry)
	}

	wantUnchanged(t, s, pid)
}

func testAppendFails(t *testing.T, s PasskeyStore) {
	t.Helper()

	pid := mustPerson(t, s, 1)
	mustAdd(t, s, 1, pid)
	mustAdd(t, s, 2, pid)

	for _, c := range changes(s, pid) {
		if err := c.call(t.Context(), refused(passkeyObject(1), at(60))); err == nil {
			t.Fatalf("%s with a refused entry: err = nil, want an error", c.name)
		}
	}

	wantUnchanged(t, s, pid)
	wantEntries(t, s, "passkey:", 2)
}
