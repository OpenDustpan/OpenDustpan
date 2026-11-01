package localusertest

import (
	"encoding/base64"
	"errors"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/localuser"
	"opendustpan.org/opendustpan/pkg/principal"
)

// The reasons of the cases that skip.
const (
	skipNoPasskeyStore = "no passkey store yet: no person has a passkey, so a password removal always " +
		"fails with ErrLastCredential"
	skipNoBreakGlassFlip = "no port sets the break-glass flag on an existing person, so no break-glass " +
		"person holds a password or a reset code; the store checks the flag in each write that would"
)

// storeCase is one case of the store suite. A case with a skip reason skips.
type storeCase struct {
	test func(t *testing.T, s Store)
	name string
	skip string
}

// runCase returns a case that runs test.
func runCase(name string, test func(t *testing.T, s Store)) storeCase {
	return storeCase{test: test, name: name, skip: ""}
}

// skipped returns a case that skips with the reason.
func skipped(name, reason string) storeCase {
	return storeCase{test: nil, name: name, skip: reason}
}

func storeCases() []storeCase {
	return slices.Concat(userCases(), passwordCases(), demoCases(), resetCases())
}

func userCases() []storeCase {
	return []storeCase{
		runCase("a created user reads back by name and by principal with its audit entry", testCreateReads),
		runCase("a created user has no password and reads back with an empty hash", testCreateNoPassword),
		runCase(
			"a user name that does not match the pattern fails with ErrInvalid and stores nothing",
			testCreateBadName,
		),
		runCase("a second user with the same name fails with ErrAlreadyExists", testCreateSameName),
		runCase("a second user for the same principal fails with ErrAlreadyExists", testCreateSamePrincipal),
		runCase("a user for a principal that is not a person fails with principal.ErrNotFound", testCreateNotPerson),
		runCase("an unknown name is ErrNotFound", testUnknownName),
		runCase("a user delete keeps the principal and deletes the reset codes", testDelete),
		runCase("a rename stores the new name, keeps the subject and appends one entry", testRename),
		runCase("a rename to a name that another user has fails with ErrAlreadyExists", testRenameTaken),
		runCase("a rename to a name that does not match the pattern fails with ErrInvalid", testRenameBadName),
		runCase(
			"a change with an entry of another scope fails with audit.ErrInvalidEntry and changes nothing",
			testOtherScope,
		),
		runCase("a change whose audit append fails changes nothing", testAppendFails),
		runCase(
			"the session generation of a new person is 1 and of an unknown principal is principal.ErrNotFound",
			testGenerationStart,
		),
	}
}

func passwordCases() []storeCase {
	return []storeCase{
		runCase("a password set stores the hash, sets the time and appends one entry", testSetPassword),
		runCase("a password set adds 1 to the session generation of the person in the same transaction",
			testSetPasswordBumps),
		runCase("a password set with a hash that the hasher does not read fails with ErrInvalid and changes nothing",
			testSetPasswordBadHash),
		runCase("a password set with ChangeRequired reads back with the flag", testSetPasswordChangeRequired),
		runCase("a rehash replaces the hash only while the stored hash is the old one", testRehash),
		runCase("a rehash after a password change fails with ErrNotFound and changes nothing", testRehashStale),
		runCase("a password remove for a person with no passkey fails with ErrLastCredential", testRemoveLast),
		skipped("a password remove for a person with a passkey deletes the hash and appends one entry",
			skipNoPasskeyStore),
		runCase("a change-required set and clear read back and each appends one entry", testChangeRequired),
		runCase("a change-required set for a user with no password fails with ErrNotFound",
			testChangeRequiredNoPassword),
		skipped("a break-glass flag on a person with a password fails with principal.ErrBreakGlassPassword",
			skipNoBreakGlassFlip),
		runCase("a password set for a break-glass person fails with ErrBreakGlass and changes nothing",
			testSetPasswordBreakGlass),
		skipped("a password remove and a remove of the last passkey at the same time: one wins and "+
			"the person keeps a credential", skipNoPasskeyStore),
	}
}

func demoCases() []storeCase {
	return []storeCase{
		runCase("a password set, a remove and a reset code for the demo person fail with ErrDemoFixed", testDemoFixed),
		runCase("a rename of the demo person fails with ErrDemoFixed", testDemoRename),
		runCase("a demo seed on a dev installation stores the hash and appends one entry", testSeed),
		runCase("a demo seed makes the demo user record when it does not exist", testSeedMakesRecord),
		runCase("a demo seed on a production installation fails with ErrDemoOnly and stores nothing",
			testSeedProduction),
		runCase("a demo seed before the installation kind is claimed fails with ErrDemoOnly", testSeedNoKind),
	}
}

func resetCases() []storeCase {
	return []storeCase{
		runCase("a redeem sets the password, deletes the code, adds 1 to the session generation and "+
			"appends one entry in one transaction", testRedeem),
		runCase("a second redeem of a reset code fails with ErrNotFound and changes nothing", testRedeemTwice),
		runCase("a redeem after the expiry fails with ErrNotFound, deletes the code and sets no password",
			testRedeemExpired),
		runCase("a redeem whose audit append fails keeps the code and sets no password", testRedeemAppendFails),
		skipped("a set-up code redeemed for a break-glass person fails with ErrBreakGlass and stores no hash",
			skipNoBreakGlassFlip),
		skipped("a redeem for a break-glass person fails with ErrBreakGlass and changes nothing",
			skipNoBreakGlassFlip),
		runCase("a reset code for a break-glass person fails with ErrBreakGlass and stores nothing",
			testResetBreakGlass),
		runCase("a new reset code deletes the earlier code of the same user", testResetReplaces),
		runCase("a reset adds 1 to the session generation of the person in the same transaction", testResetBumps),
		runCase("a reset keeps the access tokens of the person by default", testResetKeepsTokens),
		runCase("a reset with RevokeAccessTokens revokes each access token of the person in the same transaction",
			testResetRevokesTokens),
		runCase("the audit entry of a reset records whether it revoked the access tokens", testResetEntry),
		runCase("a set-up code adds 1 to no session generation and revokes no token", testSetUpKeeps),
		runCase("a reset code for an unknown user fails with ErrNotFound", testResetUnknownUser),
		runCase("two redeems of one code at the same time: one wins", testRedeemRace),
		runCase("a prune deletes the expired codes only", testPrune),
		runCase("the store finds a reset code by its digest only", testDigestOnly),
		runCase("a second reset code with a digest that another code holds fails with ErrAlreadyExists",
			testDigestTaken),
	}
}

// at is a fixed, millisecond-precision instant: stores keep milliseconds.
func at(minute int) time.Time {
	return time.Date(2026, time.November, 1, 9, 0, 0, 0, time.UTC).Add(time.Duration(minute) * time.Minute)
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
		Action:        "installation:manage-sign-in",
		Object:        object,
		Version:       "",
		ValuePath:     "",
		ValueCount:    nil,
		Changes:       nil,
		SourceIP:      "192.0.2.1",
		UserAgent:     "localusertest",
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

// hash returns a valid stored hash that differs for each n. The suite never
// verifies it: the store keeps the string.
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

// mustCreate creates the principal p.
func mustCreate(t *testing.T, s Store, p principal.Principal) {
	t.Helper()

	if err := s.CreatePrincipal(t.Context(), p, entry(principal.Ref(p.ID), at(0))); err != nil {
		t.Fatalf("create principal %q: %v", p.ID, err)
	}
}

// mustPrincipal creates the principal id of kind with the subject subject.
func mustPrincipal(t *testing.T, s Store, id, subject string, kind principal.Kind) {
	t.Helper()
	mustCreate(t, s, principalOf(id, subject, kind))
}

// newUser returns a local user record of the principal principalID.
func newUser(id, principalID, name string) localuser.User {
	return localuser.User{
		CreatedAt:      at(1),
		PasswordSetAt:  time.Time{},
		ID:             id,
		PrincipalID:    principalID,
		UserName:       name,
		CreatedBy:      "admin",
		ChangeRequired: false,
		Demo:           false,
	}
}

// mustUser creates the person prn-<n> and its local user lus-<n> with the
// user name user<n>, and returns the user.
func mustUser(t *testing.T, s Store, n int) localuser.User {
	t.Helper()

	id := strconv.Itoa(n)
	mustPrincipal(t, s, "prn-"+id, "local:lus-"+id, principal.KindPerson)

	u := newUser("lus-"+id, "prn-"+id, "user"+id)
	if err := s.CreateUser(t.Context(), u, entry("local-user:lus-"+id, at(1))); err != nil {
		t.Fatalf("create user %d: %v", n, err)
	}

	return u
}

// mustPassword sets hash(n) as the password of u.
func mustPassword(t *testing.T, s Store, u localuser.User, n int) {
	t.Helper()

	err := s.SetPassword(t.Context(), u.ID, hash(n), localuser.PasswordChange{ChangeRequired: false},
		entry("local-user:"+u.ID, at(2)))
	if err != nil {
		t.Fatalf("set password of %s: %v", u.ID, err)
	}
}

// storedHash returns the hash of the user name.
func storedHash(t *testing.T, s Store, name string) localuser.PasswordHash {
	t.Helper()

	_, h, err := s.UserByName(t.Context(), name)
	if err != nil {
		t.Fatalf("user by name %q: %v", name, err)
	}

	return h
}

// generation returns the session generation of the principal id.
func generation(t *testing.T, s Store, id string) int64 {
	t.Helper()

	g, err := s.SessionGeneration(t.Context(), id)
	if err != nil {
		t.Fatalf("session generation of %s: %v", id, err)
	}

	return g
}

// entries returns the entries of @server whose object has the prefix.
func entries(t *testing.T, s Store, prefix string) []audit.Entry {
	t.Helper()

	listing, err := s.List(t.Context(), audit.ServerScope, audit.Page{Number: 1, Size: audit.DefaultPageSize})
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
func wantEntries(t *testing.T, s Store, object string, n int) {
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

func testCreateReads(t *testing.T, s Store) {
	t.Helper()

	u := mustUser(t, s, 1)

	byName, h, err := s.UserByName(t.Context(), "user1")
	if err != nil {
		t.Fatalf("user by name: %v", err)
	}

	if byName != u || h != "" {
		t.Fatalf("user by name = %+v, %q; want %+v and no hash", byName, h, u)
	}

	byPrincipal, err := s.UserByPrincipal(t.Context(), "prn-1")
	if err != nil {
		t.Fatalf("user by principal: %v", err)
	}

	if byPrincipal != u {
		t.Fatalf("user by principal = %+v, want %+v", byPrincipal, u)
	}

	wantEntries(t, s, "local-user:lus-1", 1)
}

func testCreateNoPassword(t *testing.T, s Store) {
	t.Helper()

	u := mustUser(t, s, 1)

	got, h, err := s.UserByName(t.Context(), u.UserName)
	if err != nil {
		t.Fatalf("user by name: %v", err)
	}

	if h != "" || !got.PasswordSetAt.IsZero() || got.ChangeRequired {
		t.Fatalf("new user = %+v with hash %q, want no password", got, h)
	}
}

func testCreateBadName(t *testing.T, s Store) {
	t.Helper()

	mustPrincipal(t, s, "prn-1", "local:lus-1", principal.KindPerson)

	for _, name := range []string{
		"", "Alice", "-alice", ".alice", "_alice", "al ice", "alice\n", "al\x00ice", "alicé", "al/ice",
		strings.Repeat("a", localuser.UserNameMaxLength+1),
	} {
		err := s.CreateUser(t.Context(), newUser("lus-1", "prn-1", name), entry("local-user:lus-1", at(1)))
		wantErr(t, "create user "+strconv.Quote(name), err, localuser.ErrInvalid)
	}

	if _, err := s.UserByPrincipal(t.Context(), "prn-1"); !errors.Is(err, localuser.ErrNotFound) {
		t.Fatalf("user by principal after refused creates: err = %v, want ErrNotFound", err)
	}

	wantEntries(t, s, "local-user:", 0)
}

func testCreateSameName(t *testing.T, s Store) {
	t.Helper()

	mustUser(t, s, 1)
	mustPrincipal(t, s, "prn-2", "local:lus-2", principal.KindPerson)

	err := s.CreateUser(t.Context(), newUser("lus-2", "prn-2", "user1"), entry("local-user:lus-2", at(1)))
	wantErr(t, "create a second user1", err, localuser.ErrAlreadyExists)
	wantEntries(t, s, "local-user:lus-2", 0)
}

func testCreateSamePrincipal(t *testing.T, s Store) {
	t.Helper()

	mustUser(t, s, 1)

	err := s.CreateUser(t.Context(), newUser("lus-2", "prn-1", "other"), entry("local-user:lus-2", at(1)))
	wantErr(t, "create a second user of prn-1", err, localuser.ErrAlreadyExists)
}

func testCreateNotPerson(t *testing.T, s Store) {
	t.Helper()

	mustPrincipal(t, s, "prn-svc", "svc", principal.KindService)

	err := s.CreateUser(t.Context(), newUser("lus-1", "prn-svc", "svc"), entry("local-user:lus-1", at(1)))
	wantErr(t, "create a user of a service principal", err, principal.ErrNotFound)

	err = s.CreateUser(t.Context(), newUser("lus-2", "prn-missing", "ghost"), entry("local-user:lus-2", at(1)))
	wantErr(t, "create a user of a missing principal", err, principal.ErrNotFound)
	wantEntries(t, s, "local-user:", 0)
}

func testUnknownName(t *testing.T, s Store) {
	t.Helper()

	mustUser(t, s, 1)

	for _, name := range []string{"nobody", "USER1", "user1 ", "", strings.Repeat("x", 1000)} {
		if _, _, err := s.UserByName(t.Context(), name); !errors.Is(err, localuser.ErrNotFound) {
			t.Errorf("user by name %q: err = %v, want ErrNotFound", name, err)
		}
	}

	if _, err := s.UserByPrincipal(t.Context(), "prn-none"); !errors.Is(err, localuser.ErrNotFound) {
		t.Errorf("user by an unknown principal: err = %v, want ErrNotFound", err)
	}
}

func testDelete(t *testing.T, s Store) {
	t.Helper()

	u := mustUser(t, s, 1)
	d := mustReset(t, s, newReset("lpr-1", u.ID, localuser.PurposeSetUp, at(3)))

	if err := s.DeleteUser(t.Context(), u.ID, entry("local-user:lus-1", at(4))); err != nil {
		t.Fatalf("delete user: %v", err)
	}

	if _, err := s.UserByPrincipal(t.Context(), "prn-1"); !errors.Is(err, localuser.ErrNotFound) {
		t.Fatalf("user after delete: err = %v, want ErrNotFound", err)
	}

	if _, err := s.GetPrincipal(t.Context(), "prn-1"); err != nil {
		t.Fatalf("principal after the user delete: %v", err)
	}

	_, err := redeem(t, s, d, at(5), hash(1))
	wantErr(t, "redeem a code of a deleted user", err, localuser.ErrNotFound)

	err = s.DeleteUser(t.Context(), u.ID, entry("local-user:lus-1", at(6)))
	wantErr(t, "delete the user again", err, localuser.ErrNotFound)
	wantEntries(t, s, "local-user:lus-1", 3)
}

func testRename(t *testing.T, s Store) {
	t.Helper()

	u := mustUser(t, s, 1)

	if err := s.RenameUser(t.Context(), u.ID, "alice.smith", entry("local-user:lus-1", at(3))); err != nil {
		t.Fatalf("rename: %v", err)
	}

	got, _, err := s.UserByName(t.Context(), "alice.smith")
	if err != nil || got.ID != u.ID || got.PrincipalID != u.PrincipalID {
		t.Fatalf("user by the new name = %+v, %v; want %s", got, err, u.ID)
	}

	if _, _, oldErr := s.UserByName(t.Context(), "user1"); !errors.Is(oldErr, localuser.ErrNotFound) {
		t.Fatalf("user by the old name: err = %v, want ErrNotFound", oldErr)
	}

	p, err := s.GetPrincipal(t.Context(), u.PrincipalID)
	if err != nil || p.Subject != "local:lus-1" {
		t.Fatalf("principal after a rename = %+v, %v; want the subject local:lus-1", p, err)
	}

	err = s.RenameUser(t.Context(), "lus-none", "bob", entry("local-user:lus-none", at(3)))
	wantErr(t, "rename an unknown user", err, localuser.ErrNotFound)
	wantEntries(t, s, "local-user:lus-1", 2)
}

func testRenameTaken(t *testing.T, s Store) {
	t.Helper()

	mustUser(t, s, 1)
	u2 := mustUser(t, s, 2)

	err := s.RenameUser(t.Context(), u2.ID, "user1", entry("local-user:lus-2", at(3)))
	wantErr(t, "rename to a taken name", err, localuser.ErrAlreadyExists)

	if got, _, err := s.UserByName(t.Context(), "user2"); err != nil || got.ID != u2.ID {
		t.Fatalf("user2 after a refused rename = %+v, %v", got, err)
	}

	if err := s.RenameUser(t.Context(), u2.ID, "user2", entry("local-user:lus-2", at(3))); err != nil {
		t.Fatalf("rename to the same name: %v", err)
	}
}

func testRenameBadName(t *testing.T, s Store) {
	t.Helper()

	u := mustUser(t, s, 1)

	for _, name := range []string{"", "User1", "user one", "user1\r\n", "-user", strings.Repeat("a", 65)} {
		err := s.RenameUser(t.Context(), u.ID, name, entry("local-user:lus-1", at(3)))
		wantErr(t, "rename to "+strconv.Quote(name), err, localuser.ErrInvalid)
	}

	wantEntries(t, s, "local-user:lus-1", 1)
}

func testOtherScope(t *testing.T, s Store) {
	t.Helper()

	u := mustUser(t, s, 1)
	g := generation(t, s, u.PrincipalID)

	err := s.SetPassword(t.Context(), u.ID, hash(1), localuser.PasswordChange{ChangeRequired: false},
		otherScope("local-user:lus-1", at(3)))
	wantErr(t, "set password with an entry of a namespace", err, audit.ErrInvalidEntry)

	mustPrincipal(t, s, "prn-2", "local:lus-2", principal.KindPerson)

	err = s.CreateUser(t.Context(), newUser("lus-2", "prn-2", "user2"), otherScope("local-user:lus-2", at(3)))
	wantErr(t, "create user with an entry of a namespace", err, audit.ErrInvalidEntry)

	if h := storedHash(t, s, "user1"); h != "" {
		t.Fatalf("hash after a refused set = %q, want none", h)
	}

	if _, _, err := s.UserByName(t.Context(), "user2"); !errors.Is(err, localuser.ErrNotFound) {
		t.Fatalf("user2 after a refused create: err = %v, want ErrNotFound", err)
	}

	if got := generation(t, s, u.PrincipalID); got != g {
		t.Fatalf("session generation after a refused set = %d, want %d", got, g)
	}
}

func testAppendFails(t *testing.T, s Store) {
	t.Helper()

	u := mustUser(t, s, 1)
	mustPassword(t, s, u, 1)

	g := generation(t, s, u.PrincipalID)
	ctx := t.Context()
	keep := localuser.PasswordChange{ChangeRequired: false}

	checks := []struct {
		call func(e audit.Entry) error
		name string
	}{
		{name: "set password", call: func(e audit.Entry) error { return s.SetPassword(ctx, u.ID, hash(2), keep, e) }},
		{name: "rehash", call: func(e audit.Entry) error { return s.RehashPassword(ctx, u.ID, hash(1), hash(3), e) }},
		{name: "rename", call: func(e audit.Entry) error { return s.RenameUser(ctx, u.ID, "renamed", e) }},
		{name: "change required", call: func(e audit.Entry) error { return s.SetChangeRequired(ctx, u.ID, true, e) }},
		{name: "delete", call: func(e audit.Entry) error { return s.DeleteUser(ctx, u.ID, e) }},
		{name: "reset", call: func(e audit.Entry) error {
			return s.CreateReset(ctx, newReset("lpr-1", u.ID, localuser.PurposeReset, at(3)), digest(1), e)
		}},
	}

	for _, c := range checks {
		if err := c.call(refused("local-user:lus-1", at(3))); err == nil {
			t.Errorf("%s with a refused entry: err = nil, want an error", c.name)
		}
	}

	got, h, err := s.UserByName(ctx, "user1")
	if err != nil || h != hash(1) || got.ChangeRequired {
		t.Fatalf("user after refused changes = %+v, %q, %v; want user1 with hash 1", got, h, err)
	}

	if got := generation(t, s, u.PrincipalID); got != g {
		t.Fatalf("session generation after refused changes = %d, want %d", got, g)
	}

	_, err = redeem(t, s, digest(1), at(4), hash(4))
	wantErr(t, "redeem a code that a refused entry stored", err, localuser.ErrNotFound)
}

func testGenerationStart(t *testing.T, s Store) {
	t.Helper()

	mustPrincipal(t, s, "prn-1", "alice", principal.KindPerson)

	if got := generation(t, s, "prn-1"); got != 1 {
		t.Fatalf("session generation of a new person = %d, want 1", got)
	}

	if _, err := s.SessionGeneration(t.Context(), "prn-none"); !errors.Is(err, principal.ErrNotFound) {
		t.Fatalf("session generation of an unknown principal: err = %v, want principal.ErrNotFound", err)
	}
}

func testSetPassword(t *testing.T, s Store) {
	t.Helper()

	u := mustUser(t, s, 1)

	err := s.SetPassword(t.Context(), u.ID, hash(1), localuser.PasswordChange{ChangeRequired: false},
		entry("local-user:lus-1", at(7)))
	if err != nil {
		t.Fatalf("set password: %v", err)
	}

	got, h, err := s.UserByName(t.Context(), "user1")
	if err != nil {
		t.Fatalf("user by name: %v", err)
	}

	if h != hash(1) || !got.PasswordSetAt.Equal(at(7)) || got.ChangeRequired {
		t.Fatalf("user after a set = %+v, %q; want hash 1 set at %s", got, h, at(7))
	}

	wantEntries(t, s, "local-user:lus-1", 2)

	err = s.SetPassword(t.Context(), "lus-none", hash(1), localuser.PasswordChange{ChangeRequired: false},
		entry("local-user:lus-none", at(7)))
	wantErr(t, "set password of an unknown user", err, localuser.ErrNotFound)
}

func testSetPasswordBumps(t *testing.T, s Store) {
	t.Helper()

	u := mustUser(t, s, 1)
	mustPrincipal(t, s, "prn-other", "other", principal.KindPerson)

	mustPassword(t, s, u, 1)
	mustPassword(t, s, u, 2)

	if got := generation(t, s, u.PrincipalID); got != 3 {
		t.Fatalf("session generation after two sets = %d, want 3", got)
	}

	if got := generation(t, s, "prn-other"); got != 1 {
		t.Fatalf("session generation of another person = %d, want 1", got)
	}
}

func testSetPasswordBadHash(t *testing.T, s Store) {
	t.Helper()

	u := mustUser(t, s, 1)

	for _, h := range []localuser.PasswordHash{
		"",
		"hunter2",
		"$argon2i$v=19$m=19456,t=2,p=1$AAAAAAAAAAAAAAAAAAAAAA$" + localuser.PasswordHash(strings.Repeat("A", 43)),
		"$argon2id$v=19$m=999999,t=2,p=1$AAAAAAAAAAAAAAAAAAAAAA$" + localuser.PasswordHash(strings.Repeat("A", 43)),
		"$argon2id$v=19$m=19456,t=2,p=0$AAAAAAAAAAAAAAAAAAAAAA$" + localuser.PasswordHash(strings.Repeat("A", 43)),
		hash(1) + "\n",
	} {
		err := s.SetPassword(t.Context(), u.ID, h, localuser.PasswordChange{ChangeRequired: false},
			entry("local-user:lus-1", at(3)))
		wantErr(t, "set the hash "+strconv.Quote(string(h)), err, localuser.ErrInvalid)
	}

	if h := storedHash(t, s, "user1"); h != "" {
		t.Fatalf("hash after refused sets = %q, want none", h)
	}

	if got := generation(t, s, u.PrincipalID); got != 1 {
		t.Fatalf("session generation after refused sets = %d, want 1", got)
	}
}

func testSetPasswordChangeRequired(t *testing.T, s Store) {
	t.Helper()

	u := mustUser(t, s, 1)

	err := s.SetPassword(t.Context(), u.ID, hash(1), localuser.PasswordChange{ChangeRequired: true},
		entry("local-user:lus-1", at(3)))
	if err != nil {
		t.Fatalf("set password: %v", err)
	}

	got, err := s.UserByPrincipal(t.Context(), u.PrincipalID)
	if err != nil || !got.ChangeRequired {
		t.Fatalf("user after a set with ChangeRequired = %+v, %v; want the flag", got, err)
	}

	mustPassword(t, s, u, 2)

	got, err = s.UserByPrincipal(t.Context(), u.PrincipalID)
	if err != nil || got.ChangeRequired {
		t.Fatalf("user after a set without ChangeRequired = %+v, %v; want no flag", got, err)
	}
}

func testRehash(t *testing.T, s Store) {
	t.Helper()

	u := mustUser(t, s, 1)
	mustPassword(t, s, u, 1)

	g := generation(t, s, u.PrincipalID)
	setAt := mustUserByID(t, s, u).PasswordSetAt

	if err := s.RehashPassword(t.Context(), u.ID, hash(1), hash(2), entry("local-user:lus-1", at(9))); err != nil {
		t.Fatalf("rehash: %v", err)
	}

	if h := storedHash(t, s, "user1"); h != hash(2) {
		t.Fatalf("hash after a rehash = %q, want hash 2", h)
	}

	if got := generation(t, s, u.PrincipalID); got != g {
		t.Fatalf("session generation after a rehash = %d, want %d: the password is the same", got, g)
	}

	if got := mustUserByID(t, s, u).PasswordSetAt; !got.Equal(setAt) {
		t.Fatalf("password set time after a rehash = %s, want %s", got, setAt)
	}

	err := s.RehashPassword(t.Context(), u.ID, hash(1), hash(3), entry("local-user:lus-1", at(9)))
	wantErr(t, "rehash from a hash that is no longer stored", err, localuser.ErrNotFound)

	if h := storedHash(t, s, "user1"); h != hash(2) {
		t.Fatalf("hash after a stale rehash = %q, want hash 2", h)
	}

	err = s.RehashPassword(t.Context(), u.ID, hash(2), "$argon2id$bad", entry("local-user:lus-1", at(9)))
	wantErr(t, "rehash to a hash that the hasher does not read", err, localuser.ErrInvalid)
}

func mustUserByID(t *testing.T, s Store, u localuser.User) localuser.User {
	t.Helper()

	got, err := s.UserByPrincipal(t.Context(), u.PrincipalID)
	if err != nil {
		t.Fatalf("user by principal: %v", err)
	}

	return got
}

func testRehashStale(t *testing.T, s Store) {
	t.Helper()

	u := mustUser(t, s, 1)
	mustPassword(t, s, u, 1)
	mustPassword(t, s, u, 2)

	before := len(entries(t, s, "local-user:lus-1"))

	err := s.RehashPassword(t.Context(), u.ID, hash(1), hash(3), entry("local-user:lus-1", at(9)))
	wantErr(t, "rehash after a change", err, localuser.ErrNotFound)

	if h := storedHash(t, s, "user1"); h != hash(2) {
		t.Fatalf("hash after a stale rehash = %q, want the changed hash 2", h)
	}

	wantEntries(t, s, "local-user:lus-1", before)

	u2 := mustUser(t, s, 2)

	err = s.RehashPassword(t.Context(), u2.ID, "", hash(3), entry("local-user:lus-2", at(9)))
	wantErr(t, "rehash of a user with no password", err, localuser.ErrNotFound)
}

func testRemoveLast(t *testing.T, s Store) {
	t.Helper()

	u := mustUser(t, s, 1)
	mustPassword(t, s, u, 1)

	err := s.RemovePassword(t.Context(), u.ID, entry("local-user:lus-1", at(4)))
	wantErr(t, "remove the only credential", err, localuser.ErrLastCredential)

	if h := storedHash(t, s, "user1"); h != hash(1) {
		t.Fatalf("hash after a refused remove = %q, want hash 1", h)
	}

	err = s.RemovePassword(t.Context(), "lus-none", entry("local-user:lus-none", at(4)))
	wantErr(t, "remove the password of an unknown user", err, localuser.ErrNotFound)
}

func testChangeRequired(t *testing.T, s Store) {
	t.Helper()

	u := mustUser(t, s, 1)
	mustPassword(t, s, u, 1)

	for i, want := range []bool{true, false} {
		if err := s.SetChangeRequired(t.Context(), u.ID, want, entry("local-user:lus-1", at(5+i))); err != nil {
			t.Fatalf("set change required to %t: %v", want, err)
		}

		if got := mustUserByID(t, s, u); got.ChangeRequired != want {
			t.Fatalf("change required = %t, want %t", got.ChangeRequired, want)
		}
	}

	wantEntries(t, s, "local-user:lus-1", 4)
}

func testChangeRequiredNoPassword(t *testing.T, s Store) {
	t.Helper()

	u := mustUser(t, s, 1)

	err := s.SetChangeRequired(t.Context(), u.ID, true, entry("local-user:lus-1", at(5)))
	wantErr(t, "change required with no password", err, localuser.ErrNotFound)

	err = s.SetChangeRequired(t.Context(), "lus-none", true, entry("local-user:lus-none", at(5)))
	wantErr(t, "change required of an unknown user", err, localuser.ErrNotFound)
}

// mustBreakGlassUser creates the break-glass person prn-bg and its local user
// lus-bg with no password.
func mustBreakGlassUser(t *testing.T, s Store) localuser.User {
	t.Helper()

	p := principalOf("prn-bg", "local:lus-bg", principal.KindPerson)
	p.BreakGlass = true
	mustCreate(t, s, p)

	u := newUser("lus-bg", "prn-bg", "breakglass")
	if err := s.CreateUser(t.Context(), u, entry("local-user:lus-bg", at(1))); err != nil {
		t.Fatalf("create the user of a break-glass person: %v", err)
	}

	return u
}

func testSetPasswordBreakGlass(t *testing.T, s Store) {
	t.Helper()

	u := mustBreakGlassUser(t, s)

	err := s.SetPassword(t.Context(), u.ID, hash(1), localuser.PasswordChange{ChangeRequired: false},
		entry("local-user:lus-bg", at(3)))
	wantErr(t, "set password of a break-glass person", err, localuser.ErrBreakGlass)

	if h := storedHash(t, s, "breakglass"); h != "" {
		t.Fatalf("hash of a break-glass person = %q, want none", h)
	}

	if got := generation(t, s, u.PrincipalID); got != 1 {
		t.Fatalf("session generation after a refused set = %d, want 1", got)
	}

	wantEntries(t, s, "local-user:lus-bg", 1)
}

// mustDemo claims kind for the installation, makes the demo person, and
// seeds the demo password hash(1) when kind is dev.
func mustDemo(t *testing.T, s Store, kind installation.Kind) {
	t.Helper()

	if _, err := s.ClaimKind(t.Context(), kind); err != nil {
		t.Fatalf("claim kind %s: %v", kind, err)
	}

	mustPrincipal(t, s, "prn-demo", localuser.DemoSubject, principal.KindPerson)

	if kind != installation.KindDev {
		return
	}

	if err := s.SeedDemoPassword(t.Context(), hash(1), entry("local-user:demo", at(2))); err != nil {
		t.Fatalf("seed the demo password: %v", err)
	}
}

func testDemoFixed(t *testing.T, s Store) {
	t.Helper()

	mustDemo(t, s, installation.KindDev)

	u, err := s.UserByPrincipal(t.Context(), "prn-demo")
	if err != nil {
		t.Fatalf("demo user: %v", err)
	}

	ctx := t.Context()
	e := entry("local-user:demo", at(3))
	keep := localuser.PasswordChange{ChangeRequired: false}

	wantErr(t, "set the demo password", s.SetPassword(ctx, u.ID, hash(2), keep, e), localuser.ErrDemoFixed)
	wantErr(t, "remove the demo password", s.RemovePassword(ctx, u.ID, e), localuser.ErrDemoFixed)
	wantErr(t, "change required of the demo user", s.SetChangeRequired(ctx, u.ID, true, e), localuser.ErrDemoFixed)
	wantErr(t, "a reset code for the demo user",
		s.CreateReset(ctx, newReset("lpr-1", u.ID, localuser.PurposeReset, at(3)), digest(1), e),
		localuser.ErrDemoFixed)

	if h := storedHash(t, s, localuser.DemoUserName); h != hash(1) {
		t.Fatalf("demo hash after refused changes = %q, want the seeded hash", h)
	}
}

func testDemoRename(t *testing.T, s Store) {
	t.Helper()

	mustDemo(t, s, installation.KindDev)

	u, err := s.UserByPrincipal(t.Context(), "prn-demo")
	if err != nil {
		t.Fatalf("demo user: %v", err)
	}

	err = s.RenameUser(t.Context(), u.ID, "visitor", entry("local-user:demo", at(3)))
	wantErr(t, "rename the demo user", err, localuser.ErrDemoFixed)

	if _, _, err := s.UserByName(t.Context(), localuser.DemoUserName); err != nil {
		t.Fatalf("demo user by name after a refused rename: %v", err)
	}
}

func testSeed(t *testing.T, s Store) {
	t.Helper()

	mustDemo(t, s, installation.KindDev)

	if err := s.SeedDemoPassword(t.Context(), hash(2), entry("local-user:demo", at(9))); err != nil {
		t.Fatalf("seed again: %v", err)
	}

	got, h, err := s.UserByName(t.Context(), localuser.DemoUserName)
	if err != nil {
		t.Fatalf("demo user: %v", err)
	}

	if h != hash(2) || !got.Demo || !got.PasswordSetAt.Equal(at(9)) || got.PrincipalID != "prn-demo" {
		t.Fatalf("demo user after a second seed = %+v, %q; want hash 2 set at %s", got, h, at(9))
	}

	wantEntries(t, s, "local-user:demo", 2)
}

func testSeedMakesRecord(t *testing.T, s Store) {
	t.Helper()

	if _, err := s.ClaimKind(t.Context(), installation.KindDev); err != nil {
		t.Fatalf("claim kind: %v", err)
	}

	err := s.SeedDemoPassword(t.Context(), hash(1), entry("local-user:demo", at(2)))
	wantErr(t, "seed with no demo person", err, principal.ErrNotFound)

	mustPrincipal(t, s, "prn-demo", localuser.DemoSubject, principal.KindPerson)

	if _, readErr := s.UserByPrincipal(t.Context(), "prn-demo"); !errors.Is(readErr, localuser.ErrNotFound) {
		t.Fatalf("demo user before the seed: err = %v, want ErrNotFound", readErr)
	}

	if seedErr := s.SeedDemoPassword(t.Context(), hash(1), entry("local-user:demo", at(2))); seedErr != nil {
		t.Fatalf("seed: %v", seedErr)
	}

	got, err := s.UserByPrincipal(t.Context(), "prn-demo")
	if err != nil {
		t.Fatalf("demo user after the seed: %v", err)
	}

	if !got.Demo || got.UserName != localuser.DemoUserName || got.CreatedBy != "system" ||
		!strings.HasPrefix(got.ID, localuser.UserIDPrefix) {
		t.Fatalf("demo user = %+v, want the demo record made by system", got)
	}
}

func testSeedProduction(t *testing.T, s Store) {
	t.Helper()

	mustDemo(t, s, installation.KindProduction)

	err := s.SeedDemoPassword(t.Context(), hash(1), entry("local-user:demo", at(2)))
	wantErr(t, "seed on a production installation", err, localuser.ErrDemoOnly)

	if _, err := s.UserByPrincipal(t.Context(), "prn-demo"); !errors.Is(err, localuser.ErrNotFound) {
		t.Fatalf("demo user after a refused seed: err = %v, want ErrNotFound", err)
	}

	wantEntries(t, s, "local-user:demo", 0)
}

func testSeedNoKind(t *testing.T, s Store) {
	t.Helper()

	mustPrincipal(t, s, "prn-demo", localuser.DemoSubject, principal.KindPerson)

	err := s.SeedDemoPassword(t.Context(), hash(1), entry("local-user:demo", at(2)))
	wantErr(t, "seed before the kind is claimed", err, localuser.ErrDemoOnly)

	if _, err := s.UserByPrincipal(t.Context(), "prn-demo"); !errors.Is(err, localuser.ErrNotFound) {
		t.Fatalf("demo user after a refused seed: err = %v, want ErrNotFound", err)
	}
}

// digest returns a distinct reset digest for n.
func digest(n int) localuser.Digest {
	return localuser.DigestOf("reset code " + strconv.Itoa(n))
}

// newReset returns a reset code of the user userID made at created that
// expires after localuser.DefaultResetLifetime.
func newReset(id, userID string, purpose localuser.Purpose, created time.Time) localuser.Reset {
	return localuser.Reset{
		CreatedAt:          created,
		ExpiresAt:          created.Add(localuser.DefaultResetLifetime),
		ID:                 id,
		UserID:             userID,
		CreatedBy:          "admin",
		Reason:             "the person lost the password",
		Purpose:            purpose,
		RevokeAccessTokens: false,
	}
}

// mustReset stores the reset code r with the digest digestOfReset(r) and
// returns the digest.
func mustReset(t *testing.T, s Store, r localuser.Reset) localuser.Digest {
	t.Helper()

	d := digestOfReset(r)
	if err := s.CreateReset(t.Context(), r, d, entry("local-user:"+r.UserID, r.CreatedAt)); err != nil {
		t.Fatalf("create reset %s: %v", r.ID, err)
	}

	return d
}

// digestOfReset returns the digest that the cases give the reset code r: a
// digest of its ID.
func digestOfReset(r localuser.Reset) localuser.Digest {
	return localuser.DigestOf("reset code " + r.ID)
}

// redeemObject is the object of the audit entries of the redeems of the
// cases.
const redeemObject = "local-user:redeem"

// redeem uses the code d at when with the hash h.
func redeem(t *testing.T, s Store, d localuser.Digest, when time.Time, h localuser.PasswordHash) (
	localuser.Reset, error,
) {
	t.Helper()

	return s.RedeemReset(t.Context(), redeemOf(d, when, h), entry(redeemObject, when))
}

// redeemOf returns the use of the code d at when with the hash h.
func redeemOf(d localuser.Digest, when time.Time, h localuser.PasswordHash) localuser.Redeem {
	return localuser.Redeem{At: when, Hash: h, Change: localuser.PasswordChange{ChangeRequired: false}, Digest: d}
}

func testRedeem(t *testing.T, s Store) {
	t.Helper()

	u := mustUser(t, s, 1)
	d := mustReset(t, s, newReset("lpr-1", u.ID, localuser.PurposeSetUp, at(3)))
	wantEntries(t, s, redeemObject, 0)

	r, err := redeem(t, s, d, at(4), hash(5))
	if err != nil {
		t.Fatalf("redeem: %v", err)
	}

	if r.ID != "lpr-1" || r.UserID != u.ID || r.Purpose != localuser.PurposeSetUp {
		t.Fatalf("redeemed reset = %+v, want lpr-1 of %s", r, u.ID)
	}

	got, h, err := s.UserByName(t.Context(), "user1")
	if err != nil || h != hash(5) || !got.PasswordSetAt.Equal(at(4)) {
		t.Fatalf("user after a redeem = %+v, %q, %v; want hash 5 set at %s", got, h, err, at(4))
	}

	if g := generation(t, s, u.PrincipalID); g != 2 {
		t.Fatalf("session generation after a redeem = %d, want 2", g)
	}

	wantEntries(t, s, redeemObject, 1)
}

func testRedeemTwice(t *testing.T, s Store) {
	t.Helper()

	u := mustUser(t, s, 1)
	d := mustReset(t, s, newReset("lpr-1", u.ID, localuser.PurposeReset, at(3)))

	if _, err := redeem(t, s, d, at(4), hash(5)); err != nil {
		t.Fatalf("first redeem: %v", err)
	}

	g := generation(t, s, u.PrincipalID)
	wantEntries(t, s, redeemObject, 1)

	_, err := redeem(t, s, d, at(5), hash(6))
	wantErr(t, "second redeem", err, localuser.ErrNotFound)

	if h := storedHash(t, s, "user1"); h != hash(5) {
		t.Fatalf("hash after a second redeem = %q, want hash 5", h)
	}

	if got := generation(t, s, u.PrincipalID); got != g {
		t.Fatalf("session generation after a second redeem = %d, want %d", got, g)
	}

	wantEntries(t, s, redeemObject, 1)
}

func testRedeemExpired(t *testing.T, s Store) {
	t.Helper()

	u := mustUser(t, s, 1)
	d := mustReset(t, s, newReset("lpr-1", u.ID, localuser.PurposeSetUp, at(3)))
	expiry := at(3).Add(localuser.DefaultResetLifetime)

	_, err := redeem(t, s, d, expiry, hash(5))
	wantErr(t, "redeem at the expiry", err, localuser.ErrNotFound)

	if h := storedHash(t, s, "user1"); h != "" {
		t.Fatalf("hash after an expired redeem = %q, want none", h)
	}

	if n, err := s.PruneResets(t.Context(), expiry.Add(time.Hour)); err != nil || n != 0 {
		t.Fatalf("prune after an expired redeem = %d, %v; want 0: the redeem deleted the code", n, err)
	}
}

func testRedeemAppendFails(t *testing.T, s Store) {
	t.Helper()

	u := mustUser(t, s, 1)
	d := mustReset(t, s, newReset("lpr-1", u.ID, localuser.PurposeReset, at(3)))
	g := generation(t, s, u.PrincipalID)

	_, err := s.RedeemReset(t.Context(), redeemOf(d, at(4), hash(5)), refused("local-user:lus-1", at(4)))
	if err == nil {
		t.Fatal("redeem with a refused entry: err = nil, want an error")
	}

	if h := storedHash(t, s, "user1"); h != "" {
		t.Fatalf("hash after a refused redeem = %q, want none", h)
	}

	if got := generation(t, s, u.PrincipalID); got != g {
		t.Fatalf("session generation after a refused redeem = %d, want %d", got, g)
	}

	if _, err := redeem(t, s, d, at(5), hash(6)); err != nil {
		t.Fatalf("redeem after a refused redeem: %v; want the code kept", err)
	}
}

func testResetBreakGlass(t *testing.T, s Store) {
	t.Helper()

	u := mustBreakGlassUser(t, s)

	for _, purpose := range []localuser.Purpose{localuser.PurposeSetUp, localuser.PurposeReset} {
		err := s.CreateReset(t.Context(), newReset("lpr-1", u.ID, purpose, at(3)), digest(1),
			entry("local-user:lus-bg", at(3)))
		wantErr(t, "a "+string(purpose)+" code for a break-glass person", err, localuser.ErrBreakGlass)
	}

	_, err := redeem(t, s, digest(1), at(4), hash(1))
	wantErr(t, "redeem a refused code", err, localuser.ErrNotFound)

	if got := generation(t, s, u.PrincipalID); got != 1 {
		t.Fatalf("session generation after refused codes = %d, want 1", got)
	}
}

func testResetReplaces(t *testing.T, s Store) {
	t.Helper()

	u := mustUser(t, s, 1)
	first := mustReset(t, s, newReset("lpr-1", u.ID, localuser.PurposeSetUp, at(3)))
	second := mustReset(t, s, newReset("lpr-2", u.ID, localuser.PurposeSetUp, at(4)))

	_, err := redeem(t, s, first, at(5), hash(5))
	wantErr(t, "redeem the earlier code", err, localuser.ErrNotFound)

	if _, err := redeem(t, s, second, at(5), hash(6)); err != nil {
		t.Fatalf("redeem the new code: %v", err)
	}
}

func testResetBumps(t *testing.T, s Store) {
	t.Helper()

	u := mustUser(t, s, 1)
	mustPassword(t, s, u, 1)

	g := generation(t, s, u.PrincipalID)

	mustReset(t, s, newReset("lpr-1", u.ID, localuser.PurposeReset, at(3)))

	if got := generation(t, s, u.PrincipalID); got != g+1 {
		t.Fatalf("session generation after a reset = %d, want %d", got, g+1)
	}
}

// mustToken creates the live personal access token id of the principal.
func mustToken(t *testing.T, s Store, id, principalID string, n int) {
	t.Helper()

	tok := accesstoken.Token{
		CreatedAt: at(1), ExpiresAt: at(1).Add(24 * time.Hour), LastUsedAt: time.Time{},
		RevokedAt: time.Time{}, DisabledAt: time.Time{}, ID: id, PrincipalID: principalID, Name: "laptop " + id,
		Kind: accesstoken.KindPersonal, ReplacedBy: "", CreatedBy: principalID,
	}

	d := accesstoken.Digest(localuser.DigestOf("token " + strconv.Itoa(n)))
	if err := s.CreateToken(t.Context(), tok, d, entry("token:"+id, at(1))); err != nil {
		t.Fatalf("create token %s: %v", id, err)
	}
}

// revokedAt returns the revoke time of the token id.
func revokedAt(t *testing.T, s Store, id string) time.Time {
	t.Helper()

	tok, err := s.GetToken(t.Context(), id)
	if err != nil {
		t.Fatalf("get token %s: %v", id, err)
	}

	return tok.RevokedAt
}

func testResetKeepsTokens(t *testing.T, s Store) {
	t.Helper()

	u := mustUser(t, s, 1)
	mustToken(t, s, "tok-1", u.PrincipalID, 1)
	mustReset(t, s, newReset("lpr-1", u.ID, localuser.PurposeReset, at(3)))

	if got := revokedAt(t, s, "tok-1"); !got.IsZero() {
		t.Fatalf("token after a reset that keeps the tokens: revoked at %s, want live", got)
	}
}

func testResetRevokesTokens(t *testing.T, s Store) {
	t.Helper()

	u := mustUser(t, s, 1)
	other := mustUser(t, s, 2)

	mustToken(t, s, "tok-1", u.PrincipalID, 1)
	mustToken(t, s, "tok-2", u.PrincipalID, 2)
	mustToken(t, s, "tok-3", other.PrincipalID, 3)

	r := newReset("lpr-1", u.ID, localuser.PurposeReset, at(3))
	r.RevokeAccessTokens = true

	if err := s.CreateReset(t.Context(), r, digest(1), entry("local-user:lus-1", at(3))); err != nil {
		t.Fatalf("create reset: %v", err)
	}

	for _, id := range []string{"tok-1", "tok-2"} {
		if got := revokedAt(t, s, id); !got.Equal(at(3)) {
			t.Errorf("token %s of the person: revoked at %s, want %s", id, got, at(3))
		}
	}

	if got := revokedAt(t, s, "tok-3"); !got.IsZero() {
		t.Errorf("token of another person: revoked at %s, want live", got)
	}
}

// changeOf returns the new value of the change key of e, and whether e has
// it.
func changeOf(e audit.Entry, key string) (string, bool) {
	for _, c := range e.Changes {
		if c.Key == key {
			return c.New, true
		}
	}

	return "", false
}

func testResetEntry(t *testing.T, s Store) {
	t.Helper()

	u1 := mustUser(t, s, 1)
	u2 := mustUser(t, s, 2)

	mustToken(t, s, "tok-1", u2.PrincipalID, 1)
	mustReset(t, s, newReset("lpr-1", u1.ID, localuser.PurposeReset, at(3)))

	r := newReset("lpr-2", u2.ID, localuser.PurposeReset, at(4))
	r.RevokeAccessTokens = true

	if err := s.CreateReset(t.Context(), r, digest(2), entry("local-user:lus-2", at(4))); err != nil {
		t.Fatalf("create reset: %v", err)
	}

	want := map[string]map[string]string{
		"local-user:lus-1": {"revokeAccessTokens": "false", "revokedAccessTokens": "0", "purpose": "reset"},
		"local-user:lus-2": {"revokeAccessTokens": "true", "revokedAccessTokens": "1", "purpose": "reset"},
	}

	for object, changes := range want {
		list := entries(t, s, object)
		if len(list) == 0 {
			t.Fatalf("no entry about %s", object)
		}

		// The list is newest first: the entry of the reset is the first.
		for key, value := range changes {
			if got, ok := changeOf(list[0], key); !ok || got != value {
				t.Errorf("entry of the reset of %s: change %s = %q (%t), want %q", object, key, got, ok, value)
			}
		}
	}
}

func testSetUpKeeps(t *testing.T, s Store) {
	t.Helper()

	u := mustUser(t, s, 1)
	mustToken(t, s, "tok-1", u.PrincipalID, 1)

	r := newReset("lpr-1", u.ID, localuser.PurposeSetUp, at(3))
	r.RevokeAccessTokens = true

	if err := s.CreateReset(t.Context(), r, digest(1), entry("local-user:lus-1", at(3))); err != nil {
		t.Fatalf("create set-up code: %v", err)
	}

	if got := generation(t, s, u.PrincipalID); got != 1 {
		t.Fatalf("session generation after a set-up code = %d, want 1", got)
	}

	if got := revokedAt(t, s, "tok-1"); !got.IsZero() {
		t.Fatalf("token after a set-up code: revoked at %s, want live", got)
	}
}

func testResetUnknownUser(t *testing.T, s Store) {
	t.Helper()

	err := s.CreateReset(t.Context(), newReset("lpr-1", "lus-none", localuser.PurposeReset, at(3)), digest(1),
		entry("local-user:lus-none", at(3)))
	wantErr(t, "a reset code for an unknown user", err, localuser.ErrNotFound)

	mustUser(t, s, 1)

	invalid := []localuser.Reset{
		newReset("lpr-1", "lus-1", "unknown", at(3)),
		newReset("bad-1", "lus-1", localuser.PurposeReset, at(3)),
		func() localuser.Reset {
			r := newReset("lpr-1", "lus-1", localuser.PurposeReset, at(3))
			r.ExpiresAt = r.CreatedAt

			return r
		}(),
		func() localuser.Reset {
			r := newReset("lpr-1", "lus-1", localuser.PurposeReset, at(3))
			r.ExpiresAt = r.CreatedAt.Add(localuser.MaxResetLifetime + time.Minute)

			return r
		}(),
		func() localuser.Reset {
			r := newReset("lpr-1", "lus-1", localuser.PurposeReset, at(3))
			r.Reason = "line one\nline two"

			return r
		}(),
	}

	for i := range invalid {
		err := s.CreateReset(t.Context(), invalid[i], digest(1), entry("local-user:lus-1", at(3)))
		wantErr(t, "an invalid reset code", err, localuser.ErrInvalid)
	}
}

func testRedeemRace(t *testing.T, s Store) {
	t.Helper()

	u := mustUser(t, s, 1)
	d := mustReset(t, s, newReset("lpr-1", u.ID, localuser.PurposeReset, at(3)))

	const callers = 8

	var (
		wg   sync.WaitGroup
		errs = make([]error, callers)
	)

	for i := range callers {
		wg.Go(func() {
			_, errs[i] = redeem(t, s, d, at(4), hash(10+i))
		})
	}

	wg.Wait()

	won := 0

	for i, err := range errs {
		switch {
		case err == nil:
			won++
		case errors.Is(err, localuser.ErrNotFound):
		default:
			t.Errorf("redeem %d: err = %v, want nil or ErrNotFound", i, err)
		}
	}

	if won != 1 {
		t.Fatalf("redeems that won = %d, want 1", won)
	}

	if g := generation(t, s, u.PrincipalID); g != 3 {
		t.Fatalf("session generation = %d, want 3: one for the reset and one for the redeem", g)
	}
}

func testPrune(t *testing.T, s Store) {
	t.Helper()

	u1 := mustUser(t, s, 1)
	u2 := mustUser(t, s, 2)
	u3 := mustUser(t, s, 3)

	mustReset(t, s, newReset("lpr-1", u1.ID, localuser.PurposeSetUp, at(0)))
	mustReset(t, s, newReset("lpr-2", u2.ID, localuser.PurposeSetUp, at(10)))
	live := mustReset(t, s, newReset("lpr-3", u3.ID, localuser.PurposeSetUp, at(30)))

	n, err := s.PruneResets(t.Context(), at(10).Add(localuser.DefaultResetLifetime))
	if err != nil || n != 2 {
		t.Fatalf("prune = %d, %v; want 2 codes that expired at or before the time", n, err)
	}

	if _, err := redeem(t, s, live, at(31), hash(7)); err != nil {
		t.Fatalf("redeem the code that the prune kept: %v", err)
	}
}

func testDigestOnly(t *testing.T, s Store) {
	t.Helper()

	u := mustUser(t, s, 1)
	d := mustReset(t, s, newReset("lpr-1", u.ID, localuser.PurposeSetUp, at(3)))

	// A caller that sent the code, or the digest of the digest, finds
	// nothing: the store holds the digest only.
	for _, other := range []localuser.Digest{localuser.DigestOf("reset code 1x"), localuser.DigestOf(d.String())} {
		_, err := redeem(t, s, other, at(4), hash(5))
		wantErr(t, "redeem another digest", err, localuser.ErrNotFound)
	}

	if _, err := redeem(t, s, d, at(4), hash(5)); err != nil {
		t.Fatalf("redeem the digest: %v", err)
	}
}

func testDigestTaken(t *testing.T, s Store) {
	t.Helper()

	u1 := mustUser(t, s, 1)
	u2 := mustUser(t, s, 2)

	first := mustReset(t, s, newReset("lpr-1", u1.ID, localuser.PurposeSetUp, at(3)))

	err := s.CreateReset(t.Context(), newReset("lpr-2", u2.ID, localuser.PurposeSetUp, at(3)), first,
		entry("local-user:lus-2", at(3)))
	wantErr(t, "a reset code with a taken digest", err, localuser.ErrAlreadyExists)
}
