package accesstoken_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// clock is a settable test clock.
type clock struct {
	now time.Time
	mu  sync.Mutex
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.now
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.now = c.now.Add(d)
}

// fixture is a token service over a fresh SQLite store with two principals:
// a person and a service. The workload has no token.
type fixture struct {
	store      *sqlite.Store
	svc        *accesstoken.Service
	principals *principal.Service
	clock      *clock
	person     principal.Principal
	service    principal.Principal
	workload   principal.Principal
}

func caller() audit.Entry {
	return audit.Entry{
		At: time.Time{}, Namespace: "", Subject: "admin", IDPSubject: "", TokenID: "",
		Action: string(identity.ActionServerManageTokens), Object: "", Version: "", ValuePath: "",
		ValueCount: nil, Changes: nil, SourceIP: "192.0.2.1", UserAgent: "test", Channel: audit.ChannelAPI, Kind: "",
		Outcome: "", RequestID: "", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0,
		FenceVersion:  0,
		Reason:        "",
		PrincipalKind: "",
		JobID:         "",
	}
}

func newFixture(t *testing.T, fresh accesstoken.Freshness) fixture {
	t.Helper()

	return newFixtureAt(t, fresh, time.Date(2026, time.October, 14, 12, 0, 0, 0, time.UTC))
}

// sweepStart is the start time of a sweep fixture: the inactivity period
// and one hour before the time of the test. The sweep reads the database
// clock, so a sweep test places its records relative to the real time.
func sweepStart() time.Time {
	return time.Now().UTC().Truncate(time.Millisecond).Add(-accesstoken.DefaultInactivity - time.Hour)
}

// newFixtureAt returns a fixture whose clock starts at start.
func newFixtureAt(t *testing.T, fresh accesstoken.Freshness, start time.Time) fixture {
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

	c := &clock{now: start, mu: sync.Mutex{}}

	principals, err := principal.NewService(
		principal.Config{
			TempAdmins: nil,
			Store:      st,
			Guard:      principal.AdminOnlyGuard{},
			Namespaces: nil,
			Tokens:     nil,
			GroupReach: nil,
			Now:        c.Now,
			NewID:      nil,
		},
	)
	if err != nil {
		t.Fatalf("principal service: %v", err)
	}

	svc, err := accesstoken.NewService(accesstoken.Config{
		TempAdmins: nil,
		Store:      st, Principals: st, Freshness: fresh, Authorizer: nil, Reach: access.ReachConfig{
			Grants: nil, Roles: nil, Callers: nil, Principals: nil, Collections: nil, Units: nil,
		},
		Logger: nil, Now: c.Now, NewID: nil,
	})
	if err != nil {
		t.Fatalf("token service: %v", err)
	}

	var none principal.Principal

	f := fixture{store: st, svc: svc, principals: principals, clock: c, person: none, service: none, workload: none}

	for _, p := range []struct {
		dst     *principal.Principal
		subject string
		kind    principal.Kind
	}{
		{&f.person, "alice@example.com", principal.KindPerson},
		{&f.service, "ci-deploy", principal.KindService},
		{&f.workload, "spiffe://example.org/ci", principal.KindWorkload},
	} {
		*p.dst, err = principals.CreatePrincipal(t.Context(), caller(),
			principal.Input{Subject: p.subject, DisplayName: "", Kind: p.kind})
		if err != nil {
			t.Fatalf("create principal %q: %v", p.subject, err)
		}
	}

	return f
}

// admin is the bootstrap administrator: with no Authorizer wired, it passes
// the check of principal:manage-tokens.
func admin() identity.Principal {
	return identity.Principal{
		TemporaryAdministrator: false,
		TemporaryExpiresAt:     time.Time{},
		Subject:                "admin",
		DisplayName:            "admin",
		Admin:                  true,
		TokenID:                "",
		TokenExpiresAt:         time.Time{},
		Kind:                   "",
		Credential:             "",
		JobID:                  "",
	}
}

// createIn creates a token of the principal of in: the person makes its
// own personal token, and the administrator makes a token of any other
// principal.
func (f *fixture) createIn(ctx context.Context, in accesstoken.Input) (accesstoken.Token, accesstoken.Secret, error) {
	if in.PrincipalID == f.person.ID {
		in.PrincipalID = ""

		return f.svc.Create(ctx, as(f.person), caller(), accesstoken.ScopeOwn, in)
	}

	return f.svc.Create(ctx, admin(), caller(), accesstoken.ScopeService, in)
}

// rotate rotates the token id: the person rotates its own personal token,
// and the administrator rotates a service token.
func (f *fixture) rotate(
	ctx context.Context, id string, in accesstoken.RotateInput,
) (accesstoken.Rotation, accesstoken.Secret, error) {
	if tok, err := f.svc.Get(ctx, id); err == nil && tok.Kind == accesstoken.KindPersonal {
		in.ID, in.Scope = id, accesstoken.ScopeOwn

		return f.svc.Rotate(ctx, as(f.person), caller(), in)
	}

	in.ID, in.Scope = id, accesstoken.ScopeService

	return f.svc.Rotate(ctx, admin(), caller(), in)
}

func (f *fixture) create(t *testing.T, p principal.Principal, name string) (accesstoken.Token, accesstoken.Secret) {
	t.Helper()

	tok, secret, err := f.createIn(t.Context(),
		accesstoken.Input{ExpiresAt: time.Time{}, PrincipalID: p.ID, Name: name})
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	return tok, secret
}

// createLong makes a token that expires in 200 days: after the inactivity
// period, so the sweep and not the expiry ends it.
func (f *fixture) createLong(t *testing.T, p principal.Principal, name string) (accesstoken.Token, accesstoken.Secret) {
	t.Helper()

	tok, secret, err := f.createIn(t.Context(), accesstoken.Input{
		ExpiresAt: f.clock.Now().Add(200 * 24 * time.Hour), PrincipalID: p.ID, Name: name,
	})
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	return tok, secret
}

func TestNewServiceNeedsDependencies(t *testing.T) {
	t.Parallel()

	_, err := accesstoken.NewService(accesstoken.Config{
		TempAdmins: nil,
		Store:      nil, Principals: nil, Freshness: nil, Authorizer: nil, Reach: access.ReachConfig{
			Grants: nil, Roles: nil, Callers: nil, Principals: nil, Collections: nil, Units: nil,
		},
		Logger: nil, Now: nil, NewID: nil,
	})
	if !errors.Is(err, accesstoken.ErrMissingDependency) {
		t.Fatalf("err = %v, want ErrMissingDependency", err)
	}
}

func TestSecretFormAndRedaction(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil)
	_, secret := f.create(t, f.service, "deploy")
	raw := secret.Reveal()

	if !strings.HasPrefix(raw, accesstoken.SecretPrefix) || len(raw) != accesstoken.SecretLength {
		t.Fatalf("secret %q: want the prefix %q and %d bytes", raw, accesstoken.SecretPrefix, accesstoken.SecretLength)
	}

	if !accesstoken.LooksLikeSecret(raw) {
		t.Fatalf("LooksLikeSecret(%q) = false", raw)
	}

	for _, out := range []string{secret.String(), fmt.Sprintf("%v %+v %#v %s", secret, secret, secret, secret)} {
		if strings.Contains(out, raw) || strings.Contains(out, raw[len(accesstoken.SecretPrefix):]) {
			t.Fatalf("formatted secret %q holds the plain value", out)
		}
	}

	_, other := f.create(t, f.service, "deploy 2")
	if other.Reveal() == raw {
		t.Fatal("two creates gave the same secret")
	}
}

func TestLooksLikeSecret(t *testing.T) {
	t.Parallel()

	valid := accesstoken.SecretPrefix + strings.Repeat("A", 43)

	cases := []struct {
		in   string
		want bool
	}{
		{valid, true},
		{"", false},
		{strings.Repeat("A", 47), false},
		{valid + "A", false},
		{valid[:len(valid)-1], false},
		{accesstoken.SecretPrefix + strings.Repeat("!", 43), false},
		{"ODP_" + strings.Repeat("A", 43), false},
	}

	for _, tc := range cases {
		if got := accesstoken.LooksLikeSecret(tc.in); got != tc.want {
			t.Errorf("LooksLikeSecret(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestKindFollowsPrincipal(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil)

	personal, _ := f.create(t, f.person, "laptop")
	service, _ := f.create(t, f.service, "deploy")

	if personal.Kind != accesstoken.KindPersonal || service.Kind != accesstoken.KindService {
		t.Fatalf("kinds = %q, %q; want personal, service", personal.Kind, service.Kind)
	}

	_, _, err := f.createIn(t.Context(),
		accesstoken.Input{ExpiresAt: time.Time{}, PrincipalID: f.workload.ID, Name: "x"})
	if !errors.Is(err, accesstoken.ErrNoTokenForKind) {
		t.Fatalf("workload token: err = %v, want ErrNoTokenForKind", err)
	}

	_, _, err = f.createIn(t.Context(),
		accesstoken.Input{ExpiresAt: time.Time{}, PrincipalID: "prn-none", Name: "x"})
	if !errors.Is(err, accesstoken.ErrPrincipalNotFound) {
		t.Fatalf("token of a missing principal: err = %v, want ErrPrincipalNotFound", err)
	}
}

func TestLifetime(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil)
	now := f.clock.Now()

	tok, _ := f.create(t, f.service, "default")
	if !tok.ExpiresAt.Equal(now.Add(accesstoken.DefaultLifetime)) {
		t.Fatalf("default expiry = %s, want %s", tok.ExpiresAt, now.Add(accesstoken.DefaultLifetime))
	}

	cases := []struct {
		expires time.Time
		want    error
	}{
		{now.Add(time.Hour), nil},
		{now.Add(accesstoken.DefaultMaxLifetime), nil},
		{now.Add(accesstoken.DefaultMaxLifetime + time.Millisecond), accesstoken.ErrInvalid},
		{now, accesstoken.ErrInvalid},
		{now.Add(-time.Hour), accesstoken.ErrInvalid},
	}

	for _, tc := range cases {
		_, _, err := f.createIn(t.Context(),
			accesstoken.Input{ExpiresAt: tc.expires, PrincipalID: f.service.ID, Name: "x"})
		if !errors.Is(err, tc.want) {
			t.Errorf("expiry %s: err = %v, want %v", tc.expires, err, tc.want)
		}
	}
}

func TestValidateName(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		want error
	}{
		{"deploy", nil},
		{"CI für prod", nil},
		{strings.Repeat("a", 100), nil},
		{"", accesstoken.ErrInvalid},
		{strings.Repeat("a", 101), accesstoken.ErrInvalid},
		{" deploy", accesstoken.ErrInvalid},
		{"de\nploy", accesstoken.ErrInvalid},
		{"de\u200eploy", accesstoken.ErrInvalid},
		{"\xff", accesstoken.ErrInvalid},
	}

	for _, tc := range cases {
		if err := accesstoken.ValidateName(tc.name); !errors.Is(err, tc.want) {
			t.Errorf("ValidateName(%q) = %v, want %v", tc.name, err, tc.want)
		}
	}
}

func TestAuthenticate(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil)
	ctx := t.Context()
	tok, secret := f.create(t, f.service, "deploy")

	got, err := f.svc.AuthenticateToken(ctx, secret.Reveal())
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}

	want := identity.Principal{
		TemporaryAdministrator: false,
		TemporaryExpiresAt:     time.Time{},
		Subject:                "ci-deploy",
		DisplayName:            "ci-deploy",
		Admin:                  false,
		TokenID:                tok.ID,
		TokenExpiresAt:         tok.ExpiresAt,
		Kind:                   identity.KindService,
		Credential:             identity.CredentialAccessToken,
		JobID:                  "",
	}
	if got != want {
		t.Fatalf("principal = %+v, want %+v", got, want)
	}

	stored, err := f.svc.Get(ctx, tok.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if !stored.LastUsedAt.Equal(f.clock.Now()) {
		t.Fatalf("last used = %s, want %s", stored.LastUsedAt, f.clock.Now())
	}

	for _, raw := range []string{
		"",
		"not-a-token",
		accesstoken.SecretPrefix + strings.Repeat("A", 43),
		strings.Repeat("x", 64),
	} {
		if _, err := f.svc.AuthenticateToken(ctx, raw); !errors.Is(err, identity.ErrUnauthenticated) {
			t.Errorf("authenticate %q: err = %v, want ErrUnauthenticated", raw, err)
		}
	}
}

// TestAuthenticateFillsTheKindOfThePrincipal proves that a personal token
// signs in a principal of the kind person and a service token one of the kind
// service, each with the credential access-token and no job ID, so a state
// download with a personal token is a reveal and one with a service token is
// not.
func TestAuthenticateFillsTheKindOfThePrincipal(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil)

	cases := []struct {
		name string
		from principal.Principal
		want identity.Kind
	}{
		{"personal token", f.person, identity.KindPerson},
		{"service token", f.service, identity.KindService},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, secret := f.create(t, tc.from, "kind-"+tc.name)

			got, err := f.svc.AuthenticateToken(t.Context(), secret.Reveal())
			if err != nil {
				t.Fatalf("authenticate: %v", err)
			}

			if got.Kind != tc.want || got.Credential != identity.CredentialAccessToken || got.JobID != "" {
				t.Fatalf("kind, credential, job ID = %q, %q, %q; want %q, access-token, empty",
					got.Kind, got.Credential, got.JobID, tc.want)
			}

			if got.ActsAsPerson() != (tc.want == identity.KindPerson) {
				t.Fatalf("ActsAsPerson = %t for kind %q", got.ActsAsPerson(), got.Kind)
			}
		})
	}
}

func TestAuthenticateRefusesEndedTokens(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil)
	ctx := t.Context()

	revoked, revokedSecret := f.create(t, f.service, "revoked")
	if err := f.svc.Revoke(ctx, admin(), caller(), accesstoken.ScopePersonal, revoked.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	_, expiredSecret, err := f.createIn(ctx,
		accesstoken.Input{ExpiresAt: f.clock.Now().Add(time.Hour), PrincipalID: f.service.ID, Name: "short"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	_, personSecret := f.create(t, f.person, "laptop")
	admin := identity.Principal{
		TemporaryAdministrator: false,
		TemporaryExpiresAt:     time.Time{},
		Subject:                "admin",
		DisplayName:            "",
		Admin:                  true,
		TokenID:                "",
		TokenExpiresAt:         time.Time{},
		Kind:                   "",
		Credential:             "",
		JobID:                  "",
	}
	if err := f.principals.DeletePrincipal(ctx, caller(), admin, f.person.ID); err != nil {
		t.Fatalf("delete principal: %v", err)
	}

	f.clock.Advance(time.Hour)

	for name, raw := range map[string]string{
		"revoked": revokedSecret.Reveal(), "expired": expiredSecret.Reveal(), "deleted owner": personSecret.Reveal(),
	} {
		if _, err := f.svc.AuthenticateToken(ctx, raw); !errors.Is(err, identity.ErrUnauthenticated) {
			t.Errorf("%s token: err = %v, want ErrUnauthenticated", name, err)
		}
	}
}

func TestCheckActive(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil)
	ctx := t.Context()

	live, _ := f.create(t, f.service, "live")

	revoked, _ := f.create(t, f.service, "revoked")
	if err := f.svc.Revoke(ctx, admin(), caller(), accesstoken.ScopePersonal, revoked.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	expired, _, err := f.createIn(ctx,
		accesstoken.Input{ExpiresAt: f.clock.Now().Add(time.Hour), PrincipalID: f.service.ID, Name: "short"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	orphan, _ := f.create(t, f.person, "laptop")
	admin := identity.Principal{
		TemporaryAdministrator: false,
		TemporaryExpiresAt:     time.Time{},
		Subject:                "admin",
		DisplayName:            "",
		Admin:                  true,
		TokenID:                "",
		TokenExpiresAt:         time.Time{},
		Kind:                   "",
		Credential:             "",
		JobID:                  "",
	}
	if err := f.principals.DeletePrincipal(ctx, caller(), admin, f.person.ID); err != nil {
		t.Fatalf("delete principal: %v", err)
	}

	f.clock.Advance(time.Hour)

	if err := f.svc.CheckActive(ctx, live.ID); err != nil {
		t.Errorf("live token: err = %v, want nil", err)
	}

	for name, id := range map[string]string{
		"revoked": revoked.ID, "expired": expired.ID, "deleted owner": orphan.ID, "unknown": "acc-unknown",
	} {
		if err := f.svc.CheckActive(ctx, id); !errors.Is(err, identity.ErrUnauthenticated) {
			t.Errorf("%s token: err = %v, want ErrUnauthenticated", name, err)
		}
	}
}

func TestCheckActiveRefusesADisabledPrincipal(t *testing.T) {
	t.Parallel()

	f := newFixtureAt(t, nil, sweepStart())
	ctx := t.Context()

	// Only the account disable is on, so the sweep disables the person and
	// not its token.
	_, err := f.svc.SetPolicy(ctx, caller(), accesstoken.PolicyChange{
		MaxLifetime: nil, Inactivity: nil, DisableTokens: new(false), DisableAccounts: nil, DisableBreakGlass: nil,
		ExchangeLifetime: nil,
	})
	if err != nil {
		t.Fatalf("set policy: %v", err)
	}

	tok, _ := f.createLong(t, f.person, "laptop")

	f.clock.Advance(accesstoken.DefaultInactivity + time.Hour)

	res, err := f.svc.SweepInactive(ctx, caller())
	if err != nil || len(res.Principals) != 1 {
		t.Fatalf("sweep = %+v, %v; want the person disabled", res, err)
	}

	if err := f.svc.CheckActive(ctx, tok.ID); !errors.Is(err, identity.ErrUnauthenticated) {
		t.Fatalf("token of a disabled person: err = %v, want ErrUnauthenticated", err)
	}
}

func TestRotate(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil)
	ctx := t.Context()
	old, oldSecret := f.create(t, f.service, "deploy")

	rot, secret, err := f.rotate(ctx,
		old.ID,
		accesstoken.RotateInput{OldEndsAt: nil, ExpiresAt: time.Time{}, ID: "", Scope: 0},
	)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}

	wantEnd := f.clock.Now().Add(accesstoken.DefaultRotationOverlap)
	if !rot.Old.ExpiresAt.Equal(wantEnd) || rot.Old.ReplacedBy != rot.New.ID {
		t.Fatalf("old token = %+v, want the end %s and the new ID", rot.Old, wantEnd)
	}

	if rot.New.Name != "deploy" || rot.New.Kind != accesstoken.KindService || rot.New.PrincipalID != f.service.ID {
		t.Fatalf("new token = %+v, want the name, the kind and the owner of the old one", rot.New)
	}

	for _, raw := range []string{oldSecret.Reveal(), secret.Reveal()} {
		if _, authErr := f.svc.AuthenticateToken(ctx, raw); authErr != nil {
			t.Fatalf("authenticate during the overlap: %v", authErr)
		}
	}

	_, _, err = f.rotate(ctx, old.ID, accesstoken.RotateInput{OldEndsAt: nil, ExpiresAt: time.Time{}, ID: "", Scope: 0})
	if !errors.Is(err, accesstoken.ErrEnded) {
		t.Fatalf("second rotation: err = %v, want ErrEnded", err)
	}

	f.clock.Advance(accesstoken.DefaultRotationOverlap)

	if _, err := f.svc.AuthenticateToken(ctx, oldSecret.Reveal()); !errors.Is(err, identity.ErrUnauthenticated) {
		t.Fatalf("old token after its end: err = %v, want ErrUnauthenticated", err)
	}

	if _, err := f.svc.AuthenticateToken(ctx, secret.Reveal()); err != nil {
		t.Fatalf("new token after the overlap: %v", err)
	}
}

func TestRotationEnd(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil)
	ctx := t.Context()
	now := f.clock.Now()

	short, _, err := f.createIn(ctx,
		accesstoken.Input{ExpiresAt: now.Add(time.Hour), PrincipalID: f.service.ID, Name: "short"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	rot, _, err := f.rotate(ctx,
		short.ID,
		accesstoken.RotateInput{OldEndsAt: nil, ExpiresAt: time.Time{}, ID: "", Scope: 0},
	)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}

	if !rot.Old.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("default end = %s, want the old expiry %s", rot.Old.ExpiresAt, now.Add(time.Hour))
	}

	cases := []struct {
		end  time.Time
		want error
	}{
		{now.Add(-time.Second), accesstoken.ErrInvalid},
		{now.Add(accesstoken.DefaultLifetime + time.Hour), accesstoken.ErrInvalid},
		{now, nil},
	}

	for _, tc := range cases {
		tok, _ := f.create(t, f.service, "case")

		end := tc.end
		_, _, err := f.rotate(ctx,
			tok.ID,
			accesstoken.RotateInput{OldEndsAt: &end, ExpiresAt: time.Time{}, ID: "", Scope: 0},
		)

		if !errors.Is(err, tc.want) {
			t.Errorf("end %s: err = %v, want %v", tc.end, err, tc.want)
		}
	}
}

// listFixture makes three tokens a second apart, two of the service and
// one of the person, and revokes the first.
func listFixture(t *testing.T) fixture {
	t.Helper()

	f := newFixture(t, nil)

	first, _ := f.create(t, f.service, "one")
	f.clock.Advance(time.Second)
	f.create(t, f.service, "two")
	f.clock.Advance(time.Second)
	f.create(t, f.person, "three")

	if err := f.svc.Revoke(t.Context(), admin(), caller(), accesstoken.ScopePersonal, first.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	return f
}

func mustList(t *testing.T, f *fixture, req accesstoken.PageRequest) accesstoken.Page {
	t.Helper()

	page, err := f.svc.List(t.Context(), req)
	if err != nil {
		t.Fatalf("list %+v: %v", req, err)
	}

	return page
}

func TestListPages(t *testing.T) {
	t.Parallel()

	f := listFixture(t)

	page := mustList(t, &f, accesstoken.PageRequest{
		PrincipalID: "", Cursor: "", Filter: accesstoken.FilterActive, Limit: 1,
	})
	if len(page.Items) != 1 || page.Items[0].Name != "three" || page.Total != 2 || page.NextCursor == "" {
		t.Fatalf("page 1 = %+v, want three, total 2 and a cursor", page)
	}

	page = mustList(t, &f, accesstoken.PageRequest{
		PrincipalID: "", Cursor: page.NextCursor, Filter: accesstoken.FilterActive, Limit: 1,
	})
	if len(page.Items) != 1 || page.Items[0].Name != "two" || page.NextCursor != "" {
		t.Fatalf("page 2 = %+v, want two and no cursor", page)
	}
}

func TestListEnded(t *testing.T) {
	t.Parallel()

	f := listFixture(t)

	for _, tc := range []struct {
		filter accesstoken.Filter
		want   int
	}{
		{accesstoken.FilterAll, 2},
		{accesstoken.FilterEnded, 1},
		{accesstoken.FilterActive, 1},
		{accesstoken.FilterExpiring, 0},
	} {
		page := mustList(t, &f, accesstoken.PageRequest{
			PrincipalID: f.service.ID, Cursor: "", Filter: tc.filter, Limit: 0,
		})
		if len(page.Items) != tc.want || page.Total != int64(tc.want) {
			t.Fatalf("tokens of the service with status %q = %+v, want %d", tc.filter, page, tc.want)
		}
	}
}

func TestListRefusesBadRequests(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil)

	for _, req := range []accesstoken.PageRequest{
		{PrincipalID: "", Cursor: "", Filter: accesstoken.FilterAll, Limit: accesstoken.MaxLimit + 1},
		{PrincipalID: "", Cursor: "", Filter: "other", Limit: 0},
		{PrincipalID: "", Cursor: "", Filter: accesstoken.FilterAll, Limit: -1},
		{PrincipalID: "", Cursor: "@@", Filter: accesstoken.FilterAll, Limit: 0},
		{PrincipalID: "", Cursor: strings.Repeat("A", accesstoken.MaxCursorLength+1), Filter: accesstoken.FilterAll, Limit: 0},
		{PrincipalID: "a b", Cursor: "", Filter: accesstoken.FilterAll, Limit: 0},
	} {
		if _, err := f.svc.List(t.Context(), req); !errors.Is(err, accesstoken.ErrInvalid) {
			t.Errorf("list %+v: err = %v, want ErrInvalid", req, err)
		}
	}
}

// staleMFA asks for a new MFA step for every operation.
type staleMFA struct{}

var errStale = errors.New("mfa step too old")

func (staleMFA) RequireFresh(context.Context, string) error { return errStale }

func TestFreshnessHook(t *testing.T) {
	t.Parallel()

	f := newFixture(t, staleMFA{})

	_, _, err := f.createIn(t.Context(),
		accesstoken.Input{ExpiresAt: time.Time{}, PrincipalID: f.service.ID, Name: "x"})
	if !errors.Is(err, errStale) {
		t.Fatalf("create: err = %v, want the freshness error", err)
	}

	_, _, err = f.rotate(t.Context(),
		"tok-any",
		accesstoken.RotateInput{OldEndsAt: nil, ExpiresAt: time.Time{}, ID: "", Scope: 0},
	)
	if !errors.Is(err, errStale) {
		t.Fatalf("rotate: err = %v, want the freshness error", err)
	}
}

func TestRef(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"tok-abc":    "token:tok-abc",
		"prn-abc":    "token:invalid",
		"tok-a b":    "token:invalid",
		"":           "token:invalid",
		"tok-../etc": "token:invalid",
	}

	for in, want := range cases {
		if got := accesstoken.Ref(in); got != want {
			t.Errorf("Ref(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDigest(t *testing.T) {
	t.Parallel()

	d := accesstoken.Digest{1, 2, 3}

	parsed, err := accesstoken.ParseDigest(d.String())
	if err != nil || !parsed.Equal(d) {
		t.Fatalf("ParseDigest(String()) = %s, %v; want %s", parsed, err, d)
	}

	for _, bad := range []string{"", "zz", strings.Repeat("a", 62)} {
		if _, err := accesstoken.ParseDigest(bad); !errors.Is(err, accesstoken.ErrInvalid) {
			t.Errorf("ParseDigest(%q) err = %v, want ErrInvalid", bad, err)
		}
	}
}

// as returns the caller with the subject of p, not an administrator.
func as(p principal.Principal) identity.Principal {
	return identity.Principal{
		TemporaryAdministrator: false,
		TemporaryExpiresAt:     time.Time{},
		Subject:                p.Subject,
		DisplayName:            p.DisplayName,
		Admin:                  false,
		TokenID:                "",
		TokenExpiresAt:         time.Time{},
		Kind:                   "",
		Credential:             "",
		JobID:                  "",
	}
}

func TestOwnTokens(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil)
	ctx := t.Context()
	alice := as(f.person)

	own, _, err := f.svc.Create(ctx, alice, caller(), accesstoken.ScopeOwn,
		accesstoken.Input{ExpiresAt: time.Time{}, PrincipalID: "ignored", Name: "laptop"})
	if err != nil {
		t.Fatalf("create own token: %v", err)
	}

	if own.PrincipalID != f.person.ID || own.Kind != accesstoken.KindPersonal {
		t.Fatalf("own token = %+v, want a personal token of the person", own)
	}

	page, err := f.svc.ListOwn(ctx, alice, accesstoken.PageRequest{
		PrincipalID: f.service.ID, Cursor: "", Filter: accesstoken.FilterAll, Limit: 0,
	})
	if err != nil || page.Total != 1 || page.Items[0].ID != own.ID {
		t.Fatalf("own list = %+v, %v; want the own token only", page, err)
	}

	rotateOwn := accesstoken.RotateInput{
		OldEndsAt: nil,
		ExpiresAt: time.Time{},
		ID:        own.ID,
		Scope:     accesstoken.ScopeOwn,
	}
	if _, _, rotErr := f.svc.Rotate(ctx, alice, caller(), rotateOwn); rotErr != nil {
		t.Fatalf("rotate own token: %v", rotErr)
	}

	// A token of another principal is not found through the own routes.
	other, _ := f.create(t, f.service, "deploy")

	err = f.svc.Revoke(ctx, alice, caller(), accesstoken.ScopeOwn, other.ID)
	if !errors.Is(err, accesstoken.ErrNotFound) {
		t.Fatalf("revoke of another principal's token as own: err = %v, want ErrNotFound", err)
	}

	// A service, a workload and the bootstrap admin own no personal token.
	for name, c := range map[string]identity.Principal{
		"service": as(f.service), "workload": as(f.workload), "bootstrap admin": admin(),
	} {
		_, _, err := f.svc.Create(ctx, c, caller(), accesstoken.ScopeOwn,
			accesstoken.Input{ExpiresAt: time.Time{}, PrincipalID: "", Name: "x"})
		if !errors.Is(err, accesstoken.ErrNoOwnPrincipal) {
			t.Errorf("own token of the %s: err = %v, want ErrNoOwnPrincipal", name, err)
		}
	}
}

func TestScopesFitTheKind(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil)
	ctx := t.Context()

	_, _, err := f.svc.Create(ctx, admin(), caller(), accesstoken.ScopeService,
		accesstoken.Input{ExpiresAt: time.Time{}, PrincipalID: f.person.ID, Name: "x"})
	if !errors.Is(err, accesstoken.ErrWrongKind) {
		t.Errorf("create for a person in the service scope: err = %v, want ErrWrongKind", err)
	}

	// The personal scope makes no token at all, whatever the kind.
	_, _, err = f.svc.Create(ctx, admin(), caller(), accesstoken.ScopePersonal,
		accesstoken.Input{ExpiresAt: time.Time{}, PrincipalID: f.service.ID, Name: "x"})
	if !errors.Is(err, accesstoken.ErrNotOwnToken) {
		t.Errorf("create for a service in the personal scope: err = %v, want ErrNotOwnToken", err)
	}

	service, _ := f.create(t, f.service, "deploy")

	_, _, err = f.svc.Rotate(
		ctx,
		admin(),
		caller(),
		accesstoken.RotateInput{
			OldEndsAt: nil,
			ExpiresAt: time.Time{},
			ID:        service.ID,
			Scope:     accesstoken.ScopePersonal,
		},
	)
	if !errors.Is(err, accesstoken.ErrNotOwnToken) {
		t.Fatalf("rotate a service token in the personal scope: err = %v, want ErrNotOwnToken", err)
	}

	// A revoke never raises access: an administrator revokes any kind.
	if err := f.svc.Revoke(ctx, admin(), caller(), accesstoken.ScopePersonal, service.ID); err != nil {
		t.Fatalf("revoke a service token: %v", err)
	}
}

func TestEscalationCheck(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil)
	ctx := t.Context()
	alice := as(f.person)

	// With no Authorizer, only an administrator can act for a service
	// principal, and nobody makes a personal token of another person: the
	// checks fail closed.
	_, _, err := f.svc.Create(ctx, alice, caller(), accesstoken.ScopeService,
		accesstoken.Input{ExpiresAt: time.Time{}, PrincipalID: f.service.ID, Name: "x"})
	if !errors.Is(err, identity.ErrDenied) {
		t.Fatalf("create for a service principal with no authorizer: err = %v, want ErrDenied", err)
	}

	_, _, err = f.svc.Create(ctx, alice, caller(), accesstoken.ScopePersonal,
		accesstoken.Input{ExpiresAt: time.Time{}, PrincipalID: f.person.ID, Name: "x"})
	if !errors.Is(err, identity.ErrDenied) {
		t.Fatalf("create for another person: err = %v, want ErrDenied", err)
	}

	tok, _ := f.create(t, f.service, "deploy")

	_, _, err = f.svc.Rotate(
		ctx,
		alice,
		caller(),
		accesstoken.RotateInput{OldEndsAt: nil, ExpiresAt: time.Time{}, ID: tok.ID, Scope: accesstoken.ScopeService},
	)
	if !errors.Is(err, identity.ErrDenied) {
		t.Fatalf("rotate for a service principal with no authorizer: err = %v, want ErrDenied", err)
	}

	revokeErr := f.svc.Revoke(ctx, alice, caller(), accesstoken.ScopePersonal, tok.ID)
	if !errors.Is(revokeErr, identity.ErrDenied) {
		t.Fatalf("revoke of a service token with no authorizer: err = %v, want ErrDenied", revokeErr)
	}

	laptop, _ := f.create(t, f.person, "laptop")

	if err := f.svc.Revoke(ctx, alice, caller(), accesstoken.ScopePersonal, laptop.ID); err != nil {
		t.Fatalf("revoke of a personal token needs no grant check: %v", err)
	}
}

// recordAuthorizer allows the actions on the objects in allowed and
// records each check.
type recordAuthorizer struct {
	allowed map[string]bool
	checked []string
	mu      sync.Mutex
}

func (a *recordAuthorizer) Authorize(
	_ context.Context, p identity.Principal, action identity.Action, obj identity.Object,
) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	check := string(action) + " " + obj.String()
	a.checked = append(a.checked, check)

	if !a.allowed[check] {
		return fmt.Errorf("%w: %s may not %s", identity.ErrDenied, p.Subject, check)
	}

	return nil
}

// ListObjects gives an administrator every object and any other principal none.
func (*recordAuthorizer) ListObjects(
	_ context.Context,
	p identity.Principal,
	_ identity.Action,
) (identity.Scope, error) {
	return identity.AdminScope(p), nil
}

// newCheckedService returns a token service over the store of f with the
// authorizer authz. The reach config reads the store, so the reach check
// finds no hold of alice and passes it: the authorizer decides alone.
func newCheckedService(t *testing.T, f *fixture, authz identity.Authorizer) *accesstoken.Service {
	t.Helper()

	svc, err := accesstoken.NewService(accesstoken.Config{
		TempAdmins: nil,
		Store:      f.store, Principals: f.store, Freshness: nil, Authorizer: authz,
		Reach: access.ReachConfig{
			Grants: f.store, Roles: f.store, Callers: f.store, Principals: f.store, Collections: nil, Units: nil,
		},
		Logger: nil,
		Now:    f.clock.Now, NewID: nil,
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	return svc
}

// TestServiceTokensNeedManageTokens proves that principal:manage-tokens on
// the service principal decides a service token: a create, a rotation and a
// revoke each ask it, and a principal without it is refused.
func TestServiceTokensNeedManageTokens(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil)
	ctx := t.Context()

	other, err := f.principals.CreatePrincipal(ctx, caller(),
		principal.Input{Subject: "ops-bot", DisplayName: "", Kind: principal.KindService})
	if err != nil {
		t.Fatalf("create principal: %v", err)
	}

	want := string(identity.ActionPrincipalManageTokens) + " principal:" + f.service.ID
	authz := &recordAuthorizer{allowed: map[string]bool{want: true}, checked: nil, mu: sync.Mutex{}}
	svc := newCheckedService(t, &f, authz)
	alice := as(f.person)

	tok, _, err := svc.Create(ctx, alice, caller(), accesstoken.ScopeService,
		accesstoken.Input{ExpiresAt: time.Time{}, PrincipalID: f.service.ID, Name: "deploy"})
	if err != nil {
		t.Fatalf("create with principal:manage-tokens: %v", err)
	}

	rot, _, err := svc.Rotate(ctx, alice, caller(),
		accesstoken.RotateInput{OldEndsAt: nil, ExpiresAt: time.Time{}, ID: tok.ID, Scope: accesstoken.ScopeService})
	if err != nil {
		t.Fatalf("rotate with principal:manage-tokens: %v", err)
	}

	if revErr := svc.Revoke(ctx, alice, caller(), accesstoken.ScopePersonal, rot.New.ID); revErr != nil {
		t.Fatalf("revoke with principal:manage-tokens: %v", revErr)
	}

	_, _, err = svc.Create(ctx, alice, caller(), accesstoken.ScopeService,
		accesstoken.Input{ExpiresAt: time.Time{}, PrincipalID: other.ID, Name: "deploy"})
	if !errors.Is(err, identity.ErrDenied) {
		t.Fatalf("create without principal:manage-tokens: err = %v, want ErrDenied", err)
	}

	otherTok, _ := f.create(t, other, "deploy")

	revokeErr := svc.Revoke(ctx, alice, caller(), accesstoken.ScopePersonal, otherTok.ID)
	if !errors.Is(revokeErr, identity.ErrDenied) {
		t.Fatalf("revoke without principal:manage-tokens: err = %v, want ErrDenied", revokeErr)
	}

	if len(authz.checked) != 5 || authz.checked[0] != want {
		t.Fatalf("authorizer checks = %q, want five, the first %q", authz.checked, want)
	}
}

func TestOwnCreateAndRotateNeedASignIn(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil)
	ctx := t.Context()

	tok, _ := f.create(t, f.person, "laptop")

	// The person signs in with its token: it can list and revoke its own
	// tokens, but it cannot make or rotate one.
	withToken := as(f.person)
	withToken.TokenID = tok.ID

	_, _, err := f.svc.Create(ctx, withToken, caller(), accesstoken.ScopeOwn,
		accesstoken.Input{ExpiresAt: time.Time{}, PrincipalID: "", Name: "more"})
	if !errors.Is(err, accesstoken.ErrSignInRequired) {
		t.Fatalf("own create with a token: err = %v, want ErrSignInRequired", err)
	}

	rotate := accesstoken.RotateInput{OldEndsAt: nil, ExpiresAt: time.Time{}, ID: tok.ID, Scope: accesstoken.ScopeOwn}
	_, _, rotErr := f.svc.Rotate(ctx, withToken, caller(), rotate)
	if !errors.Is(rotErr, accesstoken.ErrSignInRequired) {
		t.Fatalf("own rotation with a token: err = %v, want ErrSignInRequired", rotErr)
	}

	page, err := f.svc.ListOwn(ctx, withToken, accesstoken.PageRequest{
		PrincipalID: "", Cursor: "", Filter: accesstoken.FilterAll, Limit: 0,
	})
	if err != nil || page.Total != 1 {
		t.Fatalf("own list with a token = %+v, %v; want the one token", page, err)
	}

	if err := f.svc.Revoke(ctx, withToken, caller(), accesstoken.ScopeOwn, tok.ID); err != nil {
		t.Fatalf("own revoke with a token: %v", err)
	}
}

func TestCreatedByAndDaysLeft(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil)

	tok, _ := f.create(t, f.service, "deploy")
	if tok.CreatedBy != "admin" {
		t.Fatalf("created by = %q, want admin", tok.CreatedBy)
	}

	days, active := tok.DaysLeft(f.clock.Now())
	if !active || days != int64(accesstoken.DefaultLifetime/(24*time.Hour)) {
		t.Fatalf("days left = %d, %v; want %d, true", days, active, accesstoken.DefaultLifetime/(24*time.Hour))
	}

	f.clock.Advance(accesstoken.DefaultLifetime)

	if _, active := tok.DaysLeft(f.clock.Now()); active {
		t.Fatal("an expired token has days left")
	}
}

func TestPolicyBoundsLifetime(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil)
	ctx := t.Context()

	p, err := f.svc.SetPolicy(ctx, caller(), accesstoken.PolicyChange{
		MaxLifetime: new(time.Hour), Inactivity: nil, DisableTokens: nil, DisableAccounts: nil,
		DisableBreakGlass: nil,
		ExchangeLifetime:  nil,
	})
	if err != nil {
		t.Fatalf("set policy: %v", err)
	}

	if p.MaxLifetime != time.Hour || p.Inactivity != accesstoken.DefaultInactivity ||
		!p.UpdatedAt.Equal(f.clock.Now()) {
		t.Fatalf("policy = %+v, want a maximum of 1h and the other defaults", p)
	}

	tok, _ := f.create(t, f.service, "short")
	if !tok.ExpiresAt.Equal(f.clock.Now().Add(time.Hour)) {
		t.Fatalf("default expiry = %s, want the maximum lifetime of 1h", tok.ExpiresAt)
	}

	_, _, err = f.createIn(ctx, accesstoken.Input{
		ExpiresAt: f.clock.Now().Add(2 * time.Hour), PrincipalID: f.service.ID, Name: "long",
	})
	if !errors.Is(err, accesstoken.ErrInvalid) {
		t.Fatalf("create past the maximum: err = %v, want ErrInvalid", err)
	}

	for _, c := range []accesstoken.PolicyChange{
		{MaxLifetime: new(time.Duration(0)), Inactivity: nil, DisableTokens: nil, DisableAccounts: nil, DisableBreakGlass: nil, ExchangeLifetime: nil},
		{
			MaxLifetime: nil, Inactivity: new(time.Millisecond), DisableTokens: nil, DisableAccounts: nil,
			DisableBreakGlass: nil,
			ExchangeLifetime:  nil,
		},
		{
			MaxLifetime: nil, Inactivity: new(accesstoken.MinInactivity - time.Minute), DisableTokens: nil,
			DisableAccounts: nil, DisableBreakGlass: nil,
			ExchangeLifetime: nil,
		},
		{
			MaxLifetime: new(accesstoken.MaxPolicyDuration + time.Hour), Inactivity: nil, DisableTokens: nil,
			DisableAccounts: nil, DisableBreakGlass: nil,
			ExchangeLifetime: nil,
		},
		{MaxLifetime: nil, Inactivity: nil, DisableTokens: nil, DisableAccounts: nil, DisableBreakGlass: new(true), ExchangeLifetime: nil},
	} {
		if _, err := f.svc.SetPolicy(ctx, caller(), c); !errors.Is(err, accesstoken.ErrInvalid) {
			t.Errorf("set policy %+v: err = %v, want ErrInvalid", c, err)
		}
	}
}

func TestSweepDisablesInactiveTokensAndAccounts(t *testing.T) {
	t.Parallel()

	f := newFixtureAt(t, nil, sweepStart())
	ctx := t.Context()

	_, serviceSecret := f.createLong(t, f.service, "deploy")
	_, personSecret := f.createLong(t, f.person, "laptop")
	_, usedSecret := f.createLong(t, f.service, "used")

	f.clock.Advance(accesstoken.DefaultInactivity - time.Hour)

	if _, err := f.svc.AuthenticateToken(ctx, usedSecret.Reveal()); err != nil {
		t.Fatalf("authenticate: %v", err)
	}

	f.clock.Advance(2 * time.Hour)

	res, err := f.svc.SweepInactive(ctx, caller())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}

	if len(res.Tokens) != 2 || len(res.Principals) != 1 || res.Principals[0] != f.person.ID {
		t.Fatalf("sweep = %+v, want two tokens and the person", res)
	}

	for name, raw := range map[string]string{"service": serviceSecret.Reveal(), "person": personSecret.Reveal()} {
		if _, err := f.svc.AuthenticateToken(ctx, raw); !errors.Is(err, identity.ErrUnauthenticated) {
			t.Errorf("%s token after the sweep: err = %v, want ErrUnauthenticated", name, err)
		}
	}

	if _, err := f.svc.AuthenticateToken(ctx, usedSecret.Reveal()); err != nil {
		t.Fatalf("used token after the sweep: %v", err)
	}

	// A new token of the disabled person does not sign in until an
	// administrator enables the person again.
	_, fresh := f.createLong(t, f.person, "new laptop")
	if _, err := f.svc.AuthenticateToken(ctx, fresh.Reveal()); !errors.Is(err, identity.ErrUnauthenticated) {
		t.Fatalf("token of a disabled person: err = %v, want ErrUnauthenticated", err)
	}

	if err := f.principals.EnablePrincipal(ctx, caller(), admin(), f.person.ID); err != nil {
		t.Fatalf("enable: %v", err)
	}

	if _, err := f.svc.AuthenticateToken(ctx, fresh.Reveal()); err != nil {
		t.Fatalf("token of an enabled person: %v", err)
	}
}

func TestSweepNeverDisablesBreakGlass(t *testing.T) {
	t.Parallel()

	f := newFixtureAt(t, nil, sweepStart())
	ctx := t.Context()

	at := f.clock.Now()
	glass := principal.Principal{
		CreatedAt: at, UpdatedAt: at, DisabledAt: time.Time{}, ID: "prn-glass",
		Subject: "glass@example.com", DisplayName: "Break Glass", Kind: principal.KindPerson,
		Source: principal.SourceLocal, Namespace: "", GroupCount: 0, BreakGlass: true,
	}

	e := caller()
	e.At, e.Namespace, e.Kind, e.Outcome, e.Object = at, audit.ServerScope, audit.KindChange,
		audit.OutcomeSuccess, principal.Ref(glass.ID)

	if err := f.store.CreatePrincipal(ctx, glass, e); err != nil {
		t.Fatalf("create break-glass principal: %v", err)
	}

	_, glassSecret := func() (accesstoken.Token, accesstoken.Secret) {
		t.Helper()

		tok, secret, err := f.svc.Create(ctx, as(glass), caller(), accesstoken.ScopeOwn,
			accesstoken.Input{
				ExpiresAt: f.clock.Now().Add(200 * 24 * time.Hour), PrincipalID: "", Name: "emergency",
			})
		if err != nil {
			t.Fatalf("create token of a break-glass account: %v", err)
		}

		return tok, secret
	}()

	f.createLong(t, f.person, "laptop")

	f.clock.Advance(accesstoken.DefaultInactivity + time.Hour)

	res, err := f.svc.SweepInactive(ctx, caller())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}

	for _, id := range res.Principals {
		if id == glass.ID {
			t.Fatalf("sweep = %+v, disabled a break-glass account", res)
		}
	}

	if len(res.Principals) != 1 || res.Principals[0] != f.person.ID {
		t.Fatalf("sweep = %+v, want only the idle person", res)
	}

	got, err := f.store.GetPrincipal(ctx, glass.ID)
	if err != nil || !got.DisabledAt.IsZero() || !got.BreakGlass {
		t.Fatalf("break-glass principal = %+v, %v; want it enabled with the flag", got, err)
	}

	if _, err := f.svc.AuthenticateToken(ctx, glassSecret.Reveal()); err != nil {
		t.Fatalf("token of a break-glass account after the sweep: %v", err)
	}
}

func TestSweepFollowsThePolicy(t *testing.T) {
	t.Parallel()

	off := accesstoken.PolicyChange{
		MaxLifetime:       nil,
		Inactivity:        new(time.Duration(0)),
		DisableTokens:     nil,
		DisableAccounts:   nil,
		DisableBreakGlass: nil,
		ExchangeLifetime:  nil,
	}
	tokensOnly := accesstoken.PolicyChange{
		MaxLifetime: nil, Inactivity: nil, DisableTokens: nil, DisableAccounts: new(false), DisableBreakGlass: nil,
		ExchangeLifetime: nil,
	}

	cases := []struct {
		change         accesstoken.PolicyChange
		name           string
		wantTokens     int
		wantPrincipals int
	}{
		{change: off, name: "off", wantTokens: 0, wantPrincipals: 0},
		{change: tokensOnly, name: "tokens only", wantTokens: 1, wantPrincipals: 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixtureAt(t, nil, sweepStart())
			ctx := t.Context()

			if _, err := f.svc.SetPolicy(ctx, caller(), tc.change); err != nil {
				t.Fatalf("set policy: %v", err)
			}

			f.createLong(t, f.person, "laptop")
			f.clock.Advance(accesstoken.DefaultInactivity + time.Hour)

			res, err := f.svc.SweepInactive(ctx, caller())
			if err != nil {
				t.Fatalf("sweep: %v", err)
			}

			if len(res.Tokens) != tc.wantTokens || len(res.Principals) != tc.wantPrincipals {
				t.Fatalf("sweep = %+v, want %d tokens and %d principals", res, tc.wantTokens, tc.wantPrincipals)
			}
		})
	}
}

func TestWarningFor(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.October, 14, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour

	cases := []struct {
		left time.Duration
		want accesstoken.ExpiryWarning
	}{
		{30 * day, accesstoken.WarningNone},
		{14*day + time.Second, accesstoken.WarningNone},
		{14 * day, accesstoken.WarningSoon},
		{3*day + time.Second, accesstoken.WarningSoon},
		{3 * day, accesstoken.WarningImminent},
		{time.Second, accesstoken.WarningImminent},
		{0, accesstoken.WarningNone},
		{-day, accesstoken.WarningNone},
	}

	for _, tc := range cases {
		if got := accesstoken.WarningFor(now.Add(tc.left), now); got != tc.want {
			t.Errorf("WarningFor(%s left) = %q, want %q", tc.left, got, tc.want)
		}
	}
}

func TestExpiryWarningOfAnEndedToken(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil)
	ctx := t.Context()

	tok, _, err := f.createIn(ctx, accesstoken.Input{
		ExpiresAt: f.clock.Now().Add(24 * time.Hour), PrincipalID: f.service.ID, Name: "short",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if got := tok.ExpiryWarningAt(f.clock.Now()); got != accesstoken.WarningImminent {
		t.Fatalf("warning = %q, want imminent", got)
	}

	if revokeErr := f.svc.Revoke(ctx, admin(), caller(), accesstoken.ScopePersonal, tok.ID); revokeErr != nil {
		t.Fatalf("revoke: %v", revokeErr)
	}

	revoked, err := f.svc.Get(ctx, tok.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if got := revoked.ExpiryWarningAt(f.clock.Now()); got != accesstoken.WarningNone {
		t.Fatalf("warning of a revoked token = %q, want none", got)
	}
}

func TestBreakGlassDisableIsRefused(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil)

	_, err := f.svc.SetPolicy(t.Context(), caller(), accesstoken.PolicyChange{
		MaxLifetime: nil, Inactivity: nil, DisableTokens: nil, DisableAccounts: nil, DisableBreakGlass: new(true),
		ExchangeLifetime: nil,
	})
	if !errors.Is(err, accesstoken.ErrInvalid) || !strings.Contains(err.Error(), "no longer supported") {
		t.Fatalf("err = %v, want ErrInvalid that says the setting is no longer supported", err)
	}

	_, err = f.svc.SetPolicy(t.Context(), caller(), accesstoken.PolicyChange{
		MaxLifetime: nil, Inactivity: nil, DisableTokens: nil, DisableAccounts: nil, DisableBreakGlass: new(false),
		ExchangeLifetime: nil,
	})
	if err != nil {
		t.Fatalf("set policy with the deprecated field false: %v", err)
	}

	p, err := f.svc.Policy(t.Context())
	if err != nil || p.DisableBreakGlass {
		t.Fatalf("policy = %+v, %v; want the deprecated field always false", p, err)
	}
}

func TestInactiveTokenIsRefusedBeforeTheSweep(t *testing.T) {
	t.Parallel()

	f := newFixtureAt(t, nil, sweepStart())
	ctx := t.Context()

	tok, secret := f.createLong(t, f.service, "idle")

	f.clock.Advance(accesstoken.DefaultInactivity + time.Minute)

	if _, err := f.svc.AuthenticateToken(ctx, secret.Reveal()); !errors.Is(err, identity.ErrUnauthenticated) {
		t.Fatalf("inactive token: err = %v, want ErrUnauthenticated", err)
	}

	// The refused request does not record a use, so it cannot keep the
	// token alive.
	got, err := f.svc.Get(ctx, tok.ID)
	if err != nil || !got.LastUsedAt.IsZero() {
		t.Fatalf("token after a refused request = %+v, %v; want no last use", got, err)
	}

	// With the token disable off, the token signs in again at once: a change
	// through the Service drops the cached policy.
	_, err = f.svc.SetPolicy(ctx, caller(), accesstoken.PolicyChange{
		MaxLifetime: nil, Inactivity: nil, DisableTokens: new(false), DisableAccounts: nil, DisableBreakGlass: nil,
		ExchangeLifetime: nil,
	})
	if err != nil {
		t.Fatalf("set policy: %v", err)
	}

	if _, err := f.svc.AuthenticateToken(ctx, secret.Reveal()); err != nil {
		t.Fatalf("token with the disable off: %v", err)
	}
}

// TestPolicyChangeOfAnotherInstanceAppliesAtOnce proves that a policy change
// that another instance writes applies to the next token lookup of this
// instance: the lookup reads the policy with the token, so no instance keeps
// an old policy for a while.
func TestPolicyChangeOfAnotherInstanceAppliesAtOnce(t *testing.T) {
	t.Parallel()

	f := newFixtureAt(t, nil, sweepStart())
	ctx := t.Context()

	_, first := f.createLong(t, f.service, "first")
	_, second := f.createLong(t, f.service, "second")

	_, err := f.svc.SetPolicy(ctx, caller(), accesstoken.PolicyChange{
		MaxLifetime: nil, Inactivity: nil, DisableTokens: new(false), DisableAccounts: nil, DisableBreakGlass: nil,
		ExchangeLifetime: nil,
	})
	if err != nil {
		t.Fatalf("set policy: %v", err)
	}

	f.clock.Advance(accesstoken.DefaultInactivity + time.Minute)

	if _, authErr := f.svc.AuthenticateToken(ctx, first.Reveal()); authErr != nil {
		t.Fatalf("token with the disable off: %v", authErr)
	}

	// Another instance turns the disable on.
	_, err = f.store.UpdatePolicy(ctx, func(old accesstoken.Policy) (accesstoken.Policy, audit.Entry, error) {
		old.DisableTokens = true
		e := caller()
		e.At, e.Namespace, e.Kind, e.Outcome, e.Object = f.clock.Now(), audit.ServerScope, audit.KindChange,
			audit.OutcomeSuccess, accesstoken.PolicyRef

		return old, e, nil
	})
	if err != nil {
		t.Fatalf("update policy in the store: %v", err)
	}

	if _, err := f.svc.AuthenticateToken(ctx, second.Reveal()); !errors.Is(err, identity.ErrUnauthenticated) {
		t.Fatalf("inactive token right after the change of another instance: err = %v, want ErrUnauthenticated", err)
	}
}

// failingPolicy is a store whose policy read fails.
type failingPolicy struct {
	*sqlite.Store
}

var errPolicyRead = errors.New("policy read failed")

func (failingPolicy) GetPolicy(context.Context) (accesstoken.Policy, error) {
	return accesstoken.Policy{}, errPolicyRead
}

// TestPolicyReadErrorFailsClosed proves that the account inactivity check
// of another sign-in, which reads the policy on its own, answers a failed
// read with its error.
func TestPolicyReadErrorFailsClosed(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil)

	svc, err := accesstoken.NewService(accesstoken.Config{
		TempAdmins: nil,
		Store:      failingPolicy{f.store}, Principals: f.store, Freshness: nil, Authorizer: nil,
		Reach: access.ReachConfig{
			Grants: nil, Roles: nil, Callers: nil, Principals: nil, Collections: nil, Units: nil,
		}, Logger: nil,
		Now: f.clock.Now, NewID: nil,
	})
	if err != nil {
		t.Fatalf("token service: %v", err)
	}

	owner := accesstoken.Owner{
		ActiveAt: f.clock.Now(), DisabledAt: time.Time{}, ID: "pri-reader", Subject: "alice", DisplayName: "Alice",
		Kind: principal.KindPerson, BreakGlass: false,
	}

	if _, err := svc.AccountInactive(t.Context(), owner); !errors.Is(err, errPolicyRead) {
		t.Fatalf("account inactivity with a failed policy read: err = %v, want the read error", err)
	}
}

func TestInactiveAccountIsRefusedBeforeTheSweep(t *testing.T) {
	t.Parallel()

	f := newFixtureAt(t, nil, sweepStart())
	ctx := t.Context()

	// Only the account disable is on, so the token rule refuses nothing.
	_, err := f.svc.SetPolicy(ctx, caller(), accesstoken.PolicyChange{
		MaxLifetime: nil, Inactivity: nil, DisableTokens: new(false), DisableAccounts: nil, DisableBreakGlass: nil,
		ExchangeLifetime: nil,
	})
	if err != nil {
		t.Fatalf("set policy: %v", err)
	}

	personTok, personSecret := f.createLong(t, f.person, "laptop")
	_, serviceSecret := f.createLong(t, f.service, "deploy")

	f.clock.Advance(accesstoken.DefaultInactivity + time.Minute)

	_, authErr := f.svc.AuthenticateToken(ctx, personSecret.Reveal())
	if !errors.Is(authErr, identity.ErrUnauthenticated) {
		t.Fatalf("token of an inactive person: err = %v, want ErrUnauthenticated", authErr)
	}

	// The refused request records no use, so it cannot make the account
	// active again.
	got, err := f.svc.Get(ctx, personTok.ID)
	if err != nil || !got.LastUsedAt.IsZero() {
		t.Fatalf("token after a refused request = %+v, %v; want no last use", got, err)
	}

	// The account rule covers persons only.
	if _, err := f.svc.AuthenticateToken(ctx, serviceSecret.Reveal()); err != nil {
		t.Fatalf("token of a service: %v", err)
	}
}

// TestAccountInactiveAt proves the account rule of the token lookup, which
// a console session applies too: the period, the account switch, persons
// only, and the break-glass exemption.
func TestAccountInactiveAt(t *testing.T) {
	t.Parallel()

	now := sweepStart()
	policy := accesstoken.DefaultPolicy()

	owner := func(kind principal.Kind, breakGlass bool, activeAt time.Time) accesstoken.Owner {
		return accesstoken.Owner{
			ActiveAt: activeAt, DisabledAt: time.Time{}, ID: "prn-x", Subject: "x", DisplayName: "X", Kind: kind,
			BreakGlass: breakGlass,
		}
	}

	old := now.Add(-accesstoken.DefaultInactivity)
	recent := now.Add(-accesstoken.DefaultInactivity + time.Minute)

	for _, tc := range []struct {
		name string
		o    accesstoken.Owner
		want bool
	}{
		{name: "a person active within the period", o: owner(principal.KindPerson, false, recent), want: false},
		{name: "a person past the period", o: owner(principal.KindPerson, false, old), want: true},
		{name: "a break-glass person past the period", o: owner(principal.KindPerson, true, old), want: false},
		{name: "a service past the period", o: owner(principal.KindService, false, old), want: false},
	} {
		if got := policy.AccountInactiveAt(tc.o, now); got != tc.want {
			t.Errorf("%s: AccountInactiveAt = %t, want %t", tc.name, got, tc.want)
		}
	}

	off := policy
	off.DisableAccounts = false

	if off.AccountInactiveAt(owner(principal.KindPerson, false, old), now) {
		t.Fatal("AccountInactiveAt with the account disable off = true, want false")
	}

	zero := policy
	zero.Inactivity = 0

	if zero.AccountInactiveAt(owner(principal.KindPerson, false, old), now) {
		t.Fatal("AccountInactiveAt with a zero period = true, want false")
	}
}

func TestAdministratorCreateAndRotateNeedASignIn(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil)
	ctx := t.Context()

	personTok, _ := f.create(t, f.person, "laptop")
	serviceTok, _ := f.create(t, f.service, "deploy")

	// A caller that authenticated with an access token cannot make or
	// rotate a token in any scope, an administrator scope included: the
	// new token would outlive a revoke of the one that made it.
	withToken := admin()
	withToken.TokenID = personTok.ID

	creates := []struct {
		scope accesstoken.Scope
		owner string
	}{
		{accesstoken.ScopePersonal, f.person.ID},
		{accesstoken.ScopeService, f.service.ID},
	}
	for _, c := range creates {
		_, _, err := f.svc.Create(ctx, withToken, caller(), c.scope,
			accesstoken.Input{ExpiresAt: time.Time{}, PrincipalID: c.owner, Name: "more"})
		if !errors.Is(err, accesstoken.ErrSignInRequired) {
			t.Errorf("create in scope %d with a token: err = %v, want ErrSignInRequired", c.scope, err)
		}
	}

	rotations := []struct {
		scope accesstoken.Scope
		id    string
	}{
		{accesstoken.ScopePersonal, personTok.ID},
		{accesstoken.ScopeService, serviceTok.ID},
	}
	for _, r := range rotations {
		in := accesstoken.RotateInput{OldEndsAt: nil, ExpiresAt: time.Time{}, ID: r.id, Scope: r.scope}
		if _, _, err := f.svc.Rotate(ctx, withToken, caller(), in); !errors.Is(err, accesstoken.ErrSignInRequired) {
			t.Errorf("rotation in scope %d with a token: err = %v, want ErrSignInRequired", r.scope, err)
		}
	}
}

// TestListOfPrincipal proves the checks of the token list of one service
// principal in the Service: principal:manage-tokens on the principal first,
// so a caller without it learns nothing about which principals exist; then
// a service principal only, and the list of that principal alone.
func TestListOfPrincipal(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil)
	ctx := t.Context()

	manage := string(identity.ActionPrincipalManageTokens) + " principal:"
	authz := &recordAuthorizer{allowed: map[string]bool{
		manage + f.service.ID:  true,
		manage + f.person.ID:   true,
		manage + f.workload.ID: true,
		manage + "prn-none":    true,
	}, checked: nil, mu: sync.Mutex{}}
	svc := newCheckedService(t, &f, authz)
	alice := as(f.person)

	f.create(t, f.service, "deploy")
	f.create(t, f.person, "laptop")

	all := accesstoken.PageRequest{PrincipalID: f.person.ID, Cursor: "", Filter: accesstoken.FilterAll, Limit: 0}

	page, err := svc.ListOfPrincipal(ctx, alice, f.service.ID, all)
	if err != nil || page.Total != 1 || page.Items[0].PrincipalID != f.service.ID {
		t.Fatalf("tokens of the service principal = %+v, %v; want its one token", page, err)
	}

	if _, err := svc.ListOfPrincipal(ctx, alice, f.person.ID, all); !errors.Is(err, accesstoken.ErrWrongKind) {
		t.Fatalf("tokens of a person: err = %v, want ErrWrongKind", err)
	}

	if _, err := svc.ListOfPrincipal(ctx, alice, "prn-none", all); !errors.Is(err, accesstoken.ErrPrincipalNotFound) {
		t.Fatalf("tokens of an unknown principal: err = %v, want ErrPrincipalNotFound", err)
	}

	if _, err := svc.ListOfPrincipal(ctx, alice, f.workload.ID, all); !errors.Is(err, accesstoken.ErrWrongKind) {
		t.Fatalf("tokens of a workload: err = %v, want ErrWrongKind", err)
	}

	authz.allowed = map[string]bool{}

	for _, id := range []string{f.service.ID, f.person.ID, "prn-none"} {
		_, err := svc.ListOfPrincipal(ctx, alice, id, all)
		if !errors.Is(err, identity.ErrDenied) || !strings.Contains(err.Error(), "list the tokens of") {
			t.Fatalf("tokens of %s without principal:manage-tokens: err = %v, want ErrDenied for a list", id, err)
		}
	}
}

// namespacedReader reads the principals of the store, and reports the
// principal id as a principal of the namespace acme.
type namespacedReader struct {
	accesstoken.PrincipalReader

	id string
}

func (r namespacedReader) GetPrincipal(ctx context.Context, id string) (principal.Principal, error) {
	p, err := r.PrincipalReader.GetPrincipal(ctx, id)
	if err == nil && p.ID == r.id {
		p.Namespace = "acme"
	}

	return p, err
}

// TestNamespacePrincipalTokens proves that the service makes, rotates and
// lists the tokens of a principal of a namespace for a holder of
// principal:manage-tokens on it, and refuses a caller without it. The
// token signs in as the principal.
func TestNamespacePrincipalTokens(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil)
	ctx := t.Context()
	reader := namespacedReader{PrincipalReader: f.store, id: f.service.ID}
	manage := string(identity.ActionPrincipalManageTokens) + " principal:" + f.service.ID
	alice := as(f.person)

	holder, err := accesstoken.NewService(accesstoken.Config{
		TempAdmins: nil,
		Store:      f.store, Principals: reader, Freshness: nil,
		Authorizer: &recordAuthorizer{allowed: map[string]bool{manage: true}, checked: nil, mu: sync.Mutex{}},
		Reach: access.ReachConfig{
			Grants: f.store, Roles: f.store, Callers: f.store, Principals: f.store, Collections: nil, Units: nil,
		},
		Logger: nil,
		Now:    f.clock.Now, NewID: nil,
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	denied, err := accesstoken.NewService(accesstoken.Config{
		TempAdmins: nil,
		Store:      f.store, Principals: reader, Freshness: nil,
		Authorizer: &recordAuthorizer{allowed: map[string]bool{}, checked: nil, mu: sync.Mutex{}},
		Reach: access.ReachConfig{
			Grants: f.store, Roles: f.store, Callers: f.store, Principals: f.store, Collections: nil, Units: nil,
		},
		Logger: nil,
		Now:    f.clock.Now, NewID: nil,
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	made, secret, createErr := holder.Create(ctx, alice, caller(), accesstoken.ScopeService,
		accesstoken.Input{ExpiresAt: time.Time{}, PrincipalID: f.service.ID, Name: "deploy"})
	if createErr != nil {
		t.Fatalf("create for a holder: %v", createErr)
	}

	_, _, deniedCreateErr := denied.Create(ctx, alice, caller(), accesstoken.ScopeService,
		accesstoken.Input{ExpiresAt: time.Time{}, PrincipalID: f.service.ID, Name: "more"})
	if !errors.Is(deniedCreateErr, identity.ErrDenied) {
		t.Fatalf("create without principal:manage-tokens = %v, want ErrDenied", deniedCreateErr)
	}

	all := accesstoken.PageRequest{PrincipalID: "", Cursor: "", Filter: accesstoken.FilterAll, Limit: 0}

	page, listErr := holder.ListOfPrincipal(ctx, alice, f.service.ID, all)
	if listErr != nil || page.Total < 1 {
		t.Fatalf("list for a holder = %+v, %v; want at least the made token", page, listErr)
	}

	_, deniedListErr := denied.ListOfPrincipal(ctx, alice, f.service.ID, all)
	if !errors.Is(deniedListErr, identity.ErrDenied) {
		t.Fatalf("list without principal:manage-tokens = %v, want ErrDenied", deniedListErr)
	}

	rot, _, rotateErr := holder.Rotate(ctx, alice, caller(), accesstoken.RotateInput{
		ID: made.ID, Scope: accesstoken.ScopeService, ExpiresAt: time.Time{}, OldEndsAt: nil,
	})
	if rotateErr != nil {
		t.Fatalf("rotate for a holder: %v", rotateErr)
	}

	_, _, deniedRotateErr := denied.Rotate(ctx, alice, caller(), accesstoken.RotateInput{
		ID: rot.New.ID, Scope: accesstoken.ScopeService, ExpiresAt: time.Time{}, OldEndsAt: nil,
	})
	if !errors.Is(deniedRotateErr, identity.ErrDenied) {
		t.Fatalf("rotate without principal:manage-tokens = %v, want ErrDenied", deniedRotateErr)
	}

	got, authErr := holder.AuthenticateToken(ctx, secret.Reveal())
	if authErr != nil || got.Subject != f.service.Subject {
		t.Fatalf("authenticate = %+v, %v; want the service subject", got, authErr)
	}
}

// TestSetPolicyRecordsChanges proves the audit entry of a policy change
// names each field that changed with its old and new text, and names no
// field that kept its value.
func TestSetPolicyRecordsChanges(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil)
	ctx := t.Context()

	lifetime := 30 * 24 * time.Hour
	disabled := false

	_, err := f.svc.SetPolicy(ctx, caller(), accesstoken.PolicyChange{
		MaxLifetime: new(lifetime), Inactivity: nil, DisableTokens: new(disabled), DisableAccounts: nil,
		DisableBreakGlass: nil,
		ExchangeLifetime:  nil,
	})
	if err != nil {
		t.Fatalf("set policy: %v", err)
	}

	want := map[string][2]string{
		"maxLifetimeSeconds": {"31536000", "2592000"},
		"disableTokens":      {"true", "false"},
	}
	got := policyEntryChanges(t, &f)
	checkChanges(t, got, want)

	// The maximum is already 30 days, so only the inactivity period changed.
	hour := time.Hour

	_, err = f.svc.SetPolicy(ctx, caller(), accesstoken.PolicyChange{
		MaxLifetime: new(lifetime), Inactivity: new(hour), DisableTokens: nil, DisableAccounts: nil,
		DisableBreakGlass: nil,
		ExchangeLifetime:  nil,
	})
	if err != nil {
		t.Fatalf("set policy: %v", err)
	}

	want = map[string][2]string{"inactivitySeconds": {"7776000", "3600"}}
	got = policyEntryChanges(t, &f)
	checkChanges(t, got, want)
}

// policyEntryChanges returns the changes of the latest token policy audit
// entry in the scope @server.
func policyEntryChanges(t *testing.T, f *fixture) []audit.Change {
	t.Helper()

	listing, err := f.store.List(t.Context(), audit.ServerScope, audit.Page{Number: 1, Size: audit.DefaultPageSize})
	if err != nil {
		t.Fatalf("list @server entries: %v", err)
	}

	for i := range listing.Entries {
		if listing.Entries[i].Object == accesstoken.PolicyRef {
			return listing.Entries[i].Changes
		}
	}

	t.Fatalf("no %q entry in @server", accesstoken.PolicyRef)

	return nil
}

// checkChanges proves changes hold exactly the keys of want with its old
// and new text.
func checkChanges(t *testing.T, changes []audit.Change, want map[string][2]string) {
	t.Helper()

	if len(changes) != len(want) {
		t.Fatalf("changes = %+v, want %d items", changes, len(want))
	}

	for _, c := range changes {
		pair, ok := want[c.Key]
		if !ok {
			t.Fatalf("change %q = %+v, want one of %v", c.Key, c, want)
		}

		if c.Old == nil || *c.Old != pair[0] || c.New != pair[1] {
			t.Fatalf("change %q = %v to %q, want %q to %q", c.Key, c.Old, c.New, pair[0], pair[1])
		}

		if c.Layer != "" || c.TakesEffect != "" {
			t.Fatalf("change %q holds layer %q and takes effect %q, want both empty", c.Key, c.Layer, c.TakesEffect)
		}
	}
}

// TestNoPersonalTokenOfAnotherPerson proves that no principal makes or
// rotates a personal token of another person, an administrator included,
// and that an administrator still revokes one. The person makes and rotates
// its own personal tokens.
func TestNoPersonalTokenOfAnotherPerson(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil)
	ctx := t.Context()

	_, _, err := f.svc.Create(ctx, admin(), caller(), accesstoken.ScopePersonal,
		accesstoken.Input{ExpiresAt: time.Time{}, PrincipalID: f.person.ID, Name: "laptop"})
	if !errors.Is(err, identity.ErrDenied) || !errors.Is(err, accesstoken.ErrNotOwnToken) {
		t.Fatalf("administrator create for a person: err = %v, want ErrDenied and ErrNotOwnToken", err)
	}

	tok, _, err := f.svc.Create(ctx, as(f.person), caller(), accesstoken.ScopeOwn,
		accesstoken.Input{ExpiresAt: time.Time{}, PrincipalID: "", Name: "laptop"})
	if err != nil {
		t.Fatalf("own create: %v", err)
	}

	_, _, err = f.svc.Rotate(ctx, admin(), caller(),
		accesstoken.RotateInput{OldEndsAt: nil, ExpiresAt: time.Time{}, ID: tok.ID, Scope: accesstoken.ScopePersonal})
	if !errors.Is(err, identity.ErrDenied) || !errors.Is(err, accesstoken.ErrNotOwnToken) {
		t.Fatalf("administrator rotation of a personal token: err = %v, want ErrDenied and ErrNotOwnToken", err)
	}

	rot, _, err := f.svc.Rotate(ctx, as(f.person), caller(),
		accesstoken.RotateInput{OldEndsAt: nil, ExpiresAt: time.Time{}, ID: tok.ID, Scope: accesstoken.ScopeOwn})
	if err != nil {
		t.Fatalf("own rotation: %v", err)
	}

	if err := f.svc.Revoke(ctx, admin(), caller(), accesstoken.ScopePersonal, rot.New.ID); err != nil {
		t.Fatalf("administrator revoke of a personal token: %v", err)
	}
}
