package github_test

import (
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/fakecred"
	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/forge"
	"opendustpan.org/opendustpan/pkg/forge/forgetest"
	"opendustpan.org/opendustpan/pkg/forge/github"
)

func loopbackRules(t *testing.T) egress.Rules {
	t.Helper()

	return egress.Rules{Allow: nil, LoopbackForTests: true}
}

func newOpener(t *testing.T, f *fake, rules egress.Rules) *github.Opener {
	t.Helper()

	pool := x509.NewCertPool()
	pool.AddCert(f.srv.Certificate())

	o, err := github.NewOpener(
		github.Config{Rules: rules, RootCAs: pool, Now: nil, Prover: testProver{}, AppBudget: nil},
	)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(o.CloseIdleConnections)

	return o
}

func appConnection(f *fake) forge.Connection {
	return forge.Connection{
		ID: "c1", Kind: forge.KindGitHub, Auth: forge.AuthApp, BaseURL: f.srv.URL,
		Params: map[string]string{forge.ParamAppID: fakeAppID, forge.ParamInstallationID: fakeInstallationID},
		Push:   true,
	}
}

func tokenConnection(f *fake) forge.Connection {
	return forge.Connection{
		ID:      "c2",
		Kind:    forge.KindGitHub,
		Auth:    forge.AuthToken,
		BaseURL: f.srv.URL,
		Params:  nil,
		Push:    true,
	}
}

// harness returns a factory of harnesses for the suite, one fake GitHub API
// for each case.
func harness(auth forge.AuthKind) forgetest.Factory {
	return func(t *testing.T) forgetest.Harness {
		t.Helper()

		key := testKey()
		userToken := "github_pat_" + randomHex(t, 24)
		f := newFake(t, key, userToken)
		o := newOpener(t, f, loopbackRules(t))

		conn, secret := appConnection(f), pkcs1PEM(key)
		if auth == forge.AuthToken {
			conn, secret = tokenConnection(f), []byte(userToken)
		}

		cred := forge.Credential{SetID: "s1", Secret: secret, CreatedAt: time.Now()}

		fg, err := o.Open(t.Context(), conn, cred)
		if err != nil {
			t.Fatalf("Open = %v", err)
		}

		return forgetest.Harness{
			Opener:      o,
			Connection:  conn,
			Credential:  cred,
			EventSecret: []byte(randomHex(t, 32)),
			Forge:       fg,
			Account: forge.Account{
				ID:        fakeAccountID,
				Login:     fakeLogin,
				ExpiresAt: time.Time{},
				NoExpiry:  false,
				Scopes:    nil,
			},
			Repo:    "acme/infra",
			Fixture: f,
		}
	}
}

func TestConformanceApp(t *testing.T) {
	t.Parallel()
	forgetest.Run(t, conformanceHarness(forge.AuthApp))
}

func TestConformanceToken(t *testing.T) {
	t.Parallel()
	forgetest.Run(t, conformanceHarness(forge.AuthToken))
}

func TestKind(t *testing.T) {
	t.Parallel()

	o, err := github.NewOpener(
		github.Config{
			Rules:     egress.Rules{Allow: nil, LoopbackForTests: false},
			RootCAs:   nil,
			Now:       nil,
			Prover:    nil,
			AppBudget: nil,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	if o.Kind() != forge.KindGitHub {
		t.Fatalf("Kind = %q", o.Kind())
	}
}

// fakeOf returns the fake API of a harness.
func fakeOf(t *testing.T, h forgetest.Harness) *fake {
	t.Helper()

	f, ok := h.Fixture.(*fake)
	if !ok {
		t.Fatal("the fixture is not the fake GitHub API")
	}

	return f
}

// calls returns the number of calls that the fake got.
func calls(t *testing.T, f *fake) int {
	t.Helper()

	n, err := f.Calls(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	return n
}

// number reads a number claim.
func number(t *testing.T, claims map[string]any, key string) int64 {
	t.Helper()

	v, ok := claims[key].(float64)
	if !ok {
		t.Fatalf("the claim %q is not a number: %v", key, claims[key])
	}

	return int64(v)
}

// TestCheckAppSendsAValidJWT checks the JWT that Check sends: three
// segments, the header, the claims and a signature that the public key
// verifies (the fake verifies the signature before it answers).
func TestCheckAppSendsAValidJWT(t *testing.T) {
	t.Parallel()

	h := harness(forge.AuthApp)(t)
	f := fakeOf(t, h)
	before := time.Now()

	acct, err := h.Forge.Check(t.Context())
	if err != nil {
		t.Fatalf("Check = %v", err)
	}

	if want := []string{"contents:write", "metadata:read"}; strings.Join(acct.Scopes, ",") != strings.Join(want, ",") {
		t.Errorf("scopes = %v, want %v", acct.Scopes, want)
	}

	if !acct.ExpiresAt.IsZero() || acct.NoExpiry {
		t.Errorf("an app account has expiry %v, no expiry %v", acct.ExpiresAt, acct.NoExpiry)
	}

	jwt, minted := f.seen()
	if minted != 1 {
		t.Errorf("Check asked for %d installation tokens, want 1", minted)
	}

	checkJWT(t, jwt, before)
}

// TestCheckAppAsksForTheSmallestTokenAndRevokesIt checks that Check asks for
// the metadata read permission only, and revokes the token after the call.
func TestCheckAppAsksForTheSmallestTokenAndRevokesIt(t *testing.T) {
	t.Parallel()

	h := harness(forge.AuthApp)(t)
	f := fakeOf(t, h)

	if _, err := h.Forge.Check(t.Context()); err != nil {
		t.Fatalf("Check = %v", err)
	}

	body, revoked := f.revocation()
	if body != `{"permissions":{"metadata":"read"}}` {
		t.Errorf("token request body = %q, want the metadata read permission only", body)
	}

	if revoked != 1 {
		t.Errorf("Check revoked %d tokens, want 1", revoked)
	}
}

// checkJWT checks the form, the signature, the header and the claims of a
// JWT that was made at about the time before.
func checkJWT(t *testing.T, jwt string, before time.Time) {
	t.Helper()

	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		t.Fatalf("the JWT has %d segments", len(parts))
	}

	if err := verifyJWT(jwt, &testKey().PublicKey); err != nil {
		t.Fatalf("the signature does not verify: %v", err)
	}

	if strings.ContainsAny(jwt, "=+/") {
		t.Error("the JWT is not base64url with no padding")
	}

	header, claims := decodeSegment(t, parts[0]), decodeSegment(t, parts[1])
	if header["alg"] != "RS256" || header["typ"] != "JWT" || len(header) != 2 {
		t.Errorf("header = %v", header)
	}

	if claims["iss"] != fakeAppID {
		t.Errorf("iss = %v, want the app ID as a string", claims["iss"])
	}

	iat, exp := number(t, claims, "iat"), number(t, claims, "exp")

	okIssue := iat <= before.Unix()-59 && iat >= before.Unix()-65
	okLife := exp-iat <= 600 && exp > before.Unix()

	if !okIssue || !okLife {
		t.Errorf(
			"the JWT lives from %d to %d: it starts about 60 seconds before %d and ends within 10 minutes",
			iat,
			exp,
			before.Unix(),
		)
	}
}

func decodeSegment(t *testing.T, seg string) map[string]any {
	t.Helper()

	raw, err := base64.RawURLEncoding.DecodeString(seg)
	if err != nil {
		t.Fatal(err)
	}

	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}

	return m
}

func TestOpenAcceptsPKCS8(t *testing.T) {
	t.Parallel()

	f := newFake(t, testKey(), "")
	o := newOpener(t, f, loopbackRules(t))
	cred := forge.Credential{SetID: "s", Secret: pkcs8PEM(t, testKey()), CreatedAt: time.Time{}}

	fg, err := o.Open(t.Context(), appConnection(f), cred)
	if err != nil {
		t.Fatalf("Open = %v", err)
	}

	if _, err := fg.Check(t.Context()); err != nil {
		t.Fatalf("Check = %v", err)
	}
}

// TestOpenHostile sends hostile connections straight to Open. Each one
// fails with ErrInvalid and makes no call.
func TestOpenHostile(t *testing.T) {
	t.Parallel()

	good := appConnection
	withBase := func(base string) func(*fake) forge.Connection {
		return func(f *fake) forge.Connection { c := good(f); c.BaseURL = base; return c }
	}
	withParam := func(k, v string) func(*fake) forge.Connection {
		return func(f *fake) forge.Connection {
			c := good(f)
			c.Params = map[string]string{
				forge.ParamAppID: fakeAppID, forge.ParamInstallationID: fakeInstallationID, k: v,
			}
			return c
		}
	}

	ec := pkcs8PEM(t, ecKey(t))
	small := pkcs1PEM(smallKey(t))

	tests := []struct {
		name string
		conn func(*fake) forge.Connection
		key  []byte
	}{
		{"an empty address", withBase(""), pkcs1PEM(testKey())},
		{"an http address", withBase("http://127.0.0.1:1"), pkcs1PEM(testKey())},
		{
			"an address with user information",
			withBase(fakecred.URL("https", "user", "pw", "127.0.0.1:1", "")),
			pkcs1PEM(testKey()),
		},
		{"an address with a query", withBase("https://127.0.0.1:1/?x=1"), pkcs1PEM(testKey())},
		{"an address with a fragment", withBase("https://127.0.0.1:1/#f"), pkcs1PEM(testKey())},
		{"localhost", withBase("https://localhost:1"), pkcs1PEM(testKey())},
		{"a number as the host", withBase("https://2130706433:1"), pkcs1PEM(testKey())},
		{"a private address", withBase("https://192.168.1.1"), pkcs1PEM(testKey())},
		{"a metadata address", withBase("https://169.254.169.254"), pkcs1PEM(testKey())},
		{"an app ID that is not a number", withParam(forge.ParamAppID, "12a"), pkcs1PEM(testKey())},
		{"an app ID with a leading zero", withParam(forge.ParamAppID, "012"), pkcs1PEM(testKey())},
		{"an installation ID with a slash", withParam(forge.ParamInstallationID, "1/../2"), pkcs1PEM(testKey())},
		{"an installation ID that is empty", withParam(forge.ParamInstallationID, ""), pkcs1PEM(testKey())},
		{"no key", good, nil},
		{"text as the key", good, []byte("hello")},
		{"a key of another type", good, ec},
		{"a key of 1024 bits", good, small},
		{"a certificate as the key", good, []byte("-----BEGIN CERTIFICATE-----\nAAAA\n-----END CERTIFICATE-----\n")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := newFake(t, testKey(), "")
			o := newOpener(t, f, loopbackRules(t))
			cred := forge.Credential{SetID: "s", Secret: tt.key, CreatedAt: time.Time{}}

			fg, err := o.Open(t.Context(), tt.conn(f), cred)
			if !errors.Is(err, forge.ErrInvalid) || fg != nil {
				t.Fatalf("Open = %v, %v; want ErrInvalid", fg, err)
			}

			if n := calls(t, f); n != 0 {
				t.Fatalf("Open made %d calls", n)
			}

			if len(tt.key) > 20 && strings.Contains(err.Error(), string(tt.key[len(tt.key)/2:])) {
				t.Fatalf("the error holds a part of the key: %v", err)
			}
		})
	}
}

// TestDefaultRulesRefuseLoopback checks that an opener with no allowed
// range refuses the fake GitHub API, which listens on loopback, before any
// call.
func TestDefaultRulesRefuseLoopback(t *testing.T) {
	t.Parallel()

	f := newFake(t, testKey(), "")
	o := newOpener(t, f, egress.Rules{Allow: nil, LoopbackForTests: false})
	cred := forge.Credential{SetID: "s", Secret: pkcs1PEM(testKey()), CreatedAt: time.Time{}}

	if _, err := o.Open(t.Context(), appConnection(f), cred); !errors.Is(err, forge.ErrInvalid) {
		t.Fatalf("Open = %v, want ErrInvalid", err)
	}

	if n := calls(t, f); n != 0 {
		t.Fatalf("%d calls reached the server", n)
	}
}

// TestRedirectIsRefused checks that the adapter follows no redirect, also
// to a host that the rules refuse, and sends no credential there.
func TestRedirectIsRefused(t *testing.T) {
	t.Parallel()

	for _, target := range []string{"https://169.254.169.254/latest", "https://example.com/x", "/elsewhere"} {
		f := newFake(t, testKey(), "")
		f.redirect(target)

		o := newOpener(t, f, loopbackRules(t))
		cred := forge.Credential{SetID: "s", Secret: pkcs1PEM(testKey()), CreatedAt: time.Time{}}

		fg, err := o.Open(t.Context(), appConnection(f), cred)
		if err != nil {
			t.Fatal(err)
		}

		if _, err := fg.Check(t.Context()); err == nil {
			t.Fatalf("Check followed a redirect to %s", target)
		}

		if n := calls(t, f); n != 1 {
			t.Fatalf("redirect to %s: %d calls, want 1", target, n)
		}
	}
}

// TestStatusMapping checks the error of each status that GitHub can answer.
func TestStatusMapping(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		status  int
		headers map[string]string
		want    error
		wait    time.Duration
	}{
		{"unauthorized", 401, nil, forge.ErrAuth, 0},
		{"forbidden", 403, nil, forge.ErrAuth, 0},
		{"installation not found", 404, nil, forge.ErrAuth, 0},
		{"too many requests", 429, map[string]string{"Retry-After": "11"}, forge.ErrUnavailable, 11 * time.Second},
		{
			"forbidden by a rate limit", 403,
			map[string]string{"X-RateLimit-Remaining": "0", "Retry-After": "5"},
			forge.ErrUnavailable, 5 * time.Second,
		},
		{
			"forbidden by a rate limit with no wait", 403,
			map[string]string{"X-RateLimit-Remaining": "0"},
			forge.ErrUnavailable, 0,
		},
		{
			"too many requests with a wait above the cap", 429,
			map[string]string{"Retry-After": "7200"},
			forge.ErrUnavailable, time.Hour,
		},
		{
			"too many requests with a wait that overflows", 429,
			map[string]string{"Retry-After": "9223372036854775807"},
			forge.ErrUnavailable, time.Hour,
		},
		{
			"forbidden by a rate limit with a reset far in the future", 403,
			map[string]string{"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": "99999999999"},
			forge.ErrUnavailable, time.Hour,
		},
		{"server error", 500, nil, forge.ErrUnavailable, 0},
		{"bad gateway", 502, nil, forge.ErrUnavailable, 0},
		{"another status", 418, nil, forge.ErrUnavailable, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := newFake(t, testKey(), "")
			f.respondWith(tt.status, tt.headers)

			o := newOpener(t, f, loopbackRules(t))
			cred := forge.Credential{SetID: "s", Secret: pkcs1PEM(testKey()), CreatedAt: time.Time{}}

			fg, err := o.Open(t.Context(), appConnection(f), cred)
			if err != nil {
				t.Fatal(err)
			}

			_, err = fg.Check(t.Context())
			if !errors.Is(err, tt.want) {
				t.Fatalf("Check = %v, want %v", err, tt.want)
			}

			if got, _ := forge.RetryAfter(err); got != tt.wait {
				t.Fatalf("RetryAfter = %v, want %v", got, tt.wait)
			}
		})
	}
}

// TestUnusedMethodsAreUnsupported checks that the methods that are not in
// this version name GitHub in their error, and that the adapter lists the
// capabilities of the files, of the comparison, of the tree, of the commits, of
// the review requests, of the statuses and of the comments only.
func TestUnusedMethodsAreUnsupported(t *testing.T) {
	t.Parallel()

	h := harness(forge.AuthApp)(t)

	caps := h.Forge.Capabilities()
	want := forge.Capabilities{
		forge.CapFiles, forge.CapCompare, forge.CapTree, forge.CapCommit, forge.CapReview, forge.CapStatus,
		forge.CapComment,
	}
	if !slices.Equal(caps, want) {
		t.Fatalf("Capabilities = %v, want %v", caps, want)
	}

	_, err := h.Forge.RepoToken(t.Context(), "acme/infra")
	if !errors.Is(err, forge.ErrUnsupported) || !strings.Contains(err.Error(), "GitHub") {
		t.Fatalf("RepoToken = %v", err)
	}
}
