package oidc_test

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/egress/egresshttp"
	"opendustpan.org/opendustpan/internal/issuersvc"
	"opendustpan.org/opendustpan/internal/profileproviders/oidc"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/profile"
	"opendustpan.org/opendustpan/pkg/profile/profiletest"
	"opendustpan.org/opendustpan/pkg/profile/provider"
	"opendustpan.org/opendustpan/pkg/profile/provider/providertest"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

const (
	issuerHost = "https://dustpan.example.com"
	subject    = "namespace:acme:access:write"
	audience   = "https://vault.example.com"
)

func clock() time.Time { return time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC) }

// scopedSealer binds each ciphertext to its scope, as the real sealer does.
type scopedSealer struct{}

func (scopedSealer) Encrypt(_ context.Context, sc secret.Scope, in [][]byte) ([][]byte, error) {
	out := make([][]byte, len(in))
	for i, p := range in {
		out[i] = append([]byte(sc.String()+"|"), p...)
	}

	return out, nil
}

func (scopedSealer) Decrypt(_ context.Context, sc secret.Scope, in [][]byte) ([][]byte, error) {
	out := make([][]byte, len(in))

	for i, c := range in {
		plain, ok := strings.CutPrefix(string(c), sc.String()+"|")
		if !ok {
			return nil, secret.ErrDecrypt
		}

		out[i] = []byte(plain)
	}

	return out, nil
}

// noGrants allows nothing. A login needs no action of the caller: the open
// checked profile:open before it.
type noGrants struct{}

func (noGrants) Authorize(context.Context, identity.Principal, identity.Action, identity.Object) error {
	return identity.ErrDenied
}

func (noGrants) ListObjects(context.Context, identity.Principal, identity.Action) (identity.Scope, error) {
	return identity.Scope{Namespaces: nil, Collections: nil, Units: nil, Principals: nil}, nil
}

// issuer is an in-process issuer over a SQLite store with one namespace.
type issuer struct {
	svc *issuersvc.Service
	ns  namespace.Namespace
}

func newIssuer(t *testing.T, base string) *issuer {
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

	ns := namespace.Namespace{
		ID: namespace.NewID(), CreatedAt: clock(), Name: "acme", PrimaryInstallation: "local",
		FailoverVersion: 1, Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}

	if err = st.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	svc, err := issuersvc.NewService(issuersvc.Config{
		Store: st.Issuer(), Namespaces: st, Sealer: scopedSealer{}, Authorizer: noGrants{}, Notifier: nil,
		Fetcher: nil, IssuerBaseURL: base, PublicURL: "", Now: clock, Log: nil,
	})
	if err != nil {
		t.Fatalf("new issuer: %v", err)
	}

	return &issuer{svc: svc, ns: ns}
}

func (i *issuer) issuerURL() string { return issuerHost + "/oidc/" + i.ns.ID.String() }

// tokens is the token source of one block, bound to an open as the server
// binds it.
func (i *issuer) tokens() issuerTokens {
	return issuerTokens{iss: i, aud: ""}
}

// issuerTokens calls the issuer. A non-empty aud replaces the audience of
// the request, as an issuer with a fault would.
type issuerTokens struct {
	iss *issuer
	aud string
}

func (s issuerTokens) Token(ctx context.Context, aud string, lifetime time.Duration) (profile.Token, error) {
	if s.aud != "" {
		aud = s.aud
	}

	o := profile.OpenContext{
		Claims: map[string]any{"access": "write"}, By: auditBy(),
		Namespace: s.iss.ns.Name, NamespaceID: s.iss.ns.ID, FailoverVersion: s.iss.ns.FailoverVersion,
		Subject: subject, Profile: "vault", ProfileID: "prf-1",
	}

	tok, err := s.iss.svc.Login(ctx, o, "token", aud, lifetime)
	if err != nil {
		return profile.Token{}, fmt.Errorf("login: %w", err)
	}

	return tok, nil
}

// slowTokens answers only after the context of the call ends, as an issuer
// that does not answer.
type slowTokens struct{}

func (slowTokens) Token(ctx context.Context, _ string, _ time.Duration) (profile.Token, error) {
	<-ctx.Done()

	return profile.Token{}, fmt.Errorf("slow issuer: %w", ctx.Err())
}

func auditBy() audit.Entry {
	return audit.Entry{
		At: time.Time{}, Namespace: "", Subject: "alice", IDPSubject: "", TokenID: "", Action: "", Object: "",
		Version: "", ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: "", UserAgent: "",
		Channel: audit.ChannelAPI, Kind: "", Outcome: "", RequestID: "", ObjectID: "", PrevHash: "", Hash: "",
		Sequence: 0, FenceVersion: 0, PrincipalKind: audit.PrincipalKindPerson, JobID: "", Reason: "",
	}
}

func plain(v string) provider.Leaf { return provider.Leaf{Value: v, Secret: false} }

func inputs(aud, lifetime string) provider.Value {
	in := provider.Value{oidc.InputAudience: plain(aud)}
	if lifetime != "" {
		in[oidc.InputTokenLifetime] = plain(lifetime)
	}

	return in
}

// fetchJWKS reads the JWKS of the issuer over HTTPS, as a relying party
// does, through the client of the egress rules.
func fetchJWKS(t *testing.T, iss *issuer) []byte {
	t.Helper()

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		doc, err := iss.svc.JWKS(r.Context(), iss.ns.ID)
		if err != nil {
			http.Error(w, "no JWKS", http.StatusInternalServerError)

			return
		}

		w.Header().Set("Content-Type", "application/json")

		if err := json.NewEncoder(w).Encode(doc); err != nil {
			t.Errorf("encode JWKS: %v", err)
		}
	}))
	defer srv.Close()

	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())

	client := egresshttp.ClientWith(egress.Rules{Allow: nil, LoopbackForTests: true}, egress.NoRedirect,
		egress.HTTPSOnly, egresshttp.Options{RootCAs: pool, Resolver: nil})
	defer client.CloseIdleConnections()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+"/jwks", http.NoBody)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("get JWKS: %v", err)
	}

	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			t.Errorf("close body: %v", cerr)
		}
	}()

	body, err := egresshttp.ReadBody(resp)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("read JWKS: status %d, %v", resp.StatusCode, err)
	}

	return body
}

// verify checks the token of r as a relying party and returns its claims.
func verify(t *testing.T, iss *issuer, r provider.Result, aud string) map[string]any {
	t.Helper()

	tok, ok := r.Value[oidc.OutputToken].Value.(string)
	if !ok {
		t.Fatal("the result has no token")
	}

	claims, err := profiletest.VerifyToken(fetchJWKS(t, iss), tok, profiletest.Expect{
		Now: clock(), Issuer: iss.issuerURL(), Audience: aud,
	})
	if err != nil {
		t.Fatalf("the relying party refused the token: %v", err)
	}

	return claims
}

func TestConformance(t *testing.T) {
	t.Parallel()

	newProvider := func(*testing.T) provider.Provider { return oidc.New(clock) }

	providertest.RunProvider(t, &providertest.Harness{
		New: newProvider,
		Valid: func(t *testing.T) providertest.Env {
			t.Helper()

			iss := newIssuer(t, issuerHost)

			return providertest.Env{Inputs: inputs(audience, ""), Tokens: iss.tokens(), Secrets: nil}
		},
		Slow: func(*testing.T) providertest.Env {
			return providertest.Env{Inputs: inputs(audience, ""), Tokens: slowTokens{}, Secrets: nil}
		},
		CheckResult: func(t *testing.T, env providertest.Env, r provider.Result) {
			t.Helper()

			src, ok := env.Tokens.(issuerTokens)
			if !ok {
				t.Fatalf("the token source is %T", env.Tokens)
			}

			claims := verify(t, src.iss, r, audience)
			if claims["sub"] != subject || claims["purpose"] != "login" {
				t.Errorf("claims = %v", claims)
			}
		},
		Now:      clock,
		Audience: audience,
		Refused: []provider.Value{
			inputs("", ""),
			inputs("aud\nx", ""),
			inputs(strings.Repeat("a", profile.MaxAudienceBytes+1), ""),
			inputs(audience, "30s"),
			inputs(audience, "61m"),
			inputs(audience, "soon"),
		},
		Errors: []providertest.ErrorCase{
			{
				Name: "the issuer refuses an audience of this server",
				Env: func(t *testing.T) providertest.Env {
					t.Helper()

					iss := newIssuer(t, issuerHost)

					return providertest.Env{Inputs: inputs(issuerHost+"/api", ""), Tokens: iss.tokens(), Secrets: nil}
				},
				Want: profile.ErrInvalid,
			},
			{
				Name: "the server has no issuer host",
				Env: func(t *testing.T) providertest.Env {
					t.Helper()

					iss := newIssuer(t, "")

					return providertest.Env{Inputs: inputs(audience, ""), Tokens: iss.tokens(), Secrets: nil}
				},
				Want: provider.ErrUnavailable,
			},
			{
				Name: "the issuer answers a token for another audience",
				Env: func(t *testing.T) providertest.Env {
					t.Helper()

					iss := newIssuer(t, issuerHost)

					return providertest.Env{
						Inputs: inputs(audience, ""), Tokens: issuerTokens{iss: iss, aud: "https://other.example.com"},
						Secrets: nil,
					}
				},
				Want: provider.ErrUnavailable,
			},
		},
	})
}

func TestTokenLifetime(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		lifetime string
		want     time.Duration
	}{
		{"default", "", oidc.DefaultLifetime},
		{"shortest", "1m", profile.MinTokenLifetime},
		{"thirty minutes", "30m", 30 * time.Minute},
		{"longest", "60m", profile.MaxTokenLifetime},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			iss := newIssuer(t, issuerHost)

			res, err := oidc.New(clock).Open(t.Context(), provider.Request{
				Inputs: inputs(audience, tc.lifetime), Claims: nil, Tokens: iss.tokens(), Login: nil,
			})
			if err != nil {
				t.Fatalf("Open: %v", err)
			}

			checkLifetime(t, iss, res, tc.want)
		})
	}
}

// checkLifetime checks the outputs of a token of the lifetime want, and the
// token itself as a relying party.
func checkLifetime(t *testing.T, iss *issuer, res provider.Result, want time.Duration) {
	t.Helper()

	exp := clock().Add(want)
	if !res.Expires.Equal(exp) {
		t.Errorf("Expires = %s, want %s", res.Expires, exp)
	}

	claims := verify(t, iss, res, audience)

	expClaim, okExp := claims["exp"].(float64)
	iat, okIat := claims["iat"].(float64)

	if !okExp || !okIat || time.Duration(expClaim-iat)*time.Second != want {
		t.Errorf("exp = %v, iat = %v: want a lifetime of %s", claims["exp"], claims["iat"], want)
	}

	if res.Value[oidc.OutputExpiresAt].Value != exp.Format(time.RFC3339) ||
		res.Value[oidc.OutputAudience].Value != audience || res.Value[oidc.OutputIssuer].Value != iss.issuerURL() {
		t.Errorf("outputs = %v", res.Value)
	}

	if res.Identity != subject {
		t.Errorf("Identity = %q, want the subject %q", res.Identity, subject)
	}
}

// TestRunThroughTheRegistry runs a login/oidc block through the runtime, and
// checks that a refused audience leaves no token in the error text.
func TestRunThroughTheRegistry(t *testing.T) {
	t.Parallel()

	reg, err := provider.NewRegistry(clock, oidc.New(clock))
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	for _, tc := range []struct {
		name   string
		tokens func(*issuer) provider.TokenSource
		aud    string
		want   error
		issued int
	}{
		{"valid", func(i *issuer) provider.TokenSource { return i.tokens() }, audience, nil, 1},
		{"refused audience", func(i *issuer) provider.TokenSource { return i.tokens() }, issuerHost, profile.ErrInvalid, 0},
		{
			"token for another audience",
			func(i *issuer) provider.TokenSource { return issuerTokens{iss: i, aud: "https://other.example.com"} },
			audience, provider.ErrUnavailable, 1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			iss := newIssuer(t, issuerHost)
			rec := &providertest.Recorder{Next: tc.tokens(iss)}
			blocks := []provider.Block{
				{Name: "vault", Use: "login/oidc", Inputs: inputs(tc.aud, ""), Login: "", Timeout: 0},
			}

			got, err := reg.Run(t.Context(), blocks, provider.Call{
				Claims: nil, Tokens: func(string) provider.TokenSource { return rec }, Late: ignoreLate, Limit: 0,
			})
			if !errors.Is(err, tc.want) {
				t.Fatalf("Run = %v, want %v", err, tc.want)
			}

			checkRun(t, got, err, rec.Values())
			checkEntries(t, got.Tokens, tc.issued)
		})
	}
}

// checkEntries checks that Run gives the caller the audit entry of each
// token that the issuer signed, also when the block failed after it.
func checkEntries(t *testing.T, issued []provider.Issued, want int) {
	t.Helper()

	if len(issued) != want {
		t.Fatalf("Run gave %d tokens to commit, want %d", len(issued), want)
	}

	for n := range issued {
		i := &issued[n]
		hasJTI := slices.ContainsFunc(i.Entry.Changes, func(c audit.Change) bool {
			return c.Key == "jti" && c.New == i.ID
		})

		if i.Block != "vault" || i.ID == "" || i.Entry.Action == "" || !hasJTI {
			t.Errorf("issued token = %+v, want the entry of the issuer with its jti", i)
		}
	}
}

// checkRun checks that an error of Run holds no token, and that a result
// marks the token as a secret.
func checkRun(t *testing.T, got provider.Outcome, err error, tokens []string) {
	t.Helper()

	if err == nil {
		if !got.Results["vault"].Value[oidc.OutputToken].Secret {
			t.Error("the token has no secret mark")
		}

		return
	}

	for _, v := range tokens {
		if strings.Contains(err.Error(), v) {
			t.Errorf("the error holds a token: %v", err)
		}
	}
}

// ignoreLate is a receiver of late tokens for a test that expects none.
func ignoreLate(provider.Issued) {}
