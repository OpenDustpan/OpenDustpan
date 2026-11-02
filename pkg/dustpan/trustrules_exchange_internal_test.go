package dustpan

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/issuersvc"
	"opendustpan.org/opendustpan/internal/trustrulesvc"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/profile"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/trustrule"
)

// exchangeRuleStore holds the rules of one namespace for the exchange. It
// never writes: every refusal in these tests happens before a write.
type exchangeRuleStore struct {
	rules []trustrule.Rule
}

func (s *exchangeRuleStore) Create(context.Context, trustrule.Rule, namespace.Fence, audit.Entry) error {
	return trustrule.ErrInvalid
}

func (s *exchangeRuleStore) Get(context.Context, namespace.Name, trustrule.ID) (trustrule.Rule, error) {
	return trustrule.Rule{
		CreatedAt: time.Time{}, ID: "", Namespace: "", Name: "", Issuer: "", Audience: "", Keys: "",
		StaticKeys: "", PrincipalID: "", CreatedBy: "", Conditions: nil,
	}, trustrule.ErrNotFound
}

func (s *exchangeRuleStore) List(context.Context, namespace.Name) ([]trustrule.Rule, error) {
	return s.rules, nil
}

func (s *exchangeRuleStore) Delete(context.Context, namespace.Name, trustrule.ID, namespace.Fence,
	audit.Entry) error {
	return trustrule.ErrNotFound
}

func (s *exchangeRuleStore) RecordExchange(context.Context, trustrule.Exchange, namespace.Fence,
	audit.Entry) error {
	return trustrule.ErrNotFound
}

// exchangeKey signs tokens with one Ed25519 key. Its JWKS is the static key
// of the rule.
type exchangeKey struct {
	priv ed25519.PrivateKey
	jwks string
}

func newExchangeKey(t *testing.T) exchangeKey {
	t.Helper()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	return exchangeKey{
		priv: priv,
		jwks: `{"keys":[{"kty":"OKP","crv":"Ed25519","kid":"k1","x":"` + base64.RawURLEncoding.EncodeToString(pub) +
			`"}]}`,
	}
}

// token returns a valid JWT of the issuer iss, for the audience of the rule.
func (k exchangeKey) token(t *testing.T, iss string) string {
	t.Helper()

	enc := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}

		return base64.RawURLEncoding.EncodeToString(b)
	}

	now := time.Now()
	header := map[string]any{"alg": "EdDSA", "typ": "JWT", "kid": "k1"}
	claims := map[string]any{
		"iss": iss, "aud": "https://dustpan.example.com", "sub": "repo:acme/infra:ref:refs/heads/main",
		"iat": now.Unix(), "exp": now.Add(5 * time.Minute).Unix(), "jti": "run-1",
	}

	input := enc(header) + "." + enc(claims)

	return input + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(k.priv, []byte(input)))
}

// exchangeFixture builds the token exchange of the server for the namespace
// acme. The rule of the namespace has the issuer ruleIssuer and the static
// key k. When withIssuer is true, the server has an issuer service with the
// issuerURL https://files.example.com/acme.
func exchangeFixture(t *testing.T, withIssuer bool, ruleIssuer string, k exchangeKey) (*trustrulesvc.Exchanger,
	*sqlite.Store) {
	t.Helper()

	st := openCollectionStore(t)

	ns := namespace.Namespace{
		ID: namespace.NewID(), CreatedAt: time.Now(), Name: "acme", PrimaryInstallation: "local", FailoverVersion: 1,
		Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}
	if err := st.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	set := profile.DefaultIssuerSettings(ns.Name)
	set.IssuerURL = "https://files.example.com/acme"

	entry := audit.Entry{
		At:            time.Now(),
		Namespace:     ns.Name,
		Subject:       "ada",
		Action:        "namespace:manage-issuer",
		Object:        "namespace:acme",
		Channel:       audit.ChannelAPI,
		Kind:          audit.KindChange,
		Outcome:       audit.OutcomeSuccess,
		PrincipalKind: audit.PrincipalKindPerson,
		IDPSubject:    "",
		TokenID:       "",
		Version:       "",
		ValuePath:     "",
		ValueCount:    nil,
		Changes:       nil,
		SourceIP:      "",
		UserAgent:     "",
		RequestID:     "",
		ObjectID:      "",
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		FenceVersion:  0,
		JobID:         "",
		Reason:        "",
	}

	if _, err := st.Issuer().PutIssuerSettings(t.Context(), set, 0, ns.Fence(), []audit.Entry{entry}); err != nil {
		t.Fatalf("put settings: %v", err)
	}

	nsSvc, err := namespace.NewService(namespace.Config{Store: st, Now: nil, Installation: "local"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	issuer, err := issuersvc.NewService(issuersvc.Config{
		Store: st.Issuer(), Namespaces: st, Sealer: noSealer{}, Authorizer: identity.AdminOnly{},
		Notifier: nil, Fetcher: nil, IssuerBaseURL: "https://issuer.example.com", PublicURL: "https://api.example.com",
		Now: nil, Log: nil,
	})
	if err != nil {
		t.Fatalf("issuer service: %v", err)
	}

	rule := trustrule.Rule{
		CreatedAt:   time.Now(),
		ID:          trustrule.NewID(),
		Namespace:   "acme",
		Name:        "deploy",
		Issuer:      ruleIssuer,
		Audience:    "https://dustpan.example.com",
		Keys:        trustrule.KeySourceStatic,
		StaticKeys:  k.jwks,
		PrincipalID: "prn-worker",
		CreatedBy:   "ada",
		Conditions:  []trustrule.Condition{{Claim: "sub", Values: []string{"repo:acme/infra:ref:refs/heads/main"}}},
	}
	rules := &exchangeRuleStore{rules: []trustrule.Rule{rule}}

	cfg := defaultSettings()
	cfg.namespaceStore = st
	cfg.trustRules.store = rules
	cfg.authorizer = identity.AdminOnly{}
	cfg.publicURL = "https://api.example.com"

	var svc services
	svc.namespaces = nsSvc
	if withIssuer {
		svc.issuer = issuer
	}

	x, err := newExchanger(svc, cfg, rules)
	if err != nil {
		t.Fatalf("token exchange: %v", err)
	}

	return x, st
}

// TestTheExchangeAsksTheIssuer proves the wiring of the server: the token
// exchange refuses a token from an issuer of this installation. Each refusal
// answers ErrNoMatch, and its audit entry names the failed check.
func TestTheExchangeAsksTheIssuer(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		withIssuer bool
		issuer     string
		wantReason string
	}{
		{
			name:       "an issuerURL of a namespace, with the issuer service",
			withIssuer: true,
			issuer:     "https://files.example.com/acme",
			wantReason: trustrule.ErrIssuerRefused.Error(),
		},
		{
			name:       "an issuer at the issuer base URL, with the issuer service",
			withIssuer: true,
			issuer:     "https://issuer.example.com/oidc/ns-1",
			wantReason: trustrule.ErrIssuerRefused.Error(),
		},
		{
			name:       "the public URL, with no issuer service",
			withIssuer: false,
			issuer:     "https://api.example.com/oidc/ns-1",
			wantReason: trustrule.ErrIssuerRefused.Error(),
		},
		{
			name:       "an issuerURL of a namespace, with no issuer service, is not an own issuer",
			withIssuer: false,
			issuer:     "https://files.example.com/acme",
			wantReason: trustrule.ErrPrincipal.Error(),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			k := newExchangeKey(t)
			x, st := exchangeFixture(t, tc.withIssuer, tc.issuer, k)

			req := audit.Entry{
				At:            time.Time{},
				Namespace:     "",
				Subject:       "ada",
				Action:        "",
				Object:        "",
				Channel:       audit.ChannelAPI,
				Kind:          "",
				Outcome:       "",
				PrincipalKind: "",
				IDPSubject:    "",
				TokenID:       "",
				Version:       "",
				ValuePath:     "",
				ValueCount:    nil,
				Changes:       nil,
				SourceIP:      "",
				UserAgent:     "",
				RequestID:     "",
				ObjectID:      "",
				PrevHash:      "",
				Hash:          "",
				Sequence:      0,
				FenceVersion:  0,
				JobID:         "",
				Reason:        "",
			}

			in := trustrulesvc.ExchangeInput{
				Namespace: "acme",
				Token:     k.token(t, tc.issuer),
				Rule:      "",
				Request:   req,
			}

			_, err := x.Exchange(t.Context(), in)
			if !errors.Is(err, trustrulesvc.ErrNoMatch) {
				t.Fatalf("Exchange = %v, want ErrNoMatch", err)
			}

			last, ok, err := st.LastEntry(t.Context(), "acme")
			if err != nil {
				t.Fatalf("last audit entry: %v", err)
			}

			if !ok {
				t.Fatal("no audit entry of the refused exchange")
			}

			if last.Action != trustrulesvc.ActionExchange {
				t.Errorf("action = %q, want %q", last.Action, trustrulesvc.ActionExchange)
			}

			if last.Outcome != audit.OutcomeDenied {
				t.Errorf("outcome = %q, want %q", last.Outcome, audit.OutcomeDenied)
			}

			if !strings.Contains(last.Reason, tc.wantReason) {
				t.Errorf("reason = %q, want it to contain %q", last.Reason, tc.wantReason)
			}

			if tc.wantReason == trustrule.ErrPrincipal.Error() &&
				strings.Contains(last.Reason, trustrule.ErrIssuerRefused.Error()) {
				t.Errorf("reason = %q, must not name the own-issuer check", last.Reason)
			}
		})
	}
}
