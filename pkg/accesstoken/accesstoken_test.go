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
		ValueCount: nil, SourceIP: "192.0.2.1", UserAgent: "test", Channel: audit.ChannelAPI, Kind: "",
		Outcome: "", RequestID: "", PrevHash: "", Hash: "", Sequence: 0,
	}
}

func newFixture(t *testing.T, fresh accesstoken.Freshness) fixture {
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

	c := &clock{now: time.Date(2026, time.October, 14, 12, 0, 0, 0, time.UTC), mu: sync.Mutex{}}

	principals, err := principal.NewService(principal.Config{Store: st, Guard: nil, Now: c.Now, NewID: nil})
	if err != nil {
		t.Fatalf("principal service: %v", err)
	}

	svc, err := accesstoken.NewService(accesstoken.Config{
		Store: st, Principals: st, Freshness: fresh, Grants: nil, Logger: nil, Now: c.Now, NewID: nil,
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

// admin is the bootstrap administrator: it passes the escalation check
// with no GrantCheck wired.
func admin() identity.Principal {
	return identity.Principal{
		Subject:        "admin",
		DisplayName:    "admin",
		Admin:          true,
		TokenID:        "",
		TokenExpiresAt: time.Time{},
	}
}

// scopeFor returns the administrator scope of the tokens of the principal
// id: personal for the person, service for every other principal.
func (f fixture) scopeFor(id string) accesstoken.Scope {
	if id == f.person.ID {
		return accesstoken.ScopePersonal
	}

	return accesstoken.ScopeService
}

// createIn creates a token as the administrator, in the scope that fits
// the principal.
func (f fixture) createIn(ctx context.Context, in accesstoken.Input) (accesstoken.Token, accesstoken.Secret, error) {
	return f.svc.Create(ctx, admin(), caller(), f.scopeFor(in.PrincipalID), in)
}

// rotate rotates the token id as the administrator, in the scope that
// fits its kind.
func (f fixture) rotate(
	ctx context.Context, id string, in accesstoken.RotateInput,
) (accesstoken.Rotation, accesstoken.Secret, error) {
	scope := accesstoken.ScopeService

	if tok, err := f.svc.Get(ctx, id); err == nil && tok.Kind == accesstoken.KindPersonal {
		scope = accesstoken.ScopePersonal
	}

	in.ID, in.Scope = id, scope

	return f.svc.Rotate(ctx, admin(), caller(), in)
}

func (f fixture) create(t *testing.T, p principal.Principal, name string) (accesstoken.Token, accesstoken.Secret) {
	t.Helper()

	tok, secret, err := f.createIn(t.Context(),
		accesstoken.Input{ExpiresAt: time.Time{}, PrincipalID: p.ID, Name: name})
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	return tok, secret
}

func TestNewServiceNeedsDependencies(t *testing.T) {
	t.Parallel()

	_, err := accesstoken.NewService(accesstoken.Config{
		Store: nil, Principals: nil, Freshness: nil, Grants: nil, Logger: nil, Now: nil, NewID: nil,
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
		{now.Add(accesstoken.MaxLifetime), nil},
		{now.Add(accesstoken.MaxLifetime + time.Millisecond), accesstoken.ErrInvalid},
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
		Subject:        "ci-deploy",
		DisplayName:    "ci-deploy",
		Admin:          false,
		TokenID:        tok.ID,
		TokenExpiresAt: tok.ExpiresAt,
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
		Subject:        "admin",
		DisplayName:    "",
		Admin:          true,
		TokenID:        "",
		TokenExpiresAt: time.Time{},
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

func mustList(t *testing.T, f fixture, req accesstoken.PageRequest) accesstoken.Page {
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

	page := mustList(t, f, accesstoken.PageRequest{
		PrincipalID: "", Cursor: "", Filter: accesstoken.FilterActive, Limit: 1,
	})
	if len(page.Items) != 1 || page.Items[0].Name != "three" || page.Total != 2 || page.NextCursor == "" {
		t.Fatalf("page 1 = %+v, want three, total 2 and a cursor", page)
	}

	page = mustList(t, f, accesstoken.PageRequest{
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
		page := mustList(t, f, accesstoken.PageRequest{
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
		Subject:        p.Subject,
		DisplayName:    p.DisplayName,
		Admin:          false,
		TokenID:        "",
		TokenExpiresAt: time.Time{},
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

	cases := []struct {
		target principal.Principal
		scope  accesstoken.Scope
	}{
		{f.service, accesstoken.ScopePersonal},
		{f.person, accesstoken.ScopeService},
	}

	for _, tc := range cases {
		_, _, err := f.svc.Create(ctx, admin(), caller(), tc.scope,
			accesstoken.Input{ExpiresAt: time.Time{}, PrincipalID: tc.target.ID, Name: "x"})
		if !errors.Is(err, accesstoken.ErrWrongKind) {
			t.Errorf("create for %s in scope %d: err = %v, want ErrWrongKind", tc.target.Kind, tc.scope, err)
		}
	}

	service, _ := f.create(t, f.service, "deploy")

	_, _, err := f.svc.Rotate(
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
	if !errors.Is(err, accesstoken.ErrWrongKind) {
		t.Fatalf("rotate a service token in the personal scope: err = %v, want ErrWrongKind", err)
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

	// With no GrantCheck, only an administrator can act for another
	// principal: the check fails closed.
	_, _, err := f.svc.Create(ctx, alice, caller(), accesstoken.ScopeService,
		accesstoken.Input{ExpiresAt: time.Time{}, PrincipalID: f.service.ID, Name: "x"})
	if !errors.Is(err, identity.ErrDenied) {
		t.Fatalf("create for another principal with no grant check: err = %v, want ErrDenied", err)
	}

	tok, _ := f.create(t, f.service, "deploy")

	_, _, err = f.svc.Rotate(
		ctx,
		alice,
		caller(),
		accesstoken.RotateInput{OldEndsAt: nil, ExpiresAt: time.Time{}, ID: tok.ID, Scope: accesstoken.ScopeService},
	)
	if !errors.Is(err, identity.ErrDenied) {
		t.Fatalf("rotate for another principal with no grant check: err = %v, want ErrDenied", err)
	}

	if err := f.svc.Revoke(ctx, alice, caller(), accesstoken.ScopePersonal, tok.ID); err != nil {
		t.Fatalf("revoke needs no escalation check: %v", err)
	}
}

// recordGrants answers the escalation check from a set of allowed
// principals and records each principal it checks.
type recordGrants struct {
	allowed map[string]bool
	checked []string
	mu      sync.Mutex
}

func (g *recordGrants) RequireHeld(_ context.Context, _ identity.Principal, principalID string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.checked = append(g.checked, principalID)

	if !g.allowed[principalID] {
		return fmt.Errorf("%w: holds less than %s", identity.ErrDenied, principalID)
	}

	return nil
}

func TestEscalationCheckUsesTheGrants(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil)
	ctx := t.Context()

	grants := &recordGrants{allowed: map[string]bool{f.service.ID: true}, checked: nil, mu: sync.Mutex{}}

	svc, err := accesstoken.NewService(accesstoken.Config{
		Store: f.store, Principals: f.store, Freshness: nil, Grants: grants, Logger: nil, Now: f.clock.Now, NewID: nil,
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	alice := as(f.person)

	allowed := accesstoken.Input{ExpiresAt: time.Time{}, PrincipalID: f.service.ID, Name: "x"}
	if _, _, createErr := svc.Create(ctx, alice, caller(), accesstoken.ScopeService, allowed); createErr != nil {
		t.Fatalf("create for an allowed principal: %v", createErr)
	}

	other, err := f.principals.CreatePrincipal(ctx, caller(),
		principal.Input{Subject: "ops-bot", DisplayName: "", Kind: principal.KindService})
	if err != nil {
		t.Fatalf("create principal: %v", err)
	}

	_, _, err = svc.Create(ctx, alice, caller(), accesstoken.ScopeService,
		accesstoken.Input{ExpiresAt: time.Time{}, PrincipalID: other.ID, Name: "x"})
	if !errors.Is(err, identity.ErrDenied) {
		t.Fatalf("create for a principal that holds more: err = %v, want ErrDenied", err)
	}

	if len(grants.checked) != 2 || grants.checked[1] != other.ID {
		t.Fatalf("checked = %q, want both principals", grants.checked)
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
