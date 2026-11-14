package localusersvc_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/localusersvc"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/localuser"
	"opendustpan.org/opendustpan/pkg/localuser/argon2id"
	"opendustpan.org/opendustpan/pkg/localuser/blocklist"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/setting"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// goodPassword passes every default rule.
const goodPassword = "violet-harbor-tin-81-ledger"

// otherPassword passes every default rule too.
const otherPassword = "copper-meadow-quiet-47-lantern"

// adminID is the principal ID of the administrator of the fixture.
const adminID = "prn-admin"

// fixture is a service over a SQLite store with an administrator.
type fixture struct {
	store      *sqlite.Store
	principals *principal.Service
	svc        *localusersvc.Service
	settings   *fakeSettings
	hasher     localuser.PasswordHasher
	sso        bool
}

// fakeSettings answers the values that a test sets, and the default of each
// other declared setting.
type fakeSettings struct {
	values map[string]string
	err    error
}

func (f *fakeSettings) Effective(_ context.Context, _ setting.Scope, key string) (setting.Item, error) {
	if f.err != nil {
		return setting.Effective(localuser.SettingDeclarations()[0], nil, nil), f.err
	}

	for _, d := range localuser.SettingDeclarations() {
		if d.Key != key {
			continue
		}

		value := d.Default
		if v, ok := f.values[key]; ok {
			value = v
		}

		return setting.Item{
			Key: key, Level: d.Level, Kind: d.Kind, Value: value, Default: d.Default, Source: setting.SourceDefault,
			Locked: false, LockedBy: "", DatabaseValue: "", HasDatabaseValue: false, Shadowed: false, Apply: d.Apply,
			Secret: false, Set: true, Database: true, ReducesSecurity: false, Doc: d.Doc,
		}, nil
	}

	return setting.Effective(setting.Declaration{
		Key: key, Level: setting.LevelInstallation, Kind: setting.KindString, Default: "", Secret: false,
		Database: false, StaticCredentialHost: false, Apply: setting.ApplyLive, Check: nil, ReducesSecurity: nil,
		Doc: "unknown",
	}, nil, nil), setting.ErrUnknownKey
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	st, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	principals, err := principal.NewService(principal.Config{
		Store: st, Guard: principal.AdminOnlyGuard{}, Namespaces: nil, Tokens: nil, TempAdmins: nil,
		GroupReach: nil, Now: nil, NewID: nil,
	})
	if err != nil {
		t.Fatalf("principal service: %v", err)
	}

	budget, err := argon2id.NewBudget(localuser.DefaultHashMemoryBudgetKiB)
	if err != nil {
		t.Fatalf("budget: %v", err)
	}

	hasher, err := argon2id.New(budget)
	if err != nil {
		t.Fatalf("hasher: %v", err)
	}

	f := &fixture{
		store: st, principals: principals, svc: nil, settings: &fakeSettings{values: map[string]string{}, err: nil},
		hasher: hasher, sso: false,
	}

	f.svc = f.serviceWith(t, access.ReachConfig{
		Grants: st, Roles: st, Callers: st, Principals: st, Collections: nil, Units: st,
	})

	return f
}

// serviceWith returns a service over the store of the fixture that reads
// the reach of a principal through reach.
func (f *fixture) serviceWith(t *testing.T, reach access.ReachConfig) *localusersvc.Service {
	t.Helper()

	svc, err := localusersvc.New(localusersvc.Config{
		Log:        nil,
		Store:      f.store,
		Principals: f.principals,
		Hasher:     f.hasher,
		Blocklist:  blocklist.Embedded(),
		Audit:      f.store,
		Locks:      f.store,
		Keys:       f.store,
		Settings:   f.settings,
		SSO:        func(context.Context) (bool, error) { return f.sso, nil }, Reach: reach,
		Subjects: f.store,
		Kind:     installation.KindProduction,
		Now:      nil,
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	return svc
}

// base returns the entry of a caller with subject.
func base(subject string) audit.Entry {
	return audit.Entry{
		At: time.Time{}, Namespace: "", Subject: subject, IDPSubject: "", TokenID: "", Action: "", Object: "",
		Version: "", ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: "192.0.2.1", UserAgent: "test",
		Channel: audit.ChannelConsole, Kind: "", Outcome: "", RequestID: "", ObjectID: "", PrevHash: "", Hash: "",
		Sequence: 0, FenceVersion: 0, Reason: "", PrincipalKind: "", JobID: "",
	}
}

// admin is the administrator that makes and resets local users.
func admin() localusersvc.Actor {
	return localusersvc.Actor{Entry: base("corp:admin"), PrincipalID: adminID, Admin: false}
}

// mustCreate makes the local user name with a password and returns it.
func (f *fixture) mustCreate(t *testing.T, name, password string) localusersvc.Created {
	t.Helper()

	c, err := f.svc.CreateUser(t.Context(), admin(), localusersvc.CreateInput{
		UserName: name, DisplayName: "Alice Example", Reason: "new team member",
	})
	if err != nil {
		t.Fatalf("create %s: %v", name, err)
	}

	if password != "" {
		if err := f.svc.Redeem(t.Context(), base(""), c.SetUp.Text, password); err != nil {
			t.Fatalf("redeem the set-up code of %s: %v", name, err)
		}
	}

	return c
}

// generation returns the stored session generation of the person id.
func (f *fixture) generation(t *testing.T, id string) uint64 {
	t.Helper()

	st, err := f.store.PrincipalState(t.Context(), id)
	if err != nil {
		t.Fatalf("session generation: %v", err)
	}

	return st.Generation
}

// entriesWith returns every audit entry of the action.
func (f *fixture) entriesWith(t *testing.T, action string) []audit.Entry {
	t.Helper()

	page, err := f.store.List(t.Context(), audit.ServerScope, audit.Page{Number: 1, Size: 500})
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}

	var out []audit.Entry

	for i := range page.Entries {
		if page.Entries[i].Action == action {
			out = append(out, page.Entries[i])
		}
	}

	return out
}

// TestSignInChecksTheSetUpPassword proves the path of a new local user: the
// set-up code sets the password, and the sign-in returns the person and the
// session generation that the redeem made.
func TestSignInChecksTheSetUpPassword(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	c := f.mustCreate(t, "alice", goodPassword)

	got, err := f.svc.SignIn(t.Context(), base(""), "Alice", goodPassword)
	if err != nil {
		t.Fatalf("sign in: %v", err)
	}

	if got.PrincipalID != c.Principal.ID || got.UserID != c.User.ID {
		t.Fatalf("signed in = %+v, want %s", got, c.Principal.ID)
	}

	if want := f.generation(t, c.Principal.ID); got.SessionGeneration != want || want != 2 {
		t.Fatalf("generation = %d, want the stored %d, which is 2 after the set-up", got.SessionGeneration, want)
	}

	if c.Principal.Subject != principal.LocalSubjectPrefix+c.User.ID {
		t.Fatalf("subject = %q, want the local prefix and the record ID", c.Principal.Subject)
	}
}

// TestSignInFailsWithOneAnswer proves that each failure of the password
// sign-in answers ErrSignInFailed, and that the audit entry records the real
// reason and never the password or the name that the client sent.
func TestSignInFailsWithOneAnswer(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.mustCreate(t, "alice", goodPassword)
	f.mustCreate(t, "nopass", "")
	disabled := f.mustCreate(t, "bob", goodPassword)

	caller := identity.Principal{
		Subject: "admin", DisplayName: "", Admin: true, TokenID: "", TokenExpiresAt: time.Time{},
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Kind: "", Credential: "", JobID: "",
	}
	de := base("admin")
	de.Action = "principal:disable"

	if err := f.principals.DisablePrincipal(t.Context(), de, caller, disabled.Principal.ID); err != nil {
		t.Fatalf("disable: %v", err)
	}

	cases := []struct {
		name, user, password, reason string
	}{
		{name: "unknown name", user: "carol", password: goodPassword, reason: localusersvc.ReasonUnknownName},
		{name: "wrong password", user: "alice", password: otherPassword, reason: localusersvc.ReasonWrongPassword},
		{name: "no password", user: "nopass", password: goodPassword, reason: localusersvc.ReasonNoPassword},
		{name: "disabled", user: "bob", password: goodPassword, reason: localusersvc.ReasonDisabled},
		{name: "name with a newline", user: "alice\r\nx", password: goodPassword, reason: localusersvc.ReasonBoundary},
		{name: "empty name", user: "", password: goodPassword, reason: localusersvc.ReasonBoundary},
		{
			name: "name over the boundary", user: strings.Repeat("a", session.MaxUserName+1), password: goodPassword,
			reason: localusersvc.ReasonBoundary,
		},
		{name: "name not UTF-8", user: "al\xffce", password: goodPassword, reason: localusersvc.ReasonBoundary},
		{name: "empty password", user: "alice", password: "", reason: localusersvc.ReasonBoundary},
		{
			name:     "name too long",
			user:     strings.Repeat("a", 65),
			password: goodPassword,
			reason:   localusersvc.ReasonNameForm,
		},
		{name: "password with NUL", user: "alice", password: "abc\x00def", reason: localusersvc.ReasonPasswordForm},
		{name: "password not UTF-8", user: "alice", password: "\xff\xfe", reason: localusersvc.ReasonPasswordForm},
		{
			name: "password too long", user: "alice", password: strings.Repeat("x", localuser.MaxBytes+1),
			reason: localusersvc.ReasonPasswordForm,
		},
	}

	for _, tc := range cases {
		_, err := f.svc.SignIn(t.Context(), base(""), tc.user, tc.password)
		if !errors.Is(err, localuser.ErrSignInFailed) {
			t.Fatalf("%s: sign in = %v, want ErrSignInFailed", tc.name, err)
		}

		if err != nil && ((tc.password != "" && strings.Contains(err.Error(), tc.password)) ||
			strings.Contains(err.Error(), "alice")) {
			t.Fatalf("%s: the error holds the password or the name: %q", tc.name, err)
		}
	}

	want := make([]string, 0, len(cases))
	for _, tc := range cases {
		want = append(want, tc.reason)
	}

	f.checkFailureEntries(t, want)
}

// checkFailureEntries fails unless the log holds one failure entry of the
// password sign-in for each reason of want, and no entry holds a password or
// a user name.
func (f *fixture) checkFailureEntries(t *testing.T, want []string) {
	t.Helper()

	entries := f.entriesWith(t, localusersvc.ActionSignInPasswordFails)
	if len(entries) != len(want) {
		t.Fatalf("failure entries = %d, want %d", len(entries), len(want))
	}

	reasons := map[string]bool{}

	for i := range entries {
		for _, c := range entries[i].Changes {
			reasons[c.New] = true

			if strings.Contains(c.New, goodPassword) || strings.Contains(c.New, otherPassword) {
				t.Fatalf("an entry holds a password: %+v", entries[i])
			}
		}

		if strings.Contains(entries[i].Object, "carol") || strings.Contains(entries[i].Object, "alice") {
			t.Fatalf("the object of an entry holds a user name: %q", entries[i].Object)
		}
	}

	for _, reason := range want {
		if !reasons[reason] {
			t.Fatalf("no failure entry with the reason %q", reason)
		}
	}
}

// TestPasswordSignInSwitch proves the effective value of the switch: on with
// no identity provider, off with one, and the set value otherwise. While it
// is off, the sign-in, the redeem and a reset answer ErrPasswordSignInOff.
func TestPasswordSignInSwitch(t *testing.T) {
	t.Parallel()

	cases := []switchCase{
		{name: "default without SSO", value: "", sso: false, want: true, source: localusersvc.SourceDefault},
		{name: "default with SSO", value: "", sso: true, want: false, source: localusersvc.SourceDefault},
		{name: "set off", value: "false", sso: false, want: false, source: localusersvc.SourceSet},
		{name: "set on with SSO", value: "true", sso: true, want: true, source: localusersvc.SourceSet},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkSwitch(t, tc)
		})
	}
}

// switchCase is one value of the switch of the password sign-in: the stored
// value, whether an identity provider exists, and the effective value and
// source that the policy must answer.
type switchCase struct {
	name, value string
	source      localusersvc.Source
	sso, want   bool
}

// checkSwitch sets the switch of tc and checks the effective value and the
// answers while the password sign-in is off.
func checkSwitch(t *testing.T, tc switchCase) {
	t.Helper()

	value, sso, want, source := tc.value, tc.sso, tc.want, tc.source

	f := newFixture(t)
	c := f.mustCreate(t, "alice", goodPassword)
	f.sso = sso
	f.settings.values[localuser.SettingEnabled] = value

	p, err := f.svc.Policy(t.Context())
	if err != nil {
		t.Fatalf("policy: %v", err)
	}

	if p.Enabled != want || p.EnabledSource != source {
		t.Fatalf("policy = %+v, want enabled %v from %s", p, want, source)
	}

	if want {
		return
	}

	_, err = f.svc.SignIn(t.Context(), base(""), "alice", goodPassword)
	if !errors.Is(err, localuser.ErrPasswordSignInOff) {
		t.Fatalf("sign in = %v, want ErrPasswordSignInOff", err)
	}

	_, err = f.svc.StartReset(t.Context(), admin(), c.Principal.ID, localusersvc.ResetInput{
		Reason: "lost", RevokeAccessTokens: false,
	})
	if !errors.Is(err, localuser.ErrPasswordSignInOff) {
		t.Fatalf("reset = %v, want ErrPasswordSignInOff", err)
	}
}

// TestSignInNextToSSONeedsASecondFactor proves that a password sign-in that
// an operator turned on next to an identity provider fails closed, because
// the server cannot ask for the second factor yet.
func TestSignInNextToSSONeedsASecondFactor(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.mustCreate(t, "alice", goodPassword)
	f.sso = true
	f.settings.values[localuser.SettingEnabled] = "true"

	_, err := f.svc.SignIn(t.Context(), base(""), "alice", goodPassword)
	if !errors.Is(err, localuser.ErrSecondFactorRequired) {
		t.Fatalf("sign in = %v, want ErrSecondFactorRequired", err)
	}
}

// TestPolicyRefusesHostileSettings sends values that skipped the checks of
// the settings resource, as a changed row would, directly to the policy:
// each one fails closed with ErrUnavailable instead of a weaker rule.
func TestPolicyRefusesHostileSettings(t *testing.T) {
	t.Parallel()

	cases := []struct{ name, key, value string }{
		{name: "switch", key: localuser.SettingEnabled, value: "yes"},
		{name: "minimum below the floor", key: localuser.SettingMinLength, value: "4"},
		{name: "minimum not a number", key: localuser.SettingMinLength, value: "fifteen"},
		{name: "letters and digits", key: localuser.SettingRequireLettersAndDigits, value: "1"},
		{name: "lifetime too long", key: localuser.SettingResetLifetime, value: "720h"},
		{name: "lifetime not a duration", key: localuser.SettingResetLifetime, value: "forever"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			f.settings.values[tc.key] = tc.value

			if _, err := f.svc.Policy(t.Context()); !errors.Is(err, localuser.ErrUnavailable) {
				t.Fatalf("policy = %v, want ErrUnavailable", err)
			}

			_, err := f.svc.SignIn(t.Context(), base(""), "alice", goodPassword)
			if !errors.Is(err, localuser.ErrUnavailable) {
				t.Fatalf("sign in = %v, want ErrUnavailable", err)
			}
		})
	}

	f := newFixture(t)
	f.settings.err = fmt.Errorf("%w: the settings store is down", setting.ErrScope)

	if _, err := f.svc.Policy(t.Context()); !errors.Is(err, localuser.ErrUnavailable) {
		t.Fatalf("policy with a failing store = %v, want ErrUnavailable", err)
	}
}

// TestRedeemRules proves that each password rule refuses a new password with
// its own error, that a refused password keeps the code, and that a code works
// once.
func TestRedeemRules(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	c := f.mustCreate(t, "alice", "")

	cases := []struct {
		want      error
		name      string
		candidate string
	}{
		{name: "too short", candidate: "short-pass-1", want: localuser.ErrPasswordTooShort},
		{name: "too long", candidate: strings.Repeat("ab", 129), want: localuser.ErrPasswordTooLong},
		{name: "control character", candidate: "violet-harbor\ttin-81-ledger", want: localuser.ErrPasswordForm},
		{name: "not UTF-8", candidate: "violet-harbor-\xff-81-ledger", want: localuser.ErrPasswordForm},
		{name: "repeat", candidate: strings.Repeat("z", 20), want: localuser.ErrPasswordBlocked},
		{name: "sequence", candidate: "abcdefghijklmnopq", want: localuser.ErrPasswordBlocked},
		{name: "user name", candidate: "the-ALICE-harbor-81-ledger", want: localuser.ErrPasswordBlocked},
		{name: "display name", candidate: "example-harbor-81-ledger", want: localuser.ErrPasswordBlocked},
		{name: "product name", candidate: "my-opendustpan-81-ledger", want: localuser.ErrPasswordBlocked},
		{name: "common password", candidate: "1qaz2wsx3edc4rfv", want: localuser.ErrPasswordBlocked},
	}

	for _, tc := range cases {
		err := f.svc.Redeem(t.Context(), base(""), c.SetUp.Text, tc.candidate)
		if !errors.Is(err, tc.want) {
			t.Fatalf("%s: redeem = %v, want %v", tc.name, err, tc.want)
		}

		if err != nil && strings.Contains(err.Error(), tc.candidate) {
			t.Fatalf("%s: the error holds the password", tc.name)
		}
	}

	n, ok := localusersvc.MinLengthOf(f.svc.Redeem(t.Context(), base(""), c.SetUp.Text, "short"))
	if !ok || n != localuser.DefaultMinLength {
		t.Fatalf("minimum of a short password = %d, %v; want %d", n, ok, localuser.DefaultMinLength)
	}

	if err := f.svc.Redeem(t.Context(), base(""), c.SetUp.Text, goodPassword); err != nil {
		t.Fatalf("redeem after the refusals: %v", err)
	}

	err := f.svc.Redeem(t.Context(), base(""), c.SetUp.Text, otherPassword)
	if !errors.Is(err, localuser.ErrNotFound) {
		t.Fatalf("second redeem = %v, want ErrNotFound", err)
	}
}

// TestRedeemRefusesHostileCodes sends codes of a wrong form directly to the
// redeem: each one answers ErrNotFound and writes a denied entry that holds
// no part of the code.
func TestRedeemRefusesHostileCodes(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.mustCreate(t, "alice", "")

	codes := []string{
		"", "short", strings.Repeat("A", 42), strings.Repeat("A", 44), strings.Repeat("A", 42) + "=",
		strings.Repeat("A", 42) + "\n", strings.Repeat("A", 42) + "/", strings.Repeat("A", 43),
	}

	for _, code := range codes {
		if err := f.svc.Redeem(t.Context(), base(""), code, goodPassword); !errors.Is(err, localuser.ErrNotFound) {
			t.Fatalf("redeem %q = %v, want ErrNotFound", code, err)
		}
	}

	for _, e := range f.entriesWith(t, localusersvc.ActionResetRefused) {
		if e.Outcome != audit.OutcomeDenied {
			t.Fatalf("refusal outcome = %s, want denied", e.Outcome)
		}

		if strings.Contains(e.Object, "AAAA") || strings.Contains(e.Reason, "AAAA") {
			t.Fatalf("a refusal entry holds the code: %+v", e)
		}
	}
}

// TestResetEndsSessionsAndKeepsOwnAccount proves that a reset adds 1 to the
// session generation, so the generation that an earlier sign-in read is no
// longer the stored one, and that an administrator cannot reset the own
// password or reset with no reason.
func TestResetEndsSessionsAndKeepsOwnAccount(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	c := f.mustCreate(t, "alice", goodPassword)

	in, err := f.svc.SignIn(t.Context(), base(""), "alice", goodPassword)
	if err != nil {
		t.Fatalf("sign in: %v", err)
	}

	refusals := []struct {
		want  error
		name  string
		actor localusersvc.Actor
		in    localusersvc.ResetInput
	}{
		{
			name:  "own account",
			actor: localusersvc.Actor{Entry: base("local:x"), PrincipalID: c.Principal.ID, Admin: false},
			in:    localusersvc.ResetInput{Reason: "lost", RevokeAccessTokens: false}, want: localuser.ErrOwnAccount,
		},
		{
			name: "no reason", actor: admin(), in: localusersvc.ResetInput{Reason: " ", RevokeAccessTokens: false},
			want: localuser.ErrReasonRequired,
		},
		{
			name: "reason with a newline", actor: admin(),
			in:   localusersvc.ResetInput{Reason: "lost\r\nforged", RevokeAccessTokens: false},
			want: localuser.ErrReasonRequired,
		},
	}

	for _, tc := range refusals {
		if _, resetErr := f.svc.StartReset(t.Context(), tc.actor, c.Principal.ID, tc.in); !errors.Is(
			resetErr, tc.want,
		) {
			t.Fatalf("%s: reset = %v, want %v", tc.name, resetErr, tc.want)
		}
	}

	code, err := f.svc.StartReset(t.Context(), admin(), c.Principal.ID, localusersvc.ResetInput{
		Reason: "the person lost the password", RevokeAccessTokens: false,
	})
	if err != nil {
		t.Fatalf("reset: %v", err)
	}

	if g := f.generation(t, c.Principal.ID); g == in.SessionGeneration {
		t.Fatalf("generation after a reset = %d, want another than the sign-in read", g)
	}

	if !code.ExpiresAt.After(time.Now()) || len(code.Text) != 43 {
		t.Fatalf("code = %d characters until %s", len(code.Text), code.ExpiresAt)
	}

	if redeemErr := f.svc.Redeem(t.Context(), base(""), code.Text, otherPassword); redeemErr != nil {
		t.Fatalf("redeem the reset code: %v", redeemErr)
	}

	_, err = f.svc.SignIn(t.Context(), base(""), "alice", goodPassword)
	if !errors.Is(err, localuser.ErrSignInFailed) {
		t.Fatalf("sign in with the old password = %v, want ErrSignInFailed", err)
	}

	if _, err := f.svc.SignIn(t.Context(), base(""), "alice", otherPassword); err != nil {
		t.Fatalf("sign in with the new password: %v", err)
	}
}

// TestChangePassword proves the checks of a change of the own password.
func TestChangePassword(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	c := f.mustCreate(t, "alice", goodPassword)
	me := localusersvc.Actor{Entry: base(c.Principal.Subject), PrincipalID: c.Principal.ID, Admin: false}
	g := f.generation(t, c.Principal.ID)

	refusals := []struct {
		want error
		name string
		in   localusersvc.ChangeInput
	}{
		{
			name: "no current password and no fresh step", want: localuser.ErrCurrentPasswordRequired,
			in: localusersvc.ChangeInput{Current: "", New: otherPassword, Generation: g, Fresh: false},
		},
		{
			name: "wrong current password", want: localuser.ErrWrongPassword,
			in: localusersvc.ChangeInput{Current: otherPassword, New: otherPassword, Generation: g, Fresh: true},
		},
		{
			name: "hostile current password", want: localuser.ErrWrongPassword,
			in: localusersvc.ChangeInput{Current: "\xff\x00", New: otherPassword, Generation: g, Fresh: false},
		},
		{
			name: "new password too short", want: localuser.ErrPasswordTooShort,
			in: localusersvc.ChangeInput{Current: goodPassword, New: "short", Generation: g, Fresh: false},
		},
		{
			name: "no session generation", want: localuser.ErrGenerationChanged,
			in: localusersvc.ChangeInput{Current: goodPassword, New: otherPassword, Generation: 0, Fresh: true},
		},
		{
			name: "a session generation that is not the stored one", want: localuser.ErrGenerationChanged,
			in: localusersvc.ChangeInput{Current: goodPassword, New: otherPassword, Generation: g + 5, Fresh: true},
		},
	}

	for _, tc := range refusals {
		if _, err := f.svc.ChangePassword(t.Context(), me, tc.in); !errors.Is(err, tc.want) {
			t.Fatalf("%s: change = %v, want %v", tc.name, err, tc.want)
		}
	}

	if after := f.generation(t, c.Principal.ID); after != g {
		t.Fatalf("generation after the refusals = %d, want %d", after, g)
	}

	next, err := f.svc.ChangePassword(t.Context(), me, localusersvc.ChangeInput{
		Current: goodPassword, New: otherPassword, Generation: g, Fresh: false,
	})
	if err != nil {
		t.Fatalf("change: %v", err)
	}

	if after := f.generation(t, c.Principal.ID); after != g+1 || next != after {
		t.Fatalf("generation after a change = %d, returned %d, want %d", after, next, g+1)
	}

	// The session of the first change ended: its generation cannot change
	// the password again.
	_, err = f.svc.ChangePassword(t.Context(), me, localusersvc.ChangeInput{
		Current: "", New: goodPassword, Generation: g, Fresh: true,
	})
	if !errors.Is(err, localuser.ErrGenerationChanged) {
		t.Fatalf("change from an ended session = %v, want ErrGenerationChanged", err)
	}

	_, err = f.svc.ChangePassword(t.Context(), me, localusersvc.ChangeInput{
		Current: "", New: goodPassword, Generation: next, Fresh: true,
	})
	if err != nil {
		t.Fatalf("change with a fresh step: %v", err)
	}

	stranger := localusersvc.Actor{Entry: base("corp:bob"), PrincipalID: "prn-nobody", Admin: false}

	_, err = f.svc.ChangePassword(t.Context(), stranger, localusersvc.ChangeInput{
		Current: goodPassword, New: otherPassword, Generation: 1, Fresh: true,
	})
	if !errors.Is(err, localuser.ErrNotFound) {
		t.Fatalf("change of a person with no local user = %v, want ErrNotFound", err)
	}
}

// TestConcurrentChangesFromOneSessionPassOnce proves that two changes from
// one session never both pass: the first ends the session, and the store
// refuses each other change with ErrGenerationChanged.
func TestConcurrentChangesFromOneSessionPassOnce(t *testing.T) {
	t.Parallel()

	const changes = 6

	f := newFixture(t)
	c := f.mustCreate(t, "alice", goodPassword)
	me := localusersvc.Actor{Entry: base(c.Principal.Subject), PrincipalID: c.Principal.ID, Admin: false}
	g := f.generation(t, c.Principal.ID)

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		passed  int
		changed int
		errs    []error
	)

	for range changes {
		wg.Go(func() {
			_, err := f.svc.ChangePassword(t.Context(), me, localusersvc.ChangeInput{
				Current: "", New: otherPassword, Generation: g, Fresh: true,
			})

			mu.Lock()
			defer mu.Unlock()

			switch {
			case err == nil:
				passed++
			case errors.Is(err, localuser.ErrGenerationChanged):
				changed++
			default:
				errs = append(errs, err)
			}
		})
	}

	wg.Wait()

	if err := errors.Join(errs...); err != nil {
		t.Fatalf("a change failed: %v", err)
	}

	if passed != 1 || changed != changes-1 {
		t.Fatalf("%d changes passed and %d were refused, want 1 and %d", passed, changed, changes-1)
	}

	if after := f.generation(t, c.Principal.ID); after != g+1 {
		t.Fatalf("generation = %d, want %d", after, g+1)
	}
}

// TestRemovePasswordKeepsTheLastCredential proves that a removal of the only
// credential fails closed with ErrLastCredential and writes a denied entry.
func TestRemovePasswordKeepsTheLastCredential(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	c := f.mustCreate(t, "alice", goodPassword)

	err := f.svc.RemovePassword(t.Context(), admin(), c.Principal.ID, "leaves the team")
	if !errors.Is(err, localuser.ErrLastCredential) {
		t.Fatalf("remove = %v, want ErrLastCredential", err)
	}

	self := localusersvc.Actor{Entry: base("x"), PrincipalID: c.Principal.ID, Admin: false}

	err = f.svc.RemovePassword(t.Context(), self, c.Principal.ID, "mine")
	if !errors.Is(err, localuser.ErrOwnAccount) {
		t.Fatalf("remove the own password = %v, want ErrOwnAccount", err)
	}

	denied := 0

	for _, e := range f.entriesWith(t, localusersvc.ActionPasswordRemoved) {
		if e.Outcome == audit.OutcomeDenied {
			denied++
		}
	}

	if denied != 2 {
		t.Fatalf("denied removal entries = %d, want 2", denied)
	}

	if _, err := f.svc.SignIn(t.Context(), base(""), "alice", goodPassword); err != nil {
		t.Fatalf("sign in after the refused removal: %v", err)
	}
}

// TestCreateUserRefusesHostileNames sends user names that are not valid
// directly to the create: each one fails with ErrInvalid and makes no
// person.
func TestCreateUserRefusesHostileNames(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	names := []string{"", "Alice", "-alice", "al ice", "alice\n", "ali\x00ce", strings.Repeat("a", 65), "ålice"}

	for _, name := range names {
		_, err := f.svc.CreateUser(t.Context(), admin(), localusersvc.CreateInput{
			UserName: name, DisplayName: "", Reason: "new",
		})
		if !errors.Is(err, localuser.ErrInvalid) {
			t.Fatalf("create %q = %v, want ErrInvalid", name, err)
		}
	}

	page, err := f.principals.ListPrincipals(t.Context(), principal.PageRequest{Prefix: "", Cursor: "", Limit: 100}, "")
	if err != nil {
		t.Fatalf("list principals: %v", err)
	}

	if len(page.Items) != 0 {
		t.Fatalf("principals after refused creates = %d, want 0", len(page.Items))
	}

	f.mustCreate(t, "alice", "")

	_, err = f.svc.CreateUser(t.Context(), admin(), localusersvc.CreateInput{
		UserName: "alice", DisplayName: "", Reason: "again",
	})
	if !errors.Is(err, localuser.ErrAlreadyExists) {
		t.Fatalf("second create of alice = %v, want ErrAlreadyExists", err)
	}
}
